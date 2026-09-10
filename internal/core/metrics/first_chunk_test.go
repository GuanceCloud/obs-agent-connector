package metrics

import (
	"encoding/json"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestFirstChunkMetricFromTerminalSpan(t *testing.T) {
	latency, zero := 125.0, 0.0
	turn := model.Turn{SessionID: "s", TurnID: "run", AgentRuntime: "openclaw", FinalStatus: model.FinalStatusCompleted, InputPreview: "hello", OutputPreview: "done", StartUnixNano: 1000000000, EndUnixNano: 2000000000,
		AggregateUsageOnly: true, Usage: model.Usage{InputTokens: 16, OutputTokens: 6},
		LLMCalls: []model.LLMCall{{CallID: "call", Provider: "test", RequestModel: "small", FirstChunkMs: &latency, StartUnixNano: 1100000000, EndUnixNano: 1500000000}, {CallID: "failed-call", Provider: "test", RequestModel: "other-model", ErrorType: "timeout", FirstChunkMs: &zero, StartUnixNano: 1600000000, EndUnixNano: 1800000000}}}
	// Exercise durable spool serialization too.
	body, err := json.Marshal(turn)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(body, &turn); err != nil {
		t.Fatal(err)
	}
	spans := (semantic.Builder{}).Build(turn)
	if len(spans) != 3 {
		t.Fatal("expected root and two native calls")
	}
	if spans[0].Attributes["gen_ai.response.time_to_first_chunk"] != nil || spans[0].Attributes["model.first_chunks"] != nil {
		t.Fatal("per-call timing must not be on root")
	}
	if spans[1].Attributes["gen_ai.response.time_to_first_chunk"] != 0.125 || spans[2].Attributes["gen_ai.response.time_to_first_chunk"] != float64(0) {
		t.Fatal("standard span timing missing")
	}
	if spans[1].Attributes["ttft"] != nil {
		t.Fatal("first chunk mislabeled as TTFT")
	}
	ms := Build(spans)
	var samples []model.Metric
	for _, m := range ms {
		if m.Name == "gen_ai.client.operation.time_to_first_chunk" {
			samples = append(samples, m)
		}
	}
	if len(samples) != 2 || samples[0].Value != 0.125 || samples[1].Value != 0 || samples[0].Unit != "s" {
		t.Fatalf("unexpected samples: %#v", samples)
	}
	for _, m := range samples {
		for _, key := range []string{"session_id", "run_id", "call_id", "gen_ai.conversation.id", "operation_name", "provider_name", "request_model", "status"} {
			if _, ok := m.Attributes[key]; ok {
				t.Fatalf("high-cardinality tag: %s", key)
			}
		}
	}
	if samples[0].Attributes["gen_ai.operation.name"] != "chat" || samples[0].Attributes["gen_ai.provider.name"] != "test" || samples[0].Attributes["gen_ai.request.model"] != "small" || samples[1].Attributes["error.type"] != "timeout" {
		t.Fatal("standard dimensions missing")
	}
	tokenSamples := 0
	for _, m := range ms {
		if m.Name == "gen_ai.client.token.usage" {
			tokenSamples++
			if m.Attributes["gen_ai.request.model"] != nil {
				t.Fatal("aggregate tokens assigned to model")
			}
		}
	}
	if tokenSamples != 2 {
		t.Fatal("aggregate token metrics duplicated or dropped")
	}
	encoded := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(samples))
	decoded, err := proto.DecodeExportMetricsServiceRequest(encoded)
	if err != nil || len(decoded.ResourceMetrics) == 0 {
		t.Fatalf("invalid protobuf: %v", err)
	}
	for i := range turn.LLMCalls {
		turn.LLMCalls[i].FirstChunkMs = nil
	}
	for _, m := range Build((semantic.Builder{}).Build(turn)) {
		if m.Name == "gen_ai.client.operation.time_to_first_chunk" {
			t.Fatal("missing timing was filled with zero")
		}
	}
}
