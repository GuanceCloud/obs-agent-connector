package otlp

import (
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestSpansToProtoRequestGroupsByResourceAndScope(t *testing.T) {
	spans := []model.Span{
		{
			TraceID:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SpanID:            "bbbbbbbbbbbbbbbb",
			Name:              "invoke_agent",
			Kind:              "SPAN_KIND_INTERNAL",
			StartTimeUnixNano: "100",
			EndTimeUnixNano:   "200",
			Attributes: map[string]any{
				"gen_ai.conversation.id": "sess-1",
			},
			Resource: map[string]any{
				"service.name": "gtrace-codex",
			},
			Scope: model.Scope{Name: "gtrace-codex-collector", Version: "0.1.0"},
		},
		{
			TraceID:           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			SpanID:            "cccccccccccccccc",
			ParentID:          "bbbbbbbbbbbbbbbb",
			Name:              "llm",
			Kind:              "SPAN_KIND_INTERNAL",
			StartTimeUnixNano: "110",
			EndTimeUnixNano:   "150",
			Attributes: map[string]any{
				"gen_ai.operation.name": "chat",
				"gen_ai.request.model":  "gpt-5.4",
			},
			Resource: map[string]any{
				"service.name": "gtrace-codex",
			},
			Scope: model.Scope{Name: "gtrace-codex-collector", Version: "0.1.0"},
		},
	}

	request := SpansToProtoRequest(spans)
	if len(request.ResourceSpans) != 1 {
		t.Fatalf("expected 1 resourceSpans group, got %d", len(request.ResourceSpans))
	}
	group := request.ResourceSpans[0]
	if len(group.ScopeSpans) != 1 || len(group.ScopeSpans[0].Spans) != 2 {
		t.Fatalf("unexpected span grouping: %#v", group)
	}
	if group.ScopeSpans[0].Spans[0].Name != "invoke_agent" || group.ScopeSpans[0].Spans[1].Name != "llm" {
		t.Fatalf("unexpected span ordering: %#v", group.ScopeSpans[0].Spans)
	}
	if string(group.ScopeSpans[0].Spans[0].TraceID) == "" || string(group.ScopeSpans[0].Spans[0].SpanID) == "" {
		t.Fatalf("expected byte trace/span ids")
	}
}

func TestMetricsToProtoRequestGroupsDataPointsByMetricIdentity(t *testing.T) {
	metrics := []model.Metric{
		{
			Name:              "gen_ai.agent.operation.count",
			Type:              "sum",
			Unit:              "",
			Description:       "Agent-side operation count.",
			Value:             1,
			Attributes:        map[string]any{"gen_ai.operation.name": "chat"},
			Resource:          map[string]any{"service.name": "gtrace-codex"},
			Scope:             model.Scope{Name: "gtrace-codex-collector", Version: "0.1.0"},
			StartTimeUnixNano: "1",
			TimeUnixNano:      "2",
		},
		{
			Name:              "gen_ai.agent.operation.count",
			Type:              "sum",
			Unit:              "",
			Description:       "Agent-side operation count.",
			Value:             1,
			Attributes:        map[string]any{"gen_ai.operation.name": "execute_tool"},
			Resource:          map[string]any{"service.name": "gtrace-codex"},
			Scope:             model.Scope{Name: "gtrace-codex-collector", Version: "0.1.0"},
			StartTimeUnixNano: "1",
			TimeUnixNano:      "3",
		},
	}

	request := MetricsToProtoRequest(metrics)
	if len(request.ResourceMetrics) != 1 {
		t.Fatalf("expected 1 resourceMetrics group, got %d", len(request.ResourceMetrics))
	}
	group := request.ResourceMetrics[0]
	if len(group.ScopeMetrics) != 1 || len(group.ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("unexpected metric grouping: %#v", group)
	}
	sum := group.ScopeMetrics[0].Metrics[0].Sum
	if sum == nil || len(sum.DataPoints) != 2 {
		t.Fatalf("expected 2 datapoints in grouped sum metric, got %#v", sum)
	}
}
