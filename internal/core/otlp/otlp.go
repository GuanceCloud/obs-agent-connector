package otlp

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

var (
	clientDurationBounds         = []float64{0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92}
	agentOperationDurationBounds = []float64{10, 20, 40, 80, 160, 320, 640, 1280, 2560, 5120, 10240, 20480, 40960, 81920}
	workflowDurationBounds       = []float64{1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200}
	tokenBounds                  = []float64{1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864}
)

type ExportTraceServiceRequest struct {
	ResourceSpans []ResourceSpans
}

type ExportMetricsServiceRequest struct {
	ResourceMetrics []ResourceMetrics
}

type ResourceSpans struct {
	Resource   Resource
	ScopeSpans []ScopeSpans
	SchemaURL  string
}

type ScopeSpans struct {
	Scope     InstrumentationScope
	Spans     []Span
	SchemaURL string
}

type Span struct {
	TraceID           []byte
	SpanID            []byte
	TraceState        string
	ParentSpanID      []byte
	Name              string
	Kind              uint32
	StartTimeUnixNano uint64
	EndTimeUnixNano   uint64
	Attributes        []KeyValue
	Status            Status
}

type Status struct {
	Message string
	Code    uint32
}

type ResourceMetrics struct {
	Resource     Resource
	ScopeMetrics []ScopeMetrics
	SchemaURL    string
}

type ScopeMetrics struct {
	Scope     InstrumentationScope
	Metrics   []Metric
	SchemaURL string
}

type Metric struct {
	Name        string
	Description string
	Unit        string
	Sum         *Sum
	Histogram   *Histogram
}

type Sum struct {
	DataPoints             []NumberDataPoint
	AggregationTemporality uint32
	IsMonotonic            bool
}

type Histogram struct {
	DataPoints             []HistogramDataPoint
	AggregationTemporality uint32
}

type NumberDataPoint struct {
	Attributes        []KeyValue
	StartTimeUnixNano uint64
	TimeUnixNano      uint64
	AsInt             *int64
	AsDouble          *float64
	Flags             uint32
}

type HistogramDataPoint struct {
	Attributes        []KeyValue
	StartTimeUnixNano uint64
	TimeUnixNano      uint64
	Count             uint64
	Sum               float64
	BucketCounts      []uint64
	ExplicitBounds    []float64
	Flags             uint32
	Min               float64
	Max               float64
}

type InstrumentationScope struct {
	Name       string
	Version    string
	Attributes []KeyValue
}

type Resource struct {
	Attributes []KeyValue
}

type KeyValue struct {
	Key   string
	Value AnyValue
}

type KeyValueList struct {
	Values []KeyValue
}

type ArrayValue struct {
	Values []AnyValue
}

type AnyValue struct {
	StringValue *string
	BoolValue   *bool
	IntValue    *int64
	DoubleValue *float64
	ArrayValue  *ArrayValue
	KVListValue *KeyValueList
	BytesValue  []byte
}

func SpansToProtoRequest(spans []model.Span) ExportTraceServiceRequest {
	groups := map[string]*ResourceSpans{}
	order := make([]string, 0)
	for _, span := range spans {
		key := resourceKey(span.Resource, span.Scope)
		if groups[key] == nil {
			groups[key] = &ResourceSpans{
				Resource: Resource{Attributes: attributesToOTLP(span.Resource)},
				ScopeSpans: []ScopeSpans{{
					Scope: InstrumentationScope{
						Name:       span.Scope.Name,
						Version:    span.Scope.Version,
						Attributes: attributesToOTLP(span.Scope.Attributes),
					},
					Spans: make([]Span, 0),
				}},
			}
			order = append(order, key)
		}
		groups[key].ScopeSpans[0].Spans = append(groups[key].ScopeSpans[0].Spans, spanToOTLP(span))
	}
	request := ExportTraceServiceRequest{ResourceSpans: make([]ResourceSpans, 0, len(order))}
	for _, key := range order {
		request.ResourceSpans = append(request.ResourceSpans, *groups[key])
	}
	return request
}

