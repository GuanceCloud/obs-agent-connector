package parse

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizedMessageStructures(t *testing.T) {
	cfg := testConfig(t)
	s := fixtureSnapshot(t)
	context := Event{Type: "context", Messages: []Message{
		{Role: "user", Content: "Read the skill"},
		{Role: "assistant", Content: []any{map[string]any{"type": "toolCall", "id": "call-1", "name": "read", "arguments": map[string]any{"path": "/synthetic/SKILL.md"}}}},
		{Role: "toolResult", ToolCallID: "call-1", Content: []any{map[string]any{"type": "text", "text": "A synthetic skill"}}},
	}}
	last := s.Events[len(s.Events)-1]
	s.Events = append(s.Events[:len(s.Events)-1], context, last)
	turn, err := Normalize(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	values := []any{turn.InputMessages, turn.OutputMessages, turn.LLMCalls[0].InputMessages, turn.LLMCalls[0].OutputMessages, turn.LLMCalls[1].InputMessages, turn.LLMCalls[1].OutputMessages, turn.AssistantOutputs[0].OutputMessages}
	for _, value := range values {
		list, ok := value.([]any)
		if !ok || len(list) == 0 {
			t.Fatalf("expected message array, got %#v", value)
		}
		for _, item := range list {
			msg, ok := item.(map[string]any)
			if !ok || msg["role"] == nil {
				t.Fatalf("missing message role: %#v", item)
			}
			parts, ok := msg["parts"].([]any)
			if !ok || len(parts) == 0 {
				t.Fatalf("missing message parts: %#v", msg)
			}
			if _, old := msg["content"]; old {
				t.Fatal("native OMP message content leaked")
			}
		}
	}
	output := turn.LLMCalls[0].OutputMessages.([]any)[0].(map[string]any)
	part := output["parts"].([]any)[0].(map[string]any)
	if part["type"] != "tool_call" || part["id"] != "call-1" || part["name"] != "read" || output["finish_reason"] != "tool_call" {
		t.Fatalf("bad tool request: %#v", output)
	}
	input := turn.LLMCalls[1].InputMessages.([]any)[2].(map[string]any)
	result := input["parts"].([]any)[0].(map[string]any)
	if input["role"] != "tool" || result["type"] != "tool_call_response" || result["id"] != "call-1" || result["response"] != "A synthetic skill" {
		t.Fatalf("bad tool response: %#v", input)
	}
	body, _ := json.Marshal(values)
	if strings.Contains(string(body), "private-value") {
		t.Fatal("messages leaked secret")
	}
}

func TestMessagePrivacyDoesNotDamageSchema(t *testing.T) {
	cfg := testConfig(t)
	cfg.MaxChars = 1
	msg := Message{Role: "assistant", StopReason: "aborted", Content: []any{map[string]any{"type": "thinking", "thinking": "A thought"}, map[string]any{"type": "text", "text": "Hello"}}}
	value := messageValue(msg, true, cfg)
	if value["role"] != "assistant" || value["finish_reason"] != "cancelled" {
		t.Fatal("clipped structural values")
	}
	parts := value["parts"].([]any)
	if parts[0].(map[string]any)["type"] != "reasoning" || parts[1].(map[string]any)["type"] != "text" {
		t.Fatal("invalid part types")
	}
	if parts[1].(map[string]any)["content"] != "H" {
		t.Fatal("content not clipped")
	}
	cfg.CaptureContent = "none"
	if messageList(msg, true, cfg) != nil {
		t.Fatal("none captured output")
	}
	if messageList(Message{Role: "user", Content: "Hello"}, false, cfg) != nil {
		t.Fatal("none captured input")
	}
}
