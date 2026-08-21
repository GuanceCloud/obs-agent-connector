package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestReadLatestTurnBuildsKiroToolChain(t *testing.T) {
	sessionDir := t.TempDir()
	sessionID := "session-1"
	end := time.Date(2026, time.August, 21, 10, 0, 5, 0, time.UTC)
	metadata := map[string]any{
		"end_reason":                    "UserTurnEnd",
		"end_timestamp":                 end.Format(time.RFC3339Nano),
		"turn_duration":                 map[string]any{"secs": float64(5), "nanos": float64(0)},
		"input_token_count":             float64(20),
		"output_token_count":            float64(7),
		"cache_read_input_token_count":  float64(3),
		"cache_write_input_token_count": float64(0),
		"message_ids":                   []any{"message-tool", "message-final"},
	}
	sidecar := map[string]any{
		"session_id": sessionID,
		"cwd":        "/workspace",
		"updated_at": end.Format(time.RFC3339Nano),
		"session_state": map[string]any{
			"rts_model_state":       map[string]any{"model_info": map[string]any{"model_id": "claude-sonnet-4"}},
			"conversation_metadata": map[string]any{"user_turn_metadatas": []any{metadata}},
		},
	}
	writeJSON(t, filepath.Join(sessionDir, sessionID+".json"), sidecar)
	lines := []map[string]any{
		{"version": "v1", "kind": "Prompt", "data": map[string]any{
			"content": []any{map[string]any{"kind": "text", "data": "inspect the repository"}},
		}},
		{"version": "v1", "kind": "AssistantMessage", "data": map[string]any{
			"message_id": "message-tool",
			"content": []any{map[string]any{"kind": "toolUse", "data": map[string]any{
				"toolUseId": "tool-1", "name": "shell", "input": map[string]any{"command": "go test ./..."},
			}}},
		}},
		{"version": "v1", "kind": "ToolResults", "data": map[string]any{
			"content": []any{map[string]any{"kind": "toolResult", "data": map[string]any{
				"toolUseId": "tool-1", "content": []any{map[string]any{"kind": "text", "data": "ok"}},
			}}},
		}},
		{"version": "v1", "kind": "AssistantMessage", "data": map[string]any{
			"message_id": "message-final", "content": []any{map[string]any{"kind": "text", "data": "done"}},
		}},
	}
	writeJSONL(t, filepath.Join(sessionDir, sessionID+".jsonl"), lines)
	base := end.Add(-5 * time.Second).UnixNano()
	turn, ok, err := ReadLatestTurn(Options{
		SessionDir: sessionDir, SessionID: sessionID, Cwd: "/workspace", CaptureContent: "preview", MaxChars: 20_000,
		ResourceAttributes: map[string]any{"team": "platform"},
		Events: []JournalEvent{
			{Event: "UserPromptSubmit", RecordedNano: base, Payload: map[string]any{"session_id": sessionID, "prompt": "inspect the repository"}},
			{Event: "PreToolUse", RecordedNano: base + int64(time.Second), Payload: map[string]any{"session_id": sessionID, "tool_name": "shell", "tool_input": map[string]any{"command": "go test ./..."}}},
			{Event: "PostToolUse", RecordedNano: base + 2*int64(time.Second), Payload: map[string]any{"session_id": sessionID, "tool_name": "shell", "tool_response": "ok"}},
			{Event: "Stop", RecordedNano: end.UnixNano(), Payload: map[string]any{"session_id": sessionID, "assistant_response": "done"}},
		},
	})
	if err != nil || !ok {
		t.Fatalf("ReadLatestTurn() ok=%t err=%v", ok, err)
	}
	if turn.SessionID != sessionID || turn.TurnID != "message-tool" || turn.FinalStatus != model.FinalStatusCompleted {
		t.Fatalf("unexpected turn identity: %#v", turn)
	}
	if turn.InputPreview != "inspect the repository" || turn.OutputPreview != "done" {
		t.Fatalf("unexpected content: %#v", turn)
	}
	if turn.Usage.InputTokens != 20 || turn.Usage.OutputTokens != 7 || turn.Usage.CacheReadTokens != 3 {
		t.Fatalf("unexpected usage: %#v", turn.Usage)
	}
	if len(turn.LLMCalls) != 2 || turn.LLMCalls[0].Usage.InputTokens != 0 {
		t.Fatalf("expected per-call usage to remain unset for aggregate multi-call metadata: %#v", turn.LLMCalls)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].CallID != "tool-1" || turn.ToolCalls[0].Command != "go test ./..." {
		t.Fatalf("unexpected tool calls: %#v", turn.ToolCalls)
	}
	if turn.ToolCalls[0].StartUnixNano != base+int64(time.Second) || turn.ToolCalls[0].EndUnixNano != base+2*int64(time.Second) {
		t.Fatalf("Kiro Hook timing was not used: %#v", turn.ToolCalls[0])
	}
	if turn.Resource["team"] != "platform" || turn.Resource["agent_runtime"] != "kiro" {
		t.Fatalf("unexpected resource attributes: %#v", turn.Resource)
	}
}