func MetricsToProtoRequest(metrics []model.Metric) ExportMetricsServiceRequest {
	type metricGroup struct {
		Metric Metric
	}
	resourceGroups := map[string]*ResourceMetrics{}
	metricGroups := map[string]map[string]*Metric{}
	order := make([]string, 0)
	metricOrder := map[string][]string{}
	for _, metric := range metrics {
		resourceKeyValue := resourceKey(metric.Resource, metric.Scope)
		if resourceGroups[resourceKeyValue] == nil {
			resourceGroups[resourceKeyValue] = &ResourceMetrics{
				Resource: Resource{Attributes: attributesToOTLP(metric.Resource)},
				ScopeMetrics: []ScopeMetrics{{
					Scope: InstrumentationScope{
						Name:       metric.Scope.Name,
						Version:    metric.Scope.Version,
						Attributes: attributesToOTLP(metric.Scope.Attributes),
					},
					Metrics: make([]Metric, 0),
				}},
			}
			metricGroups[resourceKeyValue] = map[string]*Metric{}
			order = append(order, resourceKeyValue)
		}
		key := metricKey(metric)
		if metricGroups[resourceKeyValue][key] == nil {
			converted := metricToOTLP(metric)
			metricGroups[resourceKeyValue][key] = &converted
			metricOrder[resourceKeyValue] = append(metricOrder[resourceKeyValue], key)
		} else {
			existing := metricGroups[resourceKeyValue][key]
			next := metricToOTLP(metric)
			if existing.Sum != nil && next.Sum != nil {
				existing.Sum.DataPoints = append(existing.Sum.DataPoints, next.Sum.DataPoints...)
			}
			if existing.Histogram != nil && next.Histogram != nil {
				existing.Histogram.DataPoints = append(existing.Histogram.DataPoints, next.Histogram.DataPoints...)
			}
		}
	}
	request := ExportMetricsServiceRequest{ResourceMetrics: make([]ResourceMetrics, 0, len(order))}
	for _, key := range order {
		group := resourceGroups[key]
		for _, metricKeyValue := range metricOrder[key] {
			group.ScopeMetrics[0].Metrics = append(group.ScopeMetrics[0].Metrics, *metricGroups[key][metricKeyValue])
		}
		request.ResourceMetrics = append(request.ResourceMetrics, *group)
	}
	return request
}

func spanToOTLP(span model.Span) Span {
	return Span{
		TraceID:           bytesToOTLP(span.TraceID),
		SpanID:            bytesToOTLP(span.SpanID),
		TraceState:        "",
		ParentSpanID:      bytesToOTLP(span.ParentID),
		Name:              span.Name,
		Kind:              spanKind(span.Kind),
		StartTimeUnixNano: parseUint64(span.StartTimeUnixNano),
		EndTimeUnixNano:   parseUint64(span.EndTimeUnixNano),
		Attributes:        attributesToOTLP(span.Attributes),
		Status:            statusToOTLP(span.Status),
	}
}

func metricToOTLP(metric model.Metric) Metric {
	out := Metric{
		Name:        metric.Name,
		Description: metric.Description,
		Unit:        metric.Unit,
	}
	switch metric.Type {
	case "sum":
		out.Sum = &Sum{
			DataPoints:             []NumberDataPoint{numberDataPoint(metric, true)},
			AggregationTemporality: 1,
			IsMonotonic:            true,
		}
	case "histogram":
		out.Histogram = &Histogram{
			DataPoints:             []HistogramDataPoint{histogramDataPoint(metric)},
			AggregationTemporality: 1,
		}
	}
	return out
}

func histogramDataPoint(metric model.Metric) HistogramDataPoint {
	value := metric.Value
	bounds := metricBounds(metric)
	buckets := make([]uint64, len(bounds)+1)
	index := len(bounds)
	for i, bound := range bounds {
		if value <= bound {
			index = i
			break
		}
	}
	buckets[index] = 1
	return HistogramDataPoint{
		Attributes:        attributesToOTLP(metric.Attributes),
		StartTimeUnixNano: parseUint64(metric.StartTimeUnixNano),
		TimeUnixNano:      parseUint64(metric.TimeUnixNano),
		Count:             1,
		Sum:               value,
		BucketCounts:      buckets,
		ExplicitBounds:    bounds,
		Min:               value,
		Max:               value,
	}
}

