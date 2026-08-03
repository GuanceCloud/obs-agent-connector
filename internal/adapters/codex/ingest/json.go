package ingest

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func DecodeJSONTraceRequest(data []byte) (otlp.ExportTraceServiceRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return otlp.ExportTraceServiceRequest{}, err
	}
	resourceSpans := asSlice(payload["resourceSpans"])
	request := otlp.ExportTraceServiceRequest{ResourceSpans: make([]otlp.ResourceSpans, 0, len(resourceSpans))}
	for _, item := range resourceSpans {
		resourceSpan, err := decodeJSONResourceSpans(item)
		if err != nil {
			return request, err
		}
		request.ResourceSpans = append(request.ResourceSpans, resourceSpan)
	}
	return request, nil
}

func DecodeJSONMetricsRequest(data []byte) (otlp.ExportMetricsServiceRequest, error) {
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return otlp.ExportMetricsServiceRequest{}, err
	}
	resourceMetrics := asSlice(payload["resourceMetrics"])
	request := otlp.ExportMetricsServiceRequest{ResourceMetrics: make([]otlp.ResourceMetrics, 0, len(resourceMetrics))}
	for _, item := range resourceMetrics {
		resourceMetric, err := decodeJSONResourceMetrics(item)
		if err != nil {
			return request, err
		}
		request.ResourceMetrics = append(request.ResourceMetrics, resourceMetric)
	}
	return request, nil
}

func decodeJSONResourceSpans(value any) (otlp.ResourceSpans, error) {
	item := asMap(value)
	scopeSpans := asSlice(item["scopeSpans"])
	out := otlp.ResourceSpans{
		Resource:   decodeJSONResource(item["resource"]),
		ScopeSpans: make([]otlp.ScopeSpans, 0, len(scopeSpans)),
		SchemaURL:  asString(item["schemaUrl"]),
	}
	for _, scopeSpan := range scopeSpans {
		decoded, err := decodeJSONScopeSpans(scopeSpan)
		if err != nil {
			return out, err
		}
		out.ScopeSpans = append(out.ScopeSpans, decoded)
	}
	return out, nil
}

func decodeJSONScopeSpans(value any) (otlp.ScopeSpans, error) {
	item := asMap(value)
	spans := asSlice(item["spans"])
	out := otlp.ScopeSpans{
		Scope:     decodeJSONScope(item["scope"]),
		Spans:     make([]otlp.Span, 0, len(spans)),
		SchemaURL: asString(item["schemaUrl"]),
	}
	for _, span := range spans {
		decoded, err := decodeJSONSpan(span)
		if err != nil {
			return out, err
		}
		out.Spans = append(out.Spans, decoded)
	}
	return out, nil
}

func decodeJSONSpan(value any) (otlp.Span, error) {
	item := asMap(value)
	attributes, err := decodeJSONKeyValues(item["attributes"])
	if err != nil {
		return otlp.Span{}, err
	}
	status, err := decodeJSONStatus(item["status"])
	if err != nil {
		return otlp.Span{}, err
	}
	return otlp.Span{
		TraceID:           decodeHexOrPlain(asString(item["traceId"])),
		SpanID:            decodeHexOrPlain(asString(item["spanId"])),
		ParentSpanID:      decodeHexOrPlain(asString(item["parentSpanId"])),
		TraceState:        asString(item["traceState"]),
		Name:              asString(item["name"]),
		Kind:              uint32(asInt64(item["kind"])),
		StartTimeUnixNano: uint64(asInt64(item["startTimeUnixNano"])),
		EndTimeUnixNano:   uint64(asInt64(item["endTimeUnixNano"])),
		Attributes:        attributes,
		Status:            status,
	}, nil
}

func decodeJSONStatus(value any) (otlp.Status, error) {
	if value == nil {
		return otlp.Status{}, nil
	}
	item := asMap(value)
	return otlp.Status{
		Message: asString(item["message"]),
		Code:    uint32(asInt64(item["code"])),
	}, nil
}

func decodeJSONResourceMetrics(value any) (otlp.ResourceMetrics, error) {
	item := asMap(value)
	scopeMetrics := asSlice(item["scopeMetrics"])
	out := otlp.ResourceMetrics{
		Resource:     decodeJSONResource(item["resource"]),
		ScopeMetrics: make([]otlp.ScopeMetrics, 0, len(scopeMetrics)),
		SchemaURL:    asString(item["schemaUrl"]),
	}
	for _, scopeMetric := range scopeMetrics {
		decoded, err := decodeJSONScopeMetrics(scopeMetric)
		if err != nil {
			return out, err
		}
		out.ScopeMetrics = append(out.ScopeMetrics, decoded)
	}
	return out, nil
}

