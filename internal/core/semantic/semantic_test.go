package semantic

import (
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestBuildProducesCanonicalTreeAndNoAssistantTokens(t *testing.T) {
	start := time.Date(2026, 7, 31, 1, 0, 0, 0, time.UTC).UnixNano()
	turn := model.Turn{
		SessionID:     "session-test",
		TurnID:        "turn-test",
		AgentRuntime:  "test-agent",
		AgentName:     "test-agent",
		StartUnixNano: start,
		EndUnixNano:   start + int64(4*time.Second),
		FinalStatus:   model.FinalStatusCompleted,
		InputPreview:  "hello",
		OutputPreview: "done",
		Usage:         model.Usage{InputTokens: 13, OutputTokens: 5},
		LLMCalls: []model.LLMCall{{
			CallID:        "llm-1",
			StartUnixNano: start,
			EndUnixNano:   start + int64(time.Second),
			RequestModel:  "model-test",
			Usage:         model.Usage{InputTokens: 13, OutputTokens: 5},
		}},
		ToolCalls: []model.ToolCall{{
			CallID:            "tool-1",
			TriggeringLLMCall: "llm-1",
			Name:              "exec",
			StartUnixNano:     start + int64(time.Second),
			EndUnixNano:       start + int64(2*time.Second),
			ResultStatus:      "completed",
			Skill:             &model.SkillUse{Name: "demo", Status: "completed"},
		}},
		AssistantOutputs: []model.AssistantOutput{{
			StartUnixNano: start + int64(3*time.Second),
			EndUnixNano:   start + int64(4*time.Second),
			OutputPreview: "done",
		}},
	}

	spans := (Builder{ScopeVersion: "test"}).Build(turn)
	if len(spans) != 5 {
		t.Fatalf("expected 5 spans, got %d", len(spans))
	}
	root := spans[0]
	ids := map[string]string{}
	for _, span := range spans {
		ids[span.Name] = span.SpanID
		if span.Name != "invoke_agent" && span.Name != "skill:demo" && span.ParentID != root.SpanID {
			t.Fatalf("%s must be a direct root child", span.Name)
		}
		if span.Name == "assistant" {
			if _, ok := span.Attributes["gen_ai.usage.input_tokens"]; ok {
				t.Fatal("assistant must not carry token usage")
			}
		}
	}
	if findSpan(t, spans, "skill:demo").ParentID != ids["tool:exec"] {
		t.Fatal("skill must be a tool child")
	}
	tool := findSpan(t, spans, "tool:exec")
	if tool.Attributes["triggered_by.llm_span_id"] != ids["llm"] {
		t.Fatal("tool must reference the triggering llm")
	}
}

func TestBuildSkipsUnsetAndBlankTurns(t *testing.T) {
	builder := Builder{}
	if spans := builder.Build(model.Turn{FinalStatus: model.FinalStatusUnset}); len(spans) != 0 {
		t.Fatal("unset turn must be skipped")
	}
	now := time.Now().UnixNano()
	if spans := builder.Build(model.Turn{
		StartUnixNano: now,
		EndUnixNano:   now + 1,
		FinalStatus:   model.FinalStatusCompleted,
	}); len(spans) != 0 {
		t.Fatal("blank turn must be skipped")
	}
}

func findSpan(t *testing.T, spans []model.Span, name string) model.Span {
	t.Helper()
	for _, span := range spans {
		if span.Name == name {
			return span
		}
	}
	t.Fatalf("missing span %s", name)
	return model.Span{}
}
