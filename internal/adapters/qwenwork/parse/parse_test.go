package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func fixture(t *testing.T) (HookInput, config.Config, []HookInput) {
	t.Helper()
	p := "testdata/personal-1.2.5.jsonl"
	cfg := config.Config{CaptureContent: "preview", MaxChars: 20000}
	in := HookInput{Event: "Stop", SessionID: "test-session", TranscriptPath: p, ParentRequestSetID: "turn-2", ObservedAt: time.Date(2026, 10, 8, 6, 21, 35, 0, time.UTC).UnixNano()}
	ev := []HookInput{{Event: "Stop", ParentRequestSetID: "turn-1", ObservedAt: time.Date(2026, 10, 8, 6, 21, 10, 0, time.UTC).UnixNano()}}
	ev = append(ev, HookInput{Event: "UserPromptSubmit", RequestSetID: "turn-2", ObservedAt: time.Date(2026, 10, 8, 6, 21, 24, 0, time.UTC).UnixNano()})
	return in, cfg, ev
}
func TestRealShapeTurnsAndPrivacy(t *testing.T) {
	in, cfg, ev := fixture(t)
	turns, pending, err := Read(in, cfg, ev)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("turns=%d pending=%v err=%v", len(turns), pending, err)
	}
	if len(turns[0].LLMCalls) != 1 || len(turns[1].LLMCalls) != 2 || len(turns[1].ToolCalls) != 1 {
		t.Fatalf("split mismatch %+v", turns)
	}
	if len(turns[1].AssistantOutputs) != 1 || turns[1].AssistantOutputs[0].StartUnixNano >= in.ObservedAt {
		t.Fatal("assistant missing or anchored to Stop")
	}
	tool := turns[1].ToolCalls[0]
	if tool.TriggeringLLMCall != turns[1].LLMCalls[0].CallID || tool.ResultStatus != "success" {
		t.Fatalf("tool mismatch %+v", tool)
	}
	b, _ := json.Marshal(turns)
	if strings.Contains(string(b), "excluded") {
		t.Fatal("system/analysis leaked")
	}
	for _, turn := range turns {
		if turn.Usage != (model.Usage{}) {
			t.Fatal("root usage")
		}
		for _, c := range turn.LLMCalls {
			if c.Usage != (model.Usage{}) {
				t.Fatal("invented usage")
			}
		}
	}
	cfg.CaptureContent = "none"
	turns, _, _ = Read(in, cfg, ev)
	if len(turns[0].AssistantOutputs) != 1 {
		t.Fatal("none removed assistant structure")
	}
	b, _ = json.Marshal(turns)
	if strings.Contains(string(b), "PROBE_OK") || strings.Contains(string(b), "TOOL_OK") {
		t.Fatal("none leaked content")
	}
}
func TestIncompleteCancelFailureAndDuplicate(t *testing.T) {
	in, cfg, ev := fixture(t)
	body, _ := os.ReadFile(in.TranscriptPath)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	for _, tc := range []struct {
		name, body, reason string
		count              int
		pending            bool
	}{
		{"incomplete", strings.Join(lines[:6], "\n"), "", 1, true},
		{"cancel", strings.Join(lines[:6], "\n"), "cancelled", 2, false},
		{"failure", strings.Join(lines[:6], "\n"), "failed", 2, false},
		{"partial", string(body) + "{", "", 1, true},
		{"duplicate", string(body) + lines[8] + "\n", "", 2, false},
		{"tool-error", strings.Replace(string(body), "\"content\":\"TOOL_OK\"", "\"content\":\"TOOL_OK\",\"is_error\":true", 1), "", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "session.jsonl")
			os.WriteFile(path, []byte(tc.body), 0600)
			in.TranscriptPath = path
			in.Reason = tc.reason
			turns, pending, err := Read(in, cfg, ev)
			if err != nil || pending != tc.pending || len(turns) != tc.count {
				t.Fatalf("%d %v %v", len(turns), pending, err)
			}
			if tc.reason != "" && turns[1].FinalStatus != model.FinalStatusCancelled {
				t.Fatal("incorrect terminal")
			}
			if tc.name == "tool-error" && turns[1].ToolCalls[0].ErrorType != "tool_error" {
				t.Fatal("lost tool error")
			}
		})
	}
}
func TestStopAssociationAndMeasuredToolTiming(t *testing.T) {
	in, cfg, ev := fixture(t)
	in.ParentRequestSetID = "unrelated"
	turns, pending, err := Read(in, cfg, ev)
	if err != nil || !pending || len(turns) != 1 {
		t.Fatal("wrong Stop finalized turn")
	}
	in.ParentRequestSetID = "turn-2"
	pre := in.ObservedAt - 7e9
	post := pre + 1e8
	ev = append(ev, HookInput{Event: "PreToolUse", ToolUseID: "tool-1", ObservedAt: pre}, HookInput{Event: "PostToolUse", ToolUseID: "tool-1", ObservedAt: post})
	turns, _, _ = Read(in, cfg, ev)
	tool := turns[1].ToolCalls[0]
	if tool.StartUnixNano != pre || tool.EndUnixNano != post {
		t.Fatalf("timing %+v", tool)
	}
}