func numberDataPoint(metric model.Metric, preferDouble bool) NumberDataPoint {
	value := metric.Value
	point := NumberDataPoint{
		Attributes:        attributesToOTLP(metric.Attributes),
		StartTimeUnixNano: parseUint64(metric.StartTimeUnixNano),
		TimeUnixNano:      parseUint64(metric.TimeUnixNano),
	}
	if preferDouble || value != float64(int64(value)) {
		point.AsDouble = &value
	} else {
		intValue := int64(value)
		point.AsInt = &intValue
	}
	return point
}

func metricBounds(metric model.Metric) []float64 {
	switch {
	case metric.Unit == "{token}":
		return tokenBounds
	case metric.Name == "gen_ai.workflow.duration":
		return workflowDurationBounds
	case metric.Name == "gen_ai.agent.operation.duration":
		return agentOperationDurationBounds
	case metric.Unit == "s":
		return clientDurationBounds
	default:
		return []float64{}
	}
}

func statusToOTLP(status model.SpanStatus) Status {
	out := Status{}
	if strings.TrimSpace(status.Message) != "" {
		out.Message = status.Message
	}
	switch strings.ToUpper(status.Code) {
	case "STATUS_CODE_OK":
		out.Code = 1
	case "STATUS_CODE_ERROR":
		out.Code = 2
	default:
		out.Code = 0
	}
	return out
}

func spanKind(kind string) uint32 {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "SPAN_KIND_SERVER":
		return 2
	case "SPAN_KIND_CLIENT":
		return 3
	case "SPAN_KIND_PRODUCER":
		return 4
	case "SPAN_KIND_CONSUMER":
		return 5
	default:
		return 1
	}
}

func attributesToOTLP(attributes map[string]any) []KeyValue {
	if len(attributes) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attributes))
	for key, value := range attributes {
		if value != nil {
			keys = append(keys, key)
		}
	}
	sortStrings(keys)
	out := make([]KeyValue, 0, len(keys))
	for _, key := range keys {
		value := attributes[key]
		if value == nil {
			continue
		}
		out = append(out, KeyValue{Key: key, Value: anyValue(value)})
	}
	return out
}

func anyValue(value any) AnyValue {
	switch current := value.(type) {
	case string:
		return AnyValue{StringValue: &current}
	case bool:
		return AnyValue{BoolValue: &current}
	case int:
		v := int64(current)
		return AnyValue{IntValue: &v}
	case int64:
		v := current
		return AnyValue{IntValue: &v}
	case float64:
		if current == float64(int64(current)) {
			v := int64(current)
			return AnyValue{IntValue: &v}
		}
		v := current
		return AnyValue{DoubleValue: &v}
	case float32:
		v := float64(current)
		return AnyValue{DoubleValue: &v}
	case []byte:
		return AnyValue{BytesValue: current}
	case []any:
		values := make([]AnyValue, 0, len(current))
		for _, entry := range current {
			values = append(values, anyValue(entry))
		}
		return AnyValue{ArrayValue: &ArrayValue{Values: values}}
	case map[string]any:
		return AnyValue{KVListValue: &KeyValueList{Values: attributesToOTLP(current)}}
	default:
		text := stringify(value)
		return AnyValue{StringValue: &text}
	}
}

func bytesToOTLP(value string) []byte {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if decoded, err := hex.DecodeString(value); err == nil {
		return decoded
	}
	return []byte(value)
}

func parseUint64(value string) uint64 {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func resourceKey(resource map[string]any, scope model.Scope) string {
	payload, _ := json.Marshal(map[string]any{
		"resource": resource,
		"scope": map[string]any{
			"name":       scope.Name,
			"version":    scope.Version,
			"attributes": scope.Attributes,
		},
	})
	return string(payload)
}

func metricKey(metric model.Metric) string {
	payload, _ := json.Marshal(map[string]any{
		"name":        metric.Name,
		"type":        metric.Type,
		"unit":        metric.Unit,
		"description": metric.Description,
	})
	return string(payload)
}

func stringify(value any) string {
	switch current := value.(type) {
	case string:
		return current
	default:
		data, err := json.Marshal(current)
		if err == nil {
			return string(data)
		}
		return ""
	}
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}