func TestReadLatestTurnUsesStopAssistantFallback(t *testing.T) {
	sessionDir := t.TempDir()
	sessionID := "session-2"
	end := time.Date(2026, time.August, 21, 11, 0, 0, 0, time.UTC)
	fallbackMetadata := map[string]any{
		"end_reason": "UserTurnEnd", "end_timestamp": end.Format(time.RFC3339Nano), "turn_duration": map[string]any{"secs": float64(1)},
	}
	writeJSON(t, filepath.Join(sessionDir, sessionID+".json"), map[string]any{
		"session_id": sessionID, "cwd": "/workspace", "updated_at": end.Format(time.RFC3339Nano),
		"session_state": map[string]any{"conversation_metadata": map[string]any{"user_turn_metadatas": []any{fallbackMetadata}}},
	})
	writeJSONL(t, filepath.Join(sessionDir, sessionID+".jsonl"), []map[string]any{
		{"kind": "Prompt", "data": map[string]any{"content": []any{map[string]any{"kind": "text", "data": "hello"}}}},
		{"kind": "AssistantMessage", "data": map[string]any{"message_id": "pending", "content": []any{map[string]any{"kind": "thinking", "data": map[string]any{"text": "internal"}}}}},
	})
	turn, ok, err := ReadLatestTurn(Options{SessionDir: sessionDir, SessionID: sessionID, Cwd: "/workspace", AssistantResponse: "world", CaptureContent: "preview", MaxChars: 20_000})
	if err != nil || !ok {
		t.Fatalf("ReadLatestTurn() ok=%t err=%v", ok, err)
	}
	if turn.OutputPreview != "world" || len(turn.AssistantOutputs) != 1 {
		t.Fatalf("Stop fallback was not used: %#v", turn)
	}
}

func TestReadLatestTurnSkipsNonTerminalBlankSession(t *testing.T) {
	sessionDir := t.TempDir()
	sessionID := "session-3"
	writeJSON(t, filepath.Join(sessionDir, sessionID+".json"), map[string]any{"session_id": sessionID, "cwd": "/workspace"})
	writeJSONL(t, filepath.Join(sessionDir, sessionID+".jsonl"), []map[string]any{{"kind": "Prompt", "data": map[string]any{"content": []any{map[string]any{"kind": "text", "data": "hello"}}}}})
	_, ok, err := ReadLatestTurn(Options{SessionDir: sessionDir, SessionID: sessionID, Cwd: "/workspace", CaptureContent: "preview", MaxChars: 20_000})
	if err != nil || ok {
		t.Fatalf("expected blank session to be skipped, ok=%t err=%v", ok, err)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeJSONL(t *testing.T, path string, values []map[string]any) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			t.Fatal(err)
		}
	}
}