func decodeJSONScopeMetrics(value any) (otlp.ScopeMetrics, error) {
	item := asMap(value)
	metrics := asSlice(item["metrics"])
	out := otlp.ScopeMetrics{
		Scope:     decodeJSONScope(item["scope"]),
		Metrics:   make([]otlp.Metric, 0, len(metrics)),
		SchemaURL: asString(item["schemaUrl"]),
	}
	for _, metric := range metrics {
		decoded, err := decodeJSONMetric(metric)
		if err != nil {
			return out, err
		}
		out.Metrics = append(out.Metrics, decoded)
	}
	return out, nil
}

func decodeJSONMetric(value any) (otlp.Metric, error) {
	item := asMap(value)
	out := otlp.Metric{
		Name:        asString(item["name"]),
		Description: asString(item["description"]),
		Unit:        asString(item["unit"]),
	}
	if sumValue, ok := item["sum"]; ok {
		sum, err := decodeJSONSum(sumValue)
		if err != nil {
			return out, err
		}
		out.Sum = &sum
	}
	if histogramValue, ok := item["histogram"]; ok {
		histogram, err := decodeJSONHistogram(histogramValue)
		if err != nil {
			return out, err
		}
		out.Histogram = &histogram
	}
	return out, nil
}

func decodeJSONSum(value any) (otlp.Sum, error) {
	item := asMap(value)
	points := asSlice(item["dataPoints"])
	out := otlp.Sum{
		DataPoints:             make([]otlp.NumberDataPoint, 0, len(points)),
		AggregationTemporality: uint32(asInt64(item["aggregationTemporality"])),
		IsMonotonic:            asBool(item["isMonotonic"]),
	}
	for _, point := range points {
		decoded, err := decodeJSONNumberDataPoint(point)
		if err != nil {
			return out, err
		}
		out.DataPoints = append(out.DataPoints, decoded)
	}
	return out, nil
}

func decodeJSONHistogram(value any) (otlp.Histogram, error) {
	item := asMap(value)
	points := asSlice(item["dataPoints"])
	out := otlp.Histogram{
		DataPoints:             make([]otlp.HistogramDataPoint, 0, len(points)),
		AggregationTemporality: uint32(asInt64(item["aggregationTemporality"])),
	}
	for _, point := range points {
		decoded, err := decodeJSONHistogramDataPoint(point)
		if err != nil {
			return out, err
		}
		out.DataPoints = append(out.DataPoints, decoded)
	}
	return out, nil
}

func decodeJSONNumberDataPoint(value any) (otlp.NumberDataPoint, error) {
	item := asMap(value)
	attributes, err := decodeJSONKeyValues(item["attributes"])
	if err != nil {
		return otlp.NumberDataPoint{}, err
	}
	point := otlp.NumberDataPoint{
		Attributes:        attributes,
		StartTimeUnixNano: uint64(asInt64(item["startTimeUnixNano"])),
		TimeUnixNano:      uint64(asInt64(item["timeUnixNano"])),
		Flags:             uint32(asInt64(item["flags"])),
	}
	if value, ok := asFloat(item["asDouble"]); ok {
		point.AsDouble = &value
	}
	if value, ok := asInteger(item["asInt"]); ok {
		point.AsInt = &value
	}
	return point, nil
}

func decodeJSONHistogramDataPoint(value any) (otlp.HistogramDataPoint, error) {
	item := asMap(value)
	attributes, err := decodeJSONKeyValues(item["attributes"])
	if err != nil {
		return otlp.HistogramDataPoint{}, err
	}
	return otlp.HistogramDataPoint{
		Attributes:        attributes,
		StartTimeUnixNano: uint64(asInt64(item["startTimeUnixNano"])),
		TimeUnixNano:      uint64(asInt64(item["timeUnixNano"])),
		Count:             uint64(asInt64(item["count"])),
		Sum:               mustFloat(item["sum"]),
		BucketCounts:      asUint64Slice(item["bucketCounts"]),
		ExplicitBounds:    asFloat64Slice(item["explicitBounds"]),
		Flags:             uint32(asInt64(item["flags"])),
		Min:               mustFloat(item["min"]),
		Max:               mustFloat(item["max"]),
	}, nil
}

func decodeJSONResource(value any) otlp.Resource {
	item := asMap(value)
	attributes, _ := decodeJSONKeyValues(item["attributes"])
	return otlp.Resource{Attributes: attributes}
}

