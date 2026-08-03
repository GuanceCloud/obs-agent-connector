package parse

import (
	"os"
	"path/filepath"
	"testing"

	claudeconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/config"
)

func TestReadTranscriptIgnoresIncompleteTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := "{\"type\":\"user\",\"message\":{\"content\":\"hello\"}}\n{\"type\":"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	messages, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
}

func TestNormalizeGroupsToolsDurationsUsageAndSkill(t *testing.T) {
	messages := decodeMessages(t, `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"check weather"}}
{"type":"assistant","timestamp":"2026-06-16T01:00:02Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"Skill","input":{"skill":"weather"}}],"usage":{"input_tokens":10,"output_tokens":3,"cache_read_input_tokens":4,"cache_creation_input_tokens":2}}}
{"type":"user","timestamp":"2026-06-16T01:10:00Z","toolUseResult":{"durationSeconds":15.4},"message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"rain"}]}}
{"type":"assistant","timestamp":"2026-06-16T01:10:01Z","message":{"id":"msg-2","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"It will rain."}],"usage":{"input_tokens":5,"output_tokens":6}}}
{"type":"system","subtype":"turn_duration","durationMs":21840}
`)
	cfg := testConfig()
	turns := Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, cfg, messages)
	if len(turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(turns))
	}
	turn := turns[0]
	if turn.TurnID != "turn-1" || len(turn.LLMCalls) != 2 || len(turn.ToolCalls) != 1 {
		t.Fatalf("unexpected normalized turn: %#v", turn)
	}
	if turn.Usage.InputTokens != 21 || turn.Usage.OutputTokens != 9 ||
		turn.Usage.CacheReadTokens != 4 || turn.Usage.CacheCreateTokens != 2 {
		t.Fatalf("unexpected usage: %#v", turn.Usage)
	}
	tool := turn.ToolCalls[0]
	if tool.EndUnixNano-tool.StartUnixNano != int64(15.4*1e9) {
		t.Fatalf("tool duration = %d", tool.EndUnixNano-tool.StartUnixNano)
	}
	if tool.Skill == nil || tool.Skill.Name != "weather" || tool.Skill.SourceType != "product_tool" {
		t.Fatalf("unexpected skill: %#v", tool.Skill)
	}
	if turn.OutputPreview != "It will rain." {
		t.Fatalf("output preview = %q", turn.OutputPreview)
	}
}

func TestNormalizeMergesAssistantSnapshots(t *testing.T) {
	messages := decodeMessages(t, `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"hello"}}
{"type":"assistant","timestamp":"2026-06-16T01:00:01Z","message":{"id":"msg-1","model":"claude-test","content":[{"type":"text","text":"hello"}]}}
{"type":"assistant","timestamp":"2026-06-16T01:00:02Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"world"}],"usage":{"input_tokens":2,"output_tokens":3}}}
`)
	turns := Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, testConfig(), messages)
	if len(turns) != 1 || len(turns[0].LLMCalls) != 1 {
		t.Fatalf("unexpected turns: %#v", turns)
	}
	if turns[0].Usage.InputTokens != 2 || turns[0].Usage.OutputTokens != 3 {
		t.Fatalf("snapshot usage was not merged: %#v", turns[0].Usage)
	}
	if turns[0].OutputPreview != "hello world" {
		t.Fatalf("merged output = %q", turns[0].OutputPreview)
	}
}

func TestNormalizeSkipsPendingTurnWithUnresolvedTool(t *testing.T) {
	messages := decodeMessages(t, `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"list files"}}
{"type":"assistant","timestamp":"2026-06-16T01:00:01Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"ls"}}]}}
`)
	turns := Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, testConfig(), messages)
	if len(turns) != 0 {
		t.Fatalf("pending turn must not be exported: %#v", turns)
	}
}

func TestNormalizeCaptureNoneOmitsContent(t *testing.T) {
	messages := decodeMessages(t, `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"secret"}}
{"type":"assistant","timestamp":"2026-06-16T01:00:01Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"private"}]}}
`)
	cfg := testConfig()
	cfg.CaptureContent = "none"
	turns := Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, cfg, messages)
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	turn := turns[0]
	if turn.InputMessages != nil || turn.OutputMessages != nil || turn.InputPreview != "" || turn.OutputPreview != "" {
		t.Fatalf("capture none leaked content: %#v", turn)
	}
}

func TestNormalizeFallsBackToToolResultAndExtendsFinalAssistant(t *testing.T) {
	messages := decodeMessages(t, `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-30T04:34:00Z","message":{"content":"validate"}}
{"type":"assistant","timestamp":"2026-06-30T04:34:59Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"check"}}]}}
{"type":"user","timestamp":"2026-06-30T04:35:08Z","message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"denied","is_error":true}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-06-30T04:35:09Z","durationMs":68000}
`)
	turns := Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, testConfig(), messages)
	if len(turns) != 1 {
		t.Fatalf("turns = %d", len(turns))
	}
	if turns[0].OutputPreview != "denied" {
		t.Fatalf("tool result fallback = %q", turns[0].OutputPreview)
	}

	withAssistant := decodeMessages(t, `
{"type":"user","uuid":"turn-2","timestamp":"2026-06-30T06:44:08.020Z","message":{"content":"done?"}}
{"type":"assistant","timestamp":"2026-06-30T06:48:24.704Z","message":{"id":"msg-2","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"partly"}]}}
{"type":"system","subtype":"turn_duration","timestamp":"2026-06-30T06:48:32.438Z","durationMs":199404}
`)
	turns = Normalize(HookPayload{SessionID: "session-1", EventName: "Stop"}, testConfig(), withAssistant)
	if len(turns) != 1 || len(turns[0].AssistantOutputs) != 1 {
		t.Fatalf("unexpected assistant outputs: %#v", turns)
	}
	want := timestamp(withAssistant[2])
	if turns[0].AssistantOutputs[0].EndUnixNano != want {
		t.Fatalf("assistant end = %d, want %d", turns[0].AssistantOutputs[0].EndUnixNano, want)
	}
}

func testConfig() claudeconfig.Config {
	return claudeconfig.Config{
		CaptureContent:     "preview",
		MaxChars:           20_000,
		ResourceAttributes: map[string]any{"service.name": "test"},
	}
}

func decodeMessages(t *testing.T, text string) []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	messages, err := ReadTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	return messages
}