func TestDurableCancellationRecovery(t *testing.T) {
	in, cfg, ev := fixture(t)
	body, _ := os.ReadFile(in.TranscriptPath)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	cancelled := `{"type":"user","uuid":"cancel-result","timestamp":"2026-10-08T06:21:28Z","toolUseResult":{"kind":"tool-status","status":"cancelled","summary":"User cancelled.","isHardFailure":false},"toolDenialKind":"interrupted","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"Cancelled","is_error":true}]}}`
	marker := `{"type":"user","uuid":"interruption","timestamp":"2026-10-08T06:21:29Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	path := filepath.Join(t.TempDir(), "cancel.jsonl")
	in.TranscriptPath = path
	in.Event = "SessionEnd"
	in.ParentRequestSetID = ""
	os.WriteFile(path, []byte(strings.Join(lines[:6], "\n")+"\n"+cancelled+"\n"+marker+"\n"), 0600)
	turns, pending, err := Read(in, cfg, ev)
	if err != nil || pending || len(turns) != 2 || turns[1].FinalStatus != model.FinalStatusCancelled {
		t.Fatalf("cancel recovery %v %v %+v", err, pending, turns)
	}
	if turns[1].ToolCalls[0].ResultStatus != "cancelled" || turns[1].ToolCalls[0].ErrorType != "cancelled" {
		t.Fatal("cancelled tool lost its explicit status")
	}
	// Loading an old conversation may emit SessionEnd, but must not export
	// pre-registration cancellations or retain their queues indefinitely.
	old, oldPending, oldErr := Read(in, cfg, nil)
	if oldErr != nil || oldPending || len(old) != 0 {
		t.Fatalf("historical cancellation replayed: %v %v %d", oldErr, oldPending, len(old))
	}
	wrong := []HookInput{{Event: "UserPromptSubmit", RequestSetID: "unrelated", ObservedAt: ev[1].ObservedAt}}
	old, oldPending, oldErr = Read(in, cfg, wrong)
	if oldErr != nil || oldPending || len(old) != 0 {
		t.Fatal("unrelated prompt authorized old cancellation")
	}
	// The interruption text alone is not evidence of a cancellation.
	os.WriteFile(path, []byte(strings.Join(lines[:6], "\n")+"\n"+marker+"\n"), 0600)
	turns, pending, err = Read(in, cfg, ev)
	if err != nil || !pending || len(turns) != 1 {
		t.Fatal("text spoofed cancellation")
	}
}

func TestUsageOnlyFromNumericMessageUsage(t *testing.T) {
	in, cfg, ev := fixture(t)
	body, _ := os.ReadFile(in.TranscriptPath)
	body = []byte(strings.ReplaceAll(string(body), `"model":"flash"`, `"model":"flash","usage":{"input_tokens":12,"output_tokens":3}`))
	path := filepath.Join(t.TempDir(), "usage.jsonl")
	os.WriteFile(path, body, 0600)
	in.TranscriptPath = path
	turns, _, err := Read(in, cfg, ev)
	if err != nil {
		t.Fatal(err)
	}
	if turns[0].LLMCalls[0].Usage.InputTokens != 12 || turns[0].LLMCalls[0].Usage.OutputTokens != 3 {
		t.Fatal("usage not deduplicated")
	}
	if turns[0].Usage != (model.Usage{}) {
		t.Fatal("root tokens")
	}
}

func TestDelayedTranscriptTimingAndSessionIsolation(t *testing.T) {
	in, cfg, ev := fixture(t)
	pre := time.Date(2026, 10, 8, 6, 21, 27, 730000000, time.UTC).UnixNano()
	ev = append(ev, HookInput{Event: "PreToolUse", ToolUseID: "tool-1", ObservedAt: pre}, HookInput{Event: "PostToolUse", ToolUseID: "tool-1", ObservedAt: in.ObservedAt + 100})
	turns, _, err := Read(in, cfg, ev)
	if err != nil || len(turns) != 2 {
		t.Fatalf("read: %v", err)
	}
	turn := turns[1]
	if turn.LLMCalls[0].EndUnixNano != pre {
		t.Fatal("inferred LLM duration includes tool execution")
	}
	if turn.EndUnixNano < turn.ToolCalls[0].EndUnixNano {
		t.Fatal("tool extends past root")
	}
	body, _ := os.ReadFile(in.TranscriptPath)
	for _, key := range []string{"sessionId", "session_id"} {
		path := filepath.Join(t.TempDir(), "foreign.jsonl")
		foreign := strings.Replace(string(body), `"uuid":"a6"`, `"uuid":"a6","`+key+`":"another-session"`, 1)
		os.WriteFile(path, []byte(foreign), 0600)
		input := in
		input.TranscriptPath = path
		got, _, err := Read(input, cfg, ev)
		if err == nil || len(got) != 0 {
			t.Fatal("foreign session exported")
		}
	}
}
