package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func jsonlFixture(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func writeJSONL(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func jsonlInput(path string) HookInput {
	return HookInput{Event: "Stop", SessionID: "session-1", GenerationID: "generation-1", TranscriptPath: path, Model: "synthetic-model", Version: "2.148.0"}
}

func TestJSONLMatchesLegacyTurnsAndFingerprints(t *testing.T) {
	for _, capture := range []string{"none", "preview", "full"} {
		t.Run(capture, func(t *testing.T) {
			input := jsonlInput(fixture(t, "normal"))
			legacy, pending, _, err := Read(input, testConfig(capture))
			if err != nil || pending || len(legacy) != 1 {
				t.Fatalf("legacy: %v %v", pending, err)
			}
			input.TranscriptPath = writeJSONL(t, jsonlFixture(t))
			modern, pending, _, err := Read(input, testConfig(capture))
			if err != nil || pending || !reflect.DeepEqual(legacy, modern) {
				t.Fatalf("JSONL differs: pending=%v err=%v\nlegacy=%+v\nmodern=%+v", pending, err, legacy, modern)
			}
			if Fingerprint(legacy[0]) != Fingerprint(modern[0]) {
				t.Fatal("format changed upload fingerprint")
			}
		})
	}
}

func TestJSONLUsageDeduplicatesParallelCallsAndRepeatedSnapshots(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(jsonlFixture(t)), "\n")
	secondCall := strings.NewReplacer(`"id": "a1"`, `"id": "parallel"`, `"callId": "call-1"`, `"callId": "call-2"`).Replace(lines[1])
	secondResult := strings.NewReplacer(`"id": "t1"`, `"id": "parallel-result"`, `"callId": "call-1"`, `"callId": "call-2"`).Replace(lines[2])
	body := strings.Join([]string{lines[0], `{"type":"file-history-snapshot","content":"ignored metadata"}`, lines[1], secondCall, lines[2], secondResult, lines[3], lines[3]}, "\n")
	turns, pending, _, err := Read(jsonlInput(writeJSONL(t, body)), testConfig("preview"))
	if err != nil || pending || len(turns) != 1 {
		t.Fatalf("pending=%v err=%v turns=%d", pending, err, len(turns))
	}
	turn := turns[0]
	if len(turn.ToolCalls) != 2 || turn.Usage.InputTokens != 120 || turn.Usage.OutputTokens != 32 || turn.Usage.CacheReadTokens != 20 || turn.Usage.ReasoningTokens != 4 || turn.OutputPreview != "Synthetic inspection completed." {
		t.Fatalf("duplicate telemetry: %+v", turn)
	}
}

func TestJSONLTerminalAndRequestSelection(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(jsonlFixture(t)), "\n")
	for _, tc := range []struct {
		name, body, event, generation, status string
		pending                               bool
		count                                 int
	}{
		{"running", lines[0], "Stop", "generation-1", "", true, 0},
		{"cancelled", lines[0], "SessionEnd", "", "cancelled", false, 1},
		{"missing generation", jsonlFixture(t), "Stop", "missing", "", true, 0},
		{"missing tool result", strings.Join([]string{lines[0], lines[1], lines[3]}, "\n"), "Stop", "generation-1", "", true, 0},
		{"nonterminal tool result", strings.Replace(jsonlFixture(t), `"status": "completed"`, `"status": "running"`, 1), "Stop", "generation-1", "", true, 0},
		{"failed tool", strings.Replace(jsonlFixture(t), `"status": "completed"`, `"status": "failed"`, 1), "Stop", "generation-1", "completed", false, 1},
		{"incomplete assistant", strings.Join([]string{lines[0], lines[1], lines[2], strings.ReplaceAll(lines[3], `"completed"`, `"in_progress"`)}, "\n"), "Stop", "generation-1", "", true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := jsonlInput(writeJSONL(t, tc.body))
			input.Event = tc.event
			input.GenerationID = tc.generation
			turns, pending, _, err := Read(input, testConfig("preview"))
			if err != nil || pending != tc.pending || len(turns) != tc.count {
				t.Fatalf("turns=%+v pending=%v err=%v", turns, pending, err)
			}
			if tc.count > 0 && string(turns[0].FinalStatus) != tc.status {
				t.Fatalf("status=%v", turns[0].FinalStatus)
			}
			if tc.name == "failed tool" && turns[0].ToolCalls[0].Status != "error" {
				t.Fatal("missing tool error")
			}
		})
	}
	completed := jsonlFixture(t)
	running := strings.ReplaceAll(lines[0], `"generation-1"`, `"generation-2"`)
	running = strings.ReplaceAll(running, `"u1"`, `"u2"`)
	input := jsonlInput(writeJSONL(t, completed+running))
	turns, pending, _, err := Read(input, testConfig("none"))
	if err != nil || pending || len(turns) != 1 {
		t.Fatalf("selected stop=%+v %v %v", turns, pending, err)
	}
	input.Event = "SessionEnd"
	turns, pending, _, err = Read(input, testConfig("none"))
	if err != nil || pending || len(turns) != 2 || turns[1].FinalStatus != "cancelled" {
		t.Fatalf("session end=%+v %v %v", turns, pending, err)
	}
}

func TestJSONLPartialWritesAndMalformedRecordsDoNotExport(t *testing.T) {
	body := jsonlFixture(t)
	for _, bad := range []string{
		body + `{"type":"message","content":"private-transcript-text`,
		strings.Replace(body, `"id": "u1"`, `"id": "../escape"`, 1),
		strings.Replace(body, `"timestamp": 1767225600000`, `"timestamp": null`, 1),
		strings.Replace(body, `"conversationRequestId": "generation-1"`, `"sessionId": "other-session"`, 1),
		strings.Replace(body, `"messageId": "provider-a1"`, `"unused": "provider-a1"`, 1),
		strings.Replace(body, `"type": "message"`, `"sessionId":"other-session","type": "message"`, 1),
	} {
		input := jsonlInput(writeJSONL(t, bad))
		turns, pending, _, err := Read(input, testConfig("none"))
		if err == nil || !pending || len(turns) != 0 {
			t.Fatalf("invalid record accepted: pending=%v err=%v turns=%d", pending, err, len(turns))
		}
		if strings.Contains(err.Error(), "private-transcript-text") {
			t.Fatal("error leaked transcript")
		}
	}
	path := writeJSONL(t, body+`{"type":`)
	input := jsonlInput(path)
	if _, _, _, err := Read(input, testConfig("none")); err == nil {
		t.Fatal("partial record accepted")
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if turns, pending, _, err := Read(input, testConfig("none")); err != nil || pending || len(turns) != 1 {
		t.Fatalf("retry did not recover: %v %v", pending, err)
	}
}

func TestJSONLLargeRecordAndUpdatedSnapshot(t *testing.T) {
	lines := strings.Split(strings.TrimSpace(jsonlFixture(t)), "\n")
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[3]), &event); err != nil {
		t.Fatal(err)
	}
	event["content"] = []any{map[string]any{"type": "output_text", "text": strings.Repeat("x", 128*1024)}}
	large, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	input := jsonlInput(writeJSONL(t, jsonlFixture(t)+string(large)))
	turns, pending, _, err := Read(input, testConfig("none"))
	if err != nil || pending || len(turns) != 1 || turns[0].OutputLength != 128*1024 || turns[0].Usage.InputTokens != 120 {
		t.Fatalf("large snapshot: %v %v %+v", pending, err, turns)
	}
}
