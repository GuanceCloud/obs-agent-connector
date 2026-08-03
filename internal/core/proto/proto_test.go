package proto

import (
	"bytes"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func TestEncodeExportTraceServiceRequestContainsTraceNames(t *testing.T) {
	request := otlp.ExportTraceServiceRequest{
		ResourceSpans: []otlp.ResourceSpans{
			{
				Resource: otlp.Resource{},
				ScopeSpans: []otlp.ScopeSpans{
					{
						Scope: otlp.InstrumentationScope{Name: "gtrace-codex-collector", Version: "0.1.0"},
						Spans: []otlp.Span{
							{
								TraceID:           bytes.Repeat([]byte{0xaa}, 16),
								SpanID:            bytes.Repeat([]byte{0xbb}, 8),
								Name:              "invoke_agent",
								Kind:              1,
								StartTimeUnixNano: 100,
								EndTimeUnixNano:   200,
								Attributes: []otlp.KeyValue{
									{Key: "gen_ai.conversation.id", Value: stringValue("sess-1")},
								},
							},
						},
					},
				},
			},
		},
	}
	payload := EncodeExportTraceServiceRequest(request)
	if len(payload) == 0 {
		t.Fatal("expected non-empty protobuf payload")
	}
	if !bytes.Contains(payload, []byte("invoke_agent")) || !bytes.Contains(payload, []byte("gen_ai.conversation.id")) {
		t.Fatalf("expected encoded payload to contain span name and attribute key: %x", payload)
	}
}

func TestEncodeExportMetricsServiceRequestContainsMetricNames(t *testing.T) {
	request := otlp.ExportMetricsServiceRequest{
		ResourceMetrics: []otlp.ResourceMetrics{
			{
				Resource: otlp.Resource{},
				ScopeMetrics: []otlp.ScopeMetrics{
					{
						Scope: otlp.InstrumentationScope{Name: "gtrace-codex-collector", Version: "0.1.0"},
						Metrics: []otlp.Metric{
							{
								Name:        "gen_ai.agent.operation.count",
								Description: "Agent-side operation count.",
								Unit:        "",
								Sum: &otlp.Sum{
									DataPoints: []otlp.NumberDataPoint{
										{
											StartTimeUnixNano: 1,
											TimeUnixNano:      2,
											AsDouble:          floatPtr(1),
											Attributes: []otlp.KeyValue{
												{Key: "gen_ai.operation.name", Value: stringValue("chat")},
											},
										},
									},
									AggregationTemporality: 1,
									IsMonotonic:            true,
								},
							},
						},
					},
				},
			},
		},
	}
	payload := EncodeExportMetricsServiceRequest(request)
	if len(payload) == 0 {
		t.Fatal("expected non-empty protobuf payload")
	}
	if !bytes.Contains(payload, []byte("gen_ai.agent.operation.count")) || !bytes.Contains(payload, []byte("chat")) {
		t.Fatalf("expected encoded payload to contain metric name and attrs: %x", payload)
	}
}

func TestTraceRequestRoundTripDecode(t *testing.T) {
	request := otlp.ExportTraceServiceRequest{
		ResourceSpans: []otlp.ResourceSpans{
			{
				Resource: otlp.Resource{
					Attributes: []otlp.KeyValue{
						{Key: "service.name", Value: stringValue("gtrace-codex")},
					},
				},
				ScopeSpans: []otlp.ScopeSpans{
					{
						Scope: otlp.InstrumentationScope{Name: "gtrace-codex-collector", Version: "0.1.0"},
						Spans: []otlp.Span{
							{
								TraceID:           bytes.Repeat([]byte{0xaa}, 16),
								SpanID:            bytes.Repeat([]byte{0xbb}, 8),
								ParentSpanID:      bytes.Repeat([]byte{0xcc}, 8),
								Name:              "llm",
								Kind:              1,
								StartTimeUnixNano: 100,
								EndTimeUnixNano:   200,
								Attributes: []otlp.KeyValue{
									{Key: "gen_ai.request.model", Value: stringValue("gpt-5.4")},
								},
								Status: otlp.Status{Code: 2, Message: "failed"},
							},
						},
					},
				},
			},
		},
	}

	payload := EncodeExportTraceServiceRequest(request)
	decoded, err := DecodeExportTraceServiceRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.ResourceSpans) != 1 || len(decoded.ResourceSpans[0].ScopeSpans) != 1 || len(decoded.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	span := decoded.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if span.Name != "llm" || span.Status.Code != 2 || span.Status.Message != "failed" {
		t.Fatalf("unexpected decoded span: %#v", span)
	}
	if string(span.TraceID) != string(bytes.Repeat([]byte{0xaa}, 16)) || string(span.ParentSpanID) != string(bytes.Repeat([]byte{0xcc}, 8)) {
		t.Fatalf("unexpected decoded ids: %#v", span)
	}
}

func TestMetricRequestRoundTripDecode(t *testing.T) {
	request := otlp.ExportMetricsServiceRequest{
		ResourceMetrics: []otlp.ResourceMetrics{
			{
				Resource: otlp.Resource{
					Attributes: []otlp.KeyValue{
						{Key: "service.name", Value: stringValue("gtrace-codex")},
					},
				},
				ScopeMetrics: []otlp.ScopeMetrics{
					{
						Scope: otlp.InstrumentationScope{Name: "gtrace-codex-collector", Version: "0.1.0"},
						Metrics: []otlp.Metric{
							{
								Name: "gen_ai.agent.operation.duration",
								Unit: "ms",
								Histogram: &otlp.Histogram{
									DataPoints: []otlp.HistogramDataPoint{
										{
											StartTimeUnixNano: 1,
											TimeUnixNano:      2,
											Count:             1,
											Sum:               42,
											BucketCounts:      []uint64{0, 1},
											ExplicitBounds:    []float64{10},
											Min:               42,
											Max:               42,
										},
									},
									AggregationTemporality: 1,
								},
							},
						},
					},
				},
			},
		},
	}

	payload := EncodeExportMetricsServiceRequest(request)
	decoded, err := DecodeExportMetricsServiceRequest(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.ResourceMetrics) != 1 || len(decoded.ResourceMetrics[0].ScopeMetrics) != 1 || len(decoded.ResourceMetrics[0].ScopeMetrics[0].Metrics) != 1 {
		t.Fatalf("unexpected decoded shape: %#v", decoded)
	}
	metric := decoded.ResourceMetrics[0].ScopeMetrics[0].Metrics[0]
	if metric.Name != "gen_ai.agent.operation.duration" || metric.Histogram == nil {
		t.Fatalf("unexpected decoded metric: %#v", metric)
	}
	point := metric.Histogram.DataPoints[0]
	if point.Sum != 42 || point.Min != 42 || point.Max != 42 || len(point.BucketCounts) != 2 || len(point.ExplicitBounds) != 1 {
		t.Fatalf("unexpected decoded histogram point: %#v", point)
	}
}

func stringValue(value string) otlp.AnyValue {
	return otlp.AnyValue{StringValue: &value}
}

func floatPtr(value float64) *float64 {
	return &value
}
