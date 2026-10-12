package semantic

import (
	"strconv"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestAssistantCallAssociationKeepsRootParentAndLegacyBehavior(t *testing.T) {
	turn := model.Turn{
		StartUnixNano: 100, EndUnixNano: 1000,
		FinalStatus: model.FinalStatusCompleted,
		LLMCalls: []model.LLMCall{
			{CallID: "first", StartUnixNano: 100, EndUnixNano: 200, Usage: model.Usage{InputTokens: 10, OutputTokens: 2}},
			{CallID: "second", StartUnixNano: 300, EndUnixNano: 400, Usage: model.Usage{InputTokens: 20, OutputTokens: 3}},
		},
		AssistantOutputs: []model.AssistantOutput{
			{TriggeringLLMCall: "first", StartUnixNano: 200, EndUnixNano: 201, OutputPreview: "first"},
			{TriggeringLLMCall: "second", StartUnixNano: 400, EndUnixNano: 401, OutputPreview: "second"},
			{StartUnixNano: 500, EndUnixNano: 501, OutputPreview: "legacy"},
			{TriggeringLLMCall: "missing", StartUnixNano: 600, EndUnixNano: 601, OutputPreview: "unknown"},
		},
	}
	spans := (Builder{}).Build(turn)
	if len(spans) != 7 {
		t.Fatalf("span count = %d, want 7", len(spans))
	}
	for i, output := range spans[3:] {
		if output.Name != "assistant" || output.ParentID != spans[0].SpanID {
			t.Fatalf("assistant %d has incorrect name or parent", i)
		}
		start, _ := strconv.ParseInt(output.StartTimeUnixNano, 10, 64)
		end, _ := strconv.ParseInt(output.EndTimeUnixNano, 10, 64)
		if start >= end {
			t.Fatalf("assistant %d has invalid window", i)
		}
		if i < 2 {
			if output.Attributes["triggered_by.llm_span_id"] != spans[i+1].SpanID {
				t.Fatalf("assistant %d is not linked to its LLM", i)
			}
		} else if _, exists := output.Attributes["triggered_by.llm_span_id"]; exists {
			t.Fatalf("assistant %d invented a call association", i)
		}
		for key := range output.Attributes {
			if strings.HasPrefix(key, "gen_ai.usage.") || key == "gtrace.usage" || key == "gen_ai.operation.name" {
				t.Fatalf("assistant %d has usage/operation attribute %s", i, key)
			}
		}
	}
	if spans[1].EndTimeUnixNano != "200" || spans[2].EndTimeUnixNano != "400" {
		t.Fatal("assistant output extended an LLM duration")
	}
}
