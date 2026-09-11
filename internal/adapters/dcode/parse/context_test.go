package parse

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMultiCallContextAndPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "context.jsonl")
	var rows []map[string]any
	add := func(role, id string, content any, extra map[string]any) {
		row := map[string]any{"schema_version": 1, "thread_id": "s", "role": role, "record_id": id, "message_id": id, "content": content}
		for k, v := range extra {
			row[k] = v
		}
		rows = append(rows, row)
	}
	add("system", "sys", "visible system instruction", nil)
	add("user", "old-user", "previous question", nil)
	add("assistant", "old-ai", "previous answer", nil)
	add("user", "user", "inspect twice", nil)
	add("assistant", "a1", "", map[string]any{"tool_calls": []any{map[string]any{"id": "t1", "name": "Read", "args": map[string]any{"path": "/one", "api_key": "sensitive-value"}}}, "usage_metadata": map[string]any{"input_tokens": 100, "output_tokens": 10}})
	add("tool", "r1", "first result", map[string]any{"tool_call_id": "t1", "name": "Read"})
	add("assistant", "child", "excluded child", map[string]any{"agent_id": "child"})
	add("assistant", "a2", "checking again", map[string]any{"tool_calls": []any{map[string]any{"id": "t2", "name": "Read", "args": map[string]any{"path": "/two"}}}, "usage_metadata": map[string]any{"input_tokens": 120, "output_tokens": 12}})
	add("tool", "r2", "second result", map[string]any{"tool_call_id": "t2", "name": "Read"})
	add("assistant", "a3", "final answer", map[string]any{"usage_metadata": map[string]any{"input_tokens": 140, "output_tokens": 14}})
	writeTranscript(t, path, rows)
	start := time.Now().UnixNano()
	second := int64(time.Second)
	events := []JournalEvent{{Event: "UserPromptSubmit", RecordedNano: start, Payload: map[string]any{"prompt": "inspect twice"}},
		{Event: "PreToolUse", RecordedNano: start + second, Payload: map[string]any{"tool_use_id": "t1", "tool_name": "Read"}},
		{Event: "PostToolUse", RecordedNano: start + 2*second, Payload: map[string]any{"tool_use_id": "t1", "tool_name": "Read", "tool_response": "first result"}},
		{Event: "PreToolUse", RecordedNano: start + 3*second, Payload: map[string]any{"tool_use_id": "t2", "tool_name": "Read"}},
		{Event: "PostToolUse", RecordedNano: start + 4*second, Payload: map[string]any{"tool_use_id": "t2", "tool_name": "Read", "tool_response": "second result"}},
		{Event: "Stop", RecordedNano: start + 5*second}}
	for _, capture := range []string{"preview", "none"} {
		turn, ok, err := ReadTurn(Options{TranscriptPath: path, SessionID: "s", TurnID: "t", CaptureContent: capture, MaxChars: 20000, Events: events})
		if err != nil || !ok {
			t.Fatalf("read: %v", err)
		}
		if len(turn.LLMCalls) != 3 || len(turn.ToolCalls) != 2 || turn.Usage.InputTokens != 360 || turn.Usage.OutputTokens != 36 {
			t.Fatalf("wrong calls/usage: %#v", turn)
		}
		for i, call := range turn.LLMCalls {
			if call.StartUnixNano >= call.EndUnixNano {
				t.Fatal("non-positive call")
			}
			if i < 2 && call.EndUnixNano > turn.ToolCalls[i].StartUnixNano {
				t.Fatal("call overlaps following tool")
			}
			if i > 0 && call.StartUnixNano < turn.ToolCalls[i-1].EndUnixNano {
				t.Fatal("call precedes tool result")
			}
			if capture == "none" {
				if call.InputMessages != nil || call.OutputMessages != nil || call.InputPreview != "" || call.OutputPreview != "" {
					t.Fatal("content captured while disabled")
				}
				continue
			}
			inputJSON, isJSON := call.ExtraAttributes["dcode.input.messages"].(string)
			input, _ := call.InputMessages.(string)
			if !isJSON || !json.Valid([]byte(inputJSON)) {
				t.Fatal("messages must survive as valid JSON strings")
			}
			var decoded []map[string]any
			if json.Unmarshal([]byte(inputJSON), &decoded) != nil || len(decoded) < 4 {
				t.Fatal("lost message array")
			}
			for _, expected := range []string{"visible system instruction", "previous question", "previous answer", "inspect twice"} {
				if !strings.Contains(input, expected) {
					t.Fatalf("missing %s in call %d", expected, i)
				}
			}
			if strings.Contains(input, "final answer") || strings.Contains(input, "excluded child") || strings.Contains(input, "sensitive-value") {
				t.Fatalf("invalid context: %s", input)
			}
			if strings.Contains(input, "first result") != (i >= 1) || strings.Contains(input, "second result") != (i >= 2) {
				t.Fatalf("future/missing result in %d: %s", i, input)
			}
			if i > 0 && (!strings.Contains(input, "t1") || !strings.Contains(input, "/one") || !strings.Contains(input, "REDACTED")) {
				t.Fatalf("missing call context: %s", input)
			}
			output, _ := json.Marshal(call.OutputMessages)
			if strings.Contains(string(output), "sensitive-value") {
				t.Fatal("secret leaked in output")
			}
			if call.InputPreview == "" || call.OutputPreview == "" {
				t.Fatal("empty preview")
			}
		}
	}
}
