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
			Skill:             &model.SkillUse{Name: "demo", Status: "completed", InputPreview: "skill/demo", OutputPreview: "done"},
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
	if root.Attributes["status"] != "ok" {
		t.Fatalf("invoke_agent status = %#v, want ok", root.Attributes["status"])
	}
	if findSpan(t, spans, "skill:demo").ParentID != ids["tool:exec"] {
		t.Fatal("skill must be a tool child")
	}
	if llm := findSpan(t, spans, "llm"); llm.Attributes["status"] != "info" {
		t.Fatalf("llm status = %#v, want info", llm.Attributes["status"])
	}
	skill := findSpan(t, spans, "skill:demo")
	if skill.Attributes["status"] != "completed" {
		t.Fatalf("skill status = %#v, want explicit completed passthrough", skill.Attributes["status"])
	}
	if skill.Attributes["input_preview"] != "skill/demo" || skill.Attributes["output_preview"] != "done" {
		t.Fatalf("unexpected skill previews: %#v", skill.Attributes)
	}
	tool := findSpan(t, spans, "tool:exec")
	if tool.Attributes["status"] != "info" {
		t.Fatalf("tool status = %#v, want info", tool.Attributes["status"])
	}
	if tool.Attributes["triggered_by.llm_span_id"] != ids["llm"] {
		t.Fatal("tool must reference the triggering llm")
	}
	if assistant := findSpan(t, spans, "assistant"); assistant.Attributes["status"] != "info" {
		t.Fatalf("assistant status = %#v, want info", assistant.Attributes["status"])
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

func TestBuildMarksInvokeAgentErrorWhenTurnCancelled(t *testing.T) {
	now := time.Now().UnixNano()
	spans := (Builder{}).Build(model.Turn{
		SessionID:      "session-cancelled",
		AgentRuntime:   "test-agent",
		AgentName:      "test-agent",
		StartUnixNano:  now,
		EndUnixNano:    now + int64(time.Second),
		FinalStatus:    model.FinalStatusCancelled,
		InputPreview:   "hello",
		OutputPreview:  "cancelled",
		AssistantOutputs: []model.AssistantOutput{{
			StartUnixNano: now,
			EndUnixNano:   now + int64(time.Millisecond),
			OutputPreview: "cancelled",
			Status:        "info",
		}},
	})
	if len(spans) == 0 {
		t.Fatal("expected spans for cancelled turn")
	}
	if spans[0].Attributes["status"] != "error" {
		t.Fatalf("cancelled invoke_agent status = %#v, want error", spans[0].Attributes["status"])
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
