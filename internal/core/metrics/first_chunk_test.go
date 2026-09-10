package metrics

import (
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestFirstChunkAndAggregateUsageRemainTraceOnly(t *testing.T) {
	latency := 125.0
	turn := model.Turn{
		SessionID: "s", TurnID: "run", AgentRuntime: "openclaw", FinalStatus: model.FinalStatusCompleted,
		InputPreview: "hello", OutputPreview: "done", StartUnixNano: 1000000000, EndUnixNano: 2000000000,
		AggregateUsageOnly: true, Usage: model.Usage{InputTokens: 16, OutputTokens: 6},
		LLMCalls: []model.LLMCall{{CallID: "call", Provider: "test", RequestModel: "small", FirstChunkMs: &latency, StartUnixNano: 1100000000, EndUnixNano: 1500000000}},
	}
	spans := (semantic.Builder{}).Build(turn)
	if len(spans) != 2 {
		t.Fatalf("expected root and native call, got %#v", spans)
	}
	if spans[0].Attributes["gen_ai.usage.input_tokens"] != int64(16) || spans[0].Attributes["gen_ai.usage.output_tokens"] != int64(6) {
		t.Fatal("aggregate usage must remain on invoke_agent")
	}
	if spans[1].Attributes["gen_ai.response.time_to_first_chunk"] != 0.125 {
		t.Fatal("first-chunk timing must remain on the llm span")
	}
	for _, metric := range Build(spans) {
		if metric.Name == "gen_ai.client.operation.time_to_first_chunk" {
			t.Fatal("first-chunk metric is not part of the default metric set")
		}
		if metric.Name == "gen_ai.client.token.usage" {
			t.Fatal("aggregate invoke_agent usage must not produce client token metrics")
		}
	}
}