func decodeJSONScope(value any) otlp.InstrumentationScope {
	item := asMap(value)
	attributes, _ := decodeJSONKeyValues(item["attributes"])
	return otlp.InstrumentationScope{
		Name:       asString(item["name"]),
		Version:    asString(item["version"]),
		Attributes: attributes,
	}
}

func decodeJSONKeyValues(value any) ([]otlp.KeyValue, error) {
	items := asSlice(value)
	out := make([]otlp.KeyValue, 0, len(items))
	for _, item := range items {
		kv, err := decodeJSONKeyValue(item)
		if err != nil {
			return nil, err
		}
		out = append(out, kv)
	}
	return out, nil
}

func decodeJSONKeyValue(value any) (otlp.KeyValue, error) {
	item := asMap(value)
	anyValue, err := decodeJSONAnyValue(item["value"])
	if err != nil {
		return otlp.KeyValue{}, err
	}
	return otlp.KeyValue{
		Key:   asString(item["key"]),
		Value: anyValue,
	}, nil
}

func decodeJSONAnyValue(value any) (otlp.AnyValue, error) {
	item := asMap(value)
	var out otlp.AnyValue
	if text, ok := item["stringValue"]; ok {
		parsed := asString(text)
		out.StringValue = &parsed
	}
	if boolean, ok := item["boolValue"]; ok {
		parsed := asBool(boolean)
		out.BoolValue = &parsed
	}
	if integer, ok := asInteger(item["intValue"]); ok {
		out.IntValue = &integer
	}
	if number, ok := asFloat(item["doubleValue"]); ok {
		out.DoubleValue = &number
	}
	if bytesValue := asString(item["bytesValue"]); bytesValue != "" {
		out.BytesValue = decodeHexOrPlain(bytesValue)
	}
	if arrayValue, ok := item["arrayValue"]; ok {
		arrayMap := asMap(arrayValue)
		values := asSlice(arrayMap["values"])
		items := make([]otlp.AnyValue, 0, len(values))
		for _, entry := range values {
			decoded, err := decodeJSONAnyValue(entry)
			if err != nil {
				return out, err
			}
			items = append(items, decoded)
		}
		out.ArrayValue = &otlp.ArrayValue{Values: items}
	}
	if kvlistValue, ok := item["kvlistValue"]; ok {
		kvMap := asMap(kvlistValue)
		values, err := decodeJSONKeyValues(kvMap["values"])
		if err != nil {
			return out, err
		}
		out.KVListValue = &otlp.KeyValueList{Values: values}
	}
	return out, nil
}

func asMap(value any) map[string]any {
	item, _ := value.(map[string]any)
	if item == nil {
		return map[string]any{}
	}
	return item
}

func asSlice(value any) []any {
	item, _ := value.([]any)
	if item == nil {
		return []any{}
	}
	return item
}

func asString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case json.Number:
		return current.String()
	default:
		return ""
	}
}

func asBool(value any) bool {
	switch current := value.(type) {
	case bool:
		return current
	default:
		return false
	}
}

func asInt64(value any) int64 {
	switch current := value.(type) {
	case int:
		return int64(current)
	case int64:
		return current
	case float64:
		return int64(current)
	case json.Number:
		parsed, _ := current.Int64()
		return parsed
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
		return parsed
	default:
		return 0
	}
}

func asFloat(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case float32:
		return float64(current), true
	case int:
		return float64(current), true
	case int64:
		return float64(current), true
	case json.Number:
		parsed, err := current.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(current), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func asInteger(value any) (int64, bool) {
	switch current := value.(type) {
	case int:
		return int64(current), true
	case int64:
		return current, true
	case float64:
		return int64(current), true
	case json.Number:
		parsed, err := current.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(current), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func mustFloat(value any) float64 {
	number, _ := asFloat(value)
	return number
}

func asUint64Slice(value any) []uint64 {
	items := asSlice(value)
	out := make([]uint64, 0, len(items))
	for _, item := range items {
		out = append(out, uint64(asInt64(item)))
	}
	return out
}

func asFloat64Slice(value any) []float64 {
	items := asSlice(value)
	out := make([]float64, 0, len(items))
	for _, item := range items {
		number, _ := asFloat(item)
		out = append(out, number)
	}
	return out
}

func decodeHexOrPlain(text string) []byte {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}
	if decoded, err := hex.DecodeString(trimmed); err == nil {
		return decoded
	}
	return []byte(trimmed)
}

func ensureObject(value any, kind string) error {
	if value == nil {
		return nil
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("expected %s object", kind)
	}
	return nil
}
