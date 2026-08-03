package ingest

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

type Scope struct {
	Name       string         `json:"name,omitempty"`
	Version    string         `json:"version,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type SpanStatus struct {
	Message string `json:"message,omitempty"`
	Code    uint32 `json:"code,omitempty"`
}

type StoredSpan struct {
	TraceID           string         `json:"trace_id"`
	SpanID            string         `json:"span_id"`
	ParentID          string         `json:"parent_id,omitempty"`
	Name              string         `json:"name"`
	Kind              uint32         `json:"kind"`
	StartTimeUnixNano string         `json:"start_time_unix_nano"`
	EndTimeUnixNano   string         `json:"end_time_unix_nano"`
	StartTime         string         `json:"start_time,omitempty"`
	EndTime           string         `json:"end_time,omitempty"`
	DurationMs        int64          `json:"duration_ms"`
	Status            SpanStatus     `json:"status,omitempty"`
	TraceState        string         `json:"trace_state,omitempty"`
	Attributes        map[string]any `json:"attributes,omitempty"`
	Resource          map[string]any `json:"resource,omitempty"`
	Scope             Scope          `json:"scope,omitempty"`
	GTrace            GTrace         `json:"gtrace,omitempty"`
	Ingest            map[string]any `json:"ingest,omitempty"`
}

type StoredMetric struct {
	Name                   string         `json:"name"`
	Description            string         `json:"description,omitempty"`
	Unit                   string         `json:"unit,omitempty"`
	Type                   string         `json:"type"`
	Value                  any            `json:"value,omitempty"`
	AggregationTemporality uint32         `json:"aggregation_temporality,omitempty"`
	IsMonotonic            bool           `json:"is_monotonic,omitempty"`
	Count                  uint64         `json:"count,omitempty"`
	Sum                    float64        `json:"sum,omitempty"`
	Min                    float64        `json:"min,omitempty"`
	Max                    float64        `json:"max,omitempty"`
	BucketCounts           []uint64       `json:"bucket_counts,omitempty"`
	ExplicitBounds         []float64      `json:"explicit_bounds,omitempty"`
	StartTimeUnixNano      string         `json:"start_time_unix_nano,omitempty"`
	TimeUnixNano           string         `json:"time_unix_nano,omitempty"`
	Attributes             map[string]any `json:"attributes,omitempty"`
	Resource               map[string]any `json:"resource,omitempty"`
	Scope                  Scope          `json:"scope,omitempty"`
	Ingest                 map[string]any `json:"ingest,omitempty"`
}

type GTrace struct {
	Trace       GTraceTrace       `json:"trace,omitempty"`
	Observation GTraceObservation `json:"observation,omitempty"`
	Environment any               `json:"environment,omitempty"`
}

type GTraceTrace struct {
	Name      any            `json:"name,omitempty"`
	SessionID any            `json:"session_id,omitempty"`
	UserID    any            `json:"user_id,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type GTraceObservation struct {
	Type      any            `json:"type,omitempty"`
	Input     any            `json:"input,omitempty"`
	Output    any            `json:"output,omitempty"`
	ModelName any            `json:"model_name,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
}

func NormalizeExportTraceRequest(request otlp.ExportTraceServiceRequest, ingest map[string]any) []StoredSpan {
	records := make([]StoredSpan, 0)
	for _, resourceSpan := range request.ResourceSpans {
		resourceAttributes := attributesToObject(resourceSpan.Resource.Attributes)
		for _, scopeSpan := range resourceSpan.ScopeSpans {
			scope := Scope{
				Name:       scopeSpan.Scope.Name,
				Version:    scopeSpan.Scope.Version,
				Attributes: attributesToObject(scopeSpan.Scope.Attributes),
			}
			for _, span := range scopeSpan.Spans {
				records = append(records, normalizeSpan(span, resourceAttributes, scope, ingest))
			}
		}
	}
	return records
}

func NormalizeExportMetricsRequest(request otlp.ExportMetricsServiceRequest, ingest map[string]any) []StoredMetric {
	records := make([]StoredMetric, 0)
	for _, resourceMetric := range request.ResourceMetrics {
		resourceAttributes := attributesToObject(resourceMetric.Resource.Attributes)
		for _, scopeMetric := range resourceMetric.ScopeMetrics {
			scope := Scope{
				Name:       scopeMetric.Scope.Name,
				Version:    scopeMetric.Scope.Version,
				Attributes: attributesToObject(scopeMetric.Scope.Attributes),
			}
			for _, metric := range scopeMetric.Metrics {
				records = append(records, normalizeMetric(metric, resourceAttributes, scope, ingest)...)
			}
		}
	}
	return records
}

func normalizeSpan(span otlp.Span, resourceAttributes map[string]any, scope Scope, ingest map[string]any) StoredSpan {
	attributes := attributesToObject(span.Attributes)
	startNs := uint64ToString(span.StartTimeUnixNano)
	endNs := uint64ToString(span.EndTimeUnixNano)
	return StoredSpan{
		TraceID:           bytesToHex(span.TraceID),
		SpanID:            bytesToHex(span.SpanID),
		ParentID:          bytesToHex(span.ParentSpanID),
		Name:              span.Name,
		Kind:              span.Kind,
		StartTimeUnixNano: startNs,
		EndTimeUnixNano:   endNs,
		StartTime:         nsToISO(span.StartTimeUnixNano),
		EndTime:           nsToISO(span.EndTimeUnixNano),
		DurationMs:        durationMs(span.StartTimeUnixNano, span.EndTimeUnixNano),
		Status:            SpanStatus{Message: span.Status.Message, Code: span.Status.Code},
		TraceState:        span.TraceState,
		Attributes:        attributes,
		Resource:          resourceAttributes,
		Scope:             scope,
		GTrace:            extractGTrace(attributes, span.Name),
		Ingest:            cloneMap(ingest),
	}
}

func normalizeMetric(metric otlp.Metric, resourceAttributes map[string]any, scope Scope, ingest map[string]any) []StoredMetric {
	out := make([]StoredMetric, 0)
	if metric.Sum != nil {
		for _, point := range metric.Sum.DataPoints {
			record := StoredMetric{
				Name:                   metric.Name,
				Description:            metric.Description,
				Unit:                   metric.Unit,
				Type:                   "sum",
				Value:                  numberPointValue(point),
				AggregationTemporality: metric.Sum.AggregationTemporality,
				IsMonotonic:            metric.Sum.IsMonotonic,
				StartTimeUnixNano:      uint64ToString(point.StartTimeUnixNano),
				TimeUnixNano:           uint64ToString(point.TimeUnixNano),
				Attributes:             attributesToObject(point.Attributes),
				Resource:               resourceAttributes,
				Scope:                  scope,
				Ingest:                 cloneMap(ingest),
			}
			out = append(out, record)
		}
	}
	if metric.Histogram != nil {
		for _, point := range metric.Histogram.DataPoints {
			record := StoredMetric{
				Name:                   metric.Name,
				Description:            metric.Description,
				Unit:                   metric.Unit,
				Type:                   "histogram",
				AggregationTemporality: metric.Histogram.AggregationTemporality,
				Count:                  point.Count,
				Sum:                    point.Sum,
				Min:                    point.Min,
				Max:                    point.Max,
				BucketCounts:           append([]uint64(nil), point.BucketCounts...),
				ExplicitBounds:         append([]float64(nil), point.ExplicitBounds...),
				StartTimeUnixNano:      uint64ToString(point.StartTimeUnixNano),
				TimeUnixNano:           uint64ToString(point.TimeUnixNano),
				Attributes:             attributesToObject(point.Attributes),
				Resource:               resourceAttributes,
				Scope:                  scope,
				Ingest:                 cloneMap(ingest),
			}
			out = append(out, record)
		}
	}
	return out
}

func traceRequestToMap(request otlp.ExportTraceServiceRequest) map[string]any {
	resourceSpans := make([]any, 0, len(request.ResourceSpans))
	for _, resourceSpan := range request.ResourceSpans {
		resourceSpans = append(resourceSpans, resourceSpansToMap(resourceSpan))
	}
	return map[string]any{"resourceSpans": resourceSpans}
}

func resourceSpansToMap(value otlp.ResourceSpans) map[string]any {
	scopeSpans := make([]any, 0, len(value.ScopeSpans))
	for _, scopeSpan := range value.ScopeSpans {
		scopeSpans = append(scopeSpans, scopeSpansToMap(scopeSpan))
	}
	out := map[string]any{
		"resource":   resourceToMap(value.Resource),
		"scopeSpans": scopeSpans,
	}
	if value.SchemaURL != "" {
		out["schemaUrl"] = value.SchemaURL
	}
	return out
}

func scopeSpansToMap(value otlp.ScopeSpans) map[string]any {
	spans := make([]any, 0, len(value.Spans))
	for _, span := range value.Spans {
		spans = append(spans, spanToMap(span))
	}
	out := map[string]any{
		"scope": scopeToMap(value.Scope),
		"spans": spans,
	}
	if value.SchemaURL != "" {
		out["schemaUrl"] = value.SchemaURL
	}
	return out
}

func spanToMap(value otlp.Span) map[string]any {
	out := map[string]any{
		"traceId":           bytesToHex(value.TraceID),
		"spanId":            bytesToHex(value.SpanID),
		"parentSpanId":      bytesToHex(value.ParentSpanID),
		"name":              value.Name,
		"kind":              value.Kind,
		"startTimeUnixNano": uint64ToString(value.StartTimeUnixNano),
		"endTimeUnixNano":   uint64ToString(value.EndTimeUnixNano),
		"attributes":        keyValuesToList(value.Attributes),
	}
	if value.TraceState != "" {
		out["traceState"] = value.TraceState
	}
	if value.Status.Code != 0 || value.Status.Message != "" {
		out["status"] = map[string]any{
			"code":    value.Status.Code,
			"message": value.Status.Message,
		}
	}
	return out
}

func metricRequestToMap(request otlp.ExportMetricsServiceRequest) map[string]any {
	resourceMetrics := make([]any, 0, len(request.ResourceMetrics))
	for _, resourceMetric := range request.ResourceMetrics {
		resourceMetrics = append(resourceMetrics, resourceMetricsToMap(resourceMetric))
	}
	return map[string]any{"resourceMetrics": resourceMetrics}
}

func resourceMetricsToMap(value otlp.ResourceMetrics) map[string]any {
	scopeMetrics := make([]any, 0, len(value.ScopeMetrics))
	for _, scopeMetric := range value.ScopeMetrics {
		scopeMetrics = append(scopeMetrics, scopeMetricsToMap(scopeMetric))
	}
	out := map[string]any{
		"resource":     resourceToMap(value.Resource),
		"scopeMetrics": scopeMetrics,
	}
	if value.SchemaURL != "" {
		out["schemaUrl"] = value.SchemaURL
	}
	return out
}

func scopeMetricsToMap(value otlp.ScopeMetrics) map[string]any {
	metrics := make([]any, 0, len(value.Metrics))
	for _, metric := range value.Metrics {
		metrics = append(metrics, metricToMap(metric))
	}
	out := map[string]any{
		"scope":   scopeToMap(value.Scope),
		"metrics": metrics,
	}
	if value.SchemaURL != "" {
		out["schemaUrl"] = value.SchemaURL
	}
	return out
}

func metricToMap(value otlp.Metric) map[string]any {
	out := map[string]any{
		"name":        value.Name,
		"description": value.Description,
		"unit":        value.Unit,
	}
	if value.Sum != nil {
		points := make([]any, 0, len(value.Sum.DataPoints))
		for _, point := range value.Sum.DataPoints {
			points = append(points, numberDataPointToMap(point))
		}
		out["sum"] = map[string]any{
			"dataPoints":             points,
			"aggregationTemporality": value.Sum.AggregationTemporality,
			"isMonotonic":            value.Sum.IsMonotonic,
		}
	}
	if value.Histogram != nil {
		points := make([]any, 0, len(value.Histogram.DataPoints))
		for _, point := range value.Histogram.DataPoints {
			points = append(points, histogramDataPointToMap(point))
		}
		out["histogram"] = map[string]any{
			"dataPoints":             points,
			"aggregationTemporality": value.Histogram.AggregationTemporality,
		}
	}
	return out
}

func numberDataPointToMap(value otlp.NumberDataPoint) map[string]any {
	out := map[string]any{
		"attributes":        keyValuesToList(value.Attributes),
		"startTimeUnixNano": uint64ToString(value.StartTimeUnixNano),
		"timeUnixNano":      uint64ToString(value.TimeUnixNano),
		"flags":             value.Flags,
	}
	if value.AsInt != nil {
		out["asInt"] = *value.AsInt
	}
	if value.AsDouble != nil {
		out["asDouble"] = *value.AsDouble
	}
	return out
}

func histogramDataPointToMap(value otlp.HistogramDataPoint) map[string]any {
	return map[string]any{
		"attributes":        keyValuesToList(value.Attributes),
		"startTimeUnixNano": uint64ToString(value.StartTimeUnixNano),
		"timeUnixNano":      uint64ToString(value.TimeUnixNano),
		"count":             value.Count,
		"sum":               value.Sum,
		"bucketCounts":      append([]uint64(nil), value.BucketCounts...),
		"explicitBounds":    append([]float64(nil), value.ExplicitBounds...),
		"flags":             value.Flags,
		"min":               value.Min,
		"max":               value.Max,
	}
}

func resourceToMap(value otlp.Resource) map[string]any {
	return map[string]any{"attributes": keyValuesToList(value.Attributes)}
}

func scopeToMap(value otlp.InstrumentationScope) map[string]any {
	out := map[string]any{
		"name":       value.Name,
		"version":    value.Version,
		"attributes": keyValuesToList(value.Attributes),
	}
	return out
}

func keyValuesToList(values []otlp.KeyValue) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, map[string]any{
			"key":   value.Key,
			"value": anyValueToMap(value.Value),
		})
	}
	return out
}

func anyValueToMap(value otlp.AnyValue) map[string]any {
	out := map[string]any{}
	if value.StringValue != nil {
		out["stringValue"] = *value.StringValue
	}
	if value.BoolValue != nil {
		out["boolValue"] = *value.BoolValue
	}
	if value.IntValue != nil {
		out["intValue"] = *value.IntValue
	}
	if value.DoubleValue != nil {
		out["doubleValue"] = *value.DoubleValue
	}
	if value.BytesValue != nil {
		out["bytesValue"] = bytesToHex(value.BytesValue)
	}
	if value.ArrayValue != nil {
		items := make([]any, 0, len(value.ArrayValue.Values))
		for _, entry := range value.ArrayValue.Values {
			items = append(items, anyValueToMap(entry))
		}
		out["arrayValue"] = map[string]any{"values": items}
	}
	if value.KVListValue != nil {
		out["kvlistValue"] = map[string]any{"values": keyValuesToList(value.KVListValue.Values)}
	}
	return out
}

func attributesToObject(attributes []otlp.KeyValue) map[string]any {
	out := map[string]any{}
	for _, item := range attributes {
		if item.Key == "" {
			continue
		}
		out[item.Key] = anyValueToJSON(item.Value)
	}
	return out
}

func anyValueToJSON(value otlp.AnyValue) any {
	switch {
	case value.StringValue != nil:
		return *value.StringValue
	case value.BoolValue != nil:
		return *value.BoolValue
	case value.IntValue != nil:
		return *value.IntValue
	case value.DoubleValue != nil:
		return *value.DoubleValue
	case len(value.BytesValue) > 0:
		return bytesToHex(value.BytesValue)
	case value.ArrayValue != nil:
		items := make([]any, 0, len(value.ArrayValue.Values))
		for _, entry := range value.ArrayValue.Values {
			items = append(items, anyValueToJSON(entry))
		}
		return items
	case value.KVListValue != nil:
		return attributesToObject(value.KVListValue.Values)
	default:
		return nil
	}
}

func extractGTrace(attributes map[string]any, spanName string) GTrace {
	return GTrace{
		Trace: GTraceTrace{
			Name:      firstNonNil(attributes["gtrace.trace.name"], attributes["trace_name"]),
			SessionID: firstNonNil(attributes["gtrace.session.id"], attributes["gen_ai.conversation.id"], attributes["session_id"]),
			UserID:    firstNonNil(attributes["gtrace.user.id"], attributes["user_id"]),
			Metadata:  collectPrefixed(attributes, "gtrace.trace.metadata."),
		},
		Observation: GTraceObservation{
			Type:      firstNonNil(attributes["gtrace.observation.type"], attributes["span_type"], observationTypeFromSpanName(spanName)),
			Input:     parseMaybeJSON(firstNonNil(attributes["gtrace.observation.input"], attributes["input_preview"])),
			Output:    parseMaybeJSON(firstNonNil(attributes["gtrace.observation.output"], attributes["output_preview"])),
			ModelName: firstNonNil(attributes["gtrace.model.name"], attributes["gen_ai.response.model"], attributes["gen_ai.request.model"], attributes["model_name"], attributes["request_model"]),
			Usage:     usageFromCanonicalAttributes(attributes),
		},
		Environment: attributes["gtrace.environment"],
	}
}

func observationTypeFromSpanName(spanName string) any {
	switch {
	case spanName == "invoke_agent":
		return "agent"
	case spanName == "llm":
		return "llm"
	case spanName == "assistant":
		return "assistant"
	case strings.HasPrefix(spanName, "skill:"):
		return "skill"
	case strings.HasPrefix(spanName, "tool:"):
		return "tool"
	default:
		return nil
	}
}

func usageFromCanonicalAttributes(attributes map[string]any) map[string]any {
	usage := map[string]any{}
	inputTokens := firstNonNil(attributes["gen_ai.usage.input_tokens"], attributes["usage_input_tokens"])
	outputTokens := firstNonNil(attributes["gen_ai.usage.output_tokens"], attributes["usage_output_tokens"])
	if number, ok := asNumber(inputTokens); ok {
		usage["input"] = number
	}
	if number, ok := asNumber(outputTokens); ok {
		usage["output"] = number
	}
	if number, ok := asNumber(attributes["usage_total_tokens"]); ok {
		usage["total"] = number
	} else {
		input, hasInput := asNumber(usage["input"])
		output, hasOutput := asNumber(usage["output"])
		if hasInput || hasOutput {
			usage["total"] = input + output
		}
	}
	cacheReadInputTokens := firstNonNil(attributes["gen_ai.usage.cache_read.input_tokens"], attributes["usage_cache_read_input_tokens"])
	if number, ok := asNumber(cacheReadInputTokens); ok {
		usage["cache_read_input_tokens"] = number
	}
	if number, ok := asNumber(attributes["usage_cache_total_tokens"]); ok {
		usage["cache_total_tokens"] = number
	}
	if number, ok := asNumber(attributes["usage_context_input_tokens"]); ok {
		usage["context_input_tokens"] = number
	}
	if number, ok := asNumber(attributes["usage_context_total_tokens"]); ok {
		usage["context_total_tokens"] = number
	}
	reasoningTokens := firstNonNil(attributes["gen_ai.usage.reasoning.output_tokens"], attributes["usage_reasoning_tokens"])
	if number, ok := asNumber(reasoningTokens); ok {
		usage["reasoning_tokens"] = number
	}
	if len(usage) == 0 {
		return nil
	}
	return usage
}

func collectPrefixed(attributes map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for key, value := range attributes {
		if strings.HasPrefix(key, prefix) {
			out[strings.TrimPrefix(key, prefix)] = parseMaybeJSON(value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseMaybeJSON(value any) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}
	var parsed any
	if json.Unmarshal([]byte(trimmed), &parsed) == nil {
		return parsed
	}
	return text
}

func cloneMap(value map[string]any) map[string]any {
	if len(value) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		return value
	}
	return nil
}

func asNumber(value any) (float64, bool) {
	switch current := value.(type) {
	case int:
		return float64(current), true
	case int64:
		return float64(current), true
	case uint64:
		return float64(current), true
	case float64:
		return current, true
	case float32:
		return float64(current), true
	default:
		return 0, false
	}
}

func numberPointValue(point otlp.NumberDataPoint) any {
	if point.AsInt != nil {
		return *point.AsInt
	}
	if point.AsDouble != nil {
		return *point.AsDouble
	}
	return nil
}

func bytesToHex(value []byte) string {
	if len(value) == 0 {
		return ""
	}
	return hex.EncodeToString(value)
}

func durationMs(startNs, endNs uint64) int64 {
	if endNs <= startNs {
		return 0
	}
	return int64((endNs - startNs) / 1_000_000)
}

func nsToISO(value uint64) string {
	if value == 0 {
		return ""
	}
	seconds := int64(value / 1_000_000_000)
	nanos := int64(value % 1_000_000_000)
	return time.Unix(seconds, nanos).UTC().Format(time.RFC3339Nano)
}

func uint64ToString(value uint64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(value, 10)
}
