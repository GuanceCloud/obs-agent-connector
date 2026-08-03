package proto

import (
	"encoding/binary"
	"math"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

const (
	wireVarint          = 0
	wireFixed64         = 1
	wireLengthDelimited = 2
)

func EncodeExportTraceServiceRequest(request otlp.ExportTraceServiceRequest) []byte {
	w := &writer{}
	for _, resourceSpan := range request.ResourceSpans {
		item := resourceSpan
		w.message(1, func(w *writer) { encodeResourceSpans(w, item) })
	}
	return w.bytes()
}

func EncodeExportMetricsServiceRequest(request otlp.ExportMetricsServiceRequest) []byte {
	w := &writer{}
	for _, resourceMetric := range request.ResourceMetrics {
		item := resourceMetric
		w.message(1, func(w *writer) { encodeResourceMetrics(w, item) })
	}
	return w.bytes()
}

type writer struct {
	buf []byte
}

func (w *writer) bytes() []byte {
	return w.buf
}

func (w *writer) tag(field int, wire int) {
	w.varint(uint64(field<<3 | wire))
}

func (w *writer) varint(value uint64) {
	for value >= 0x80 {
		w.buf = append(w.buf, byte(value)|0x80)
		value >>= 7
	}
	w.buf = append(w.buf, byte(value))
}

func (w *writer) bool(field int, value bool) {
	if !value {
		return
	}
	w.tag(field, wireVarint)
	if value {
		w.varint(1)
	} else {
		w.varint(0)
	}
}

func (w *writer) uint32(field int, value uint32) {
	if value == 0 {
		return
	}
	w.tag(field, wireVarint)
	w.varint(uint64(value))
}

func (w *writer) uint64(field int, value uint64) {
	if value == 0 {
		return
	}
	w.tag(field, wireVarint)
	w.varint(value)
}

func (w *writer) fixed64(field int, value uint64) {
	if value == 0 {
		return
	}
	w.tag(field, wireFixed64)
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], value)
	w.buf = append(w.buf, buf[:]...)
}

func (w *writer) double(field int, value float64) {
	if value == 0 {
		return
	}
	w.fixed64(field, math.Float64bits(value))
}

func (w *writer) string(field int, value string) {
	if value == "" {
		return
	}
	w.tag(field, wireLengthDelimited)
	w.varint(uint64(len(value)))
	w.buf = append(w.buf, value...)
}

func (w *writer) rawBytes(field int, value []byte) {
	if len(value) == 0 {
		return
	}
	w.tag(field, wireLengthDelimited)
	w.varint(uint64(len(value)))
	w.buf = append(w.buf, value...)
}

func (w *writer) message(field int, fn func(*writer)) {
	child := &writer{}
	fn(child)
	if len(child.buf) == 0 {
		return
	}
	w.tag(field, wireLengthDelimited)
	w.varint(uint64(len(child.buf)))
	w.buf = append(w.buf, child.buf...)
}

func (w *writer) packedFixed64(field int, values []uint64) {
	if len(values) == 0 {
		return
	}
	child := &writer{}
	for _, value := range values {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], value)
		child.buf = append(child.buf, buf[:]...)
	}
	w.tag(field, wireLengthDelimited)
	w.varint(uint64(len(child.buf)))
	w.buf = append(w.buf, child.buf...)
}

func (w *writer) packedDouble(field int, values []float64) {
	if len(values) == 0 {
		return
	}
	child := &writer{}
	for _, value := range values {
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(value))
		child.buf = append(child.buf, buf[:]...)
	}
	w.tag(field, wireLengthDelimited)
	w.varint(uint64(len(child.buf)))
	w.buf = append(w.buf, child.buf...)
}

func encodeResourceSpans(w *writer, value otlp.ResourceSpans) {
	w.message(1, func(w *writer) { encodeResource(w, value.Resource) })
	for _, item := range value.ScopeSpans {
		scopeSpan := item
		w.message(2, func(w *writer) { encodeScopeSpans(w, scopeSpan) })
	}
	w.string(3, value.SchemaURL)
}

func encodeResource(w *writer, value otlp.Resource) {
	for _, item := range value.Attributes {
		attr := item
		w.message(1, func(w *writer) { encodeKeyValue(w, attr) })
	}
}

func encodeScopeSpans(w *writer, value otlp.ScopeSpans) {
	w.message(1, func(w *writer) { encodeInstrumentationScope(w, value.Scope) })
	for _, item := range value.Spans {
		span := item
		w.message(2, func(w *writer) { encodeSpan(w, span) })
	}
	w.string(3, value.SchemaURL)
}

func encodeSpan(w *writer, value otlp.Span) {
	w.rawBytes(1, value.TraceID)
	w.rawBytes(2, value.SpanID)
	w.string(3, value.TraceState)
	w.rawBytes(4, value.ParentSpanID)
	w.string(5, value.Name)
	w.uint32(6, value.Kind)
	w.fixed64(7, value.StartTimeUnixNano)
	w.fixed64(8, value.EndTimeUnixNano)
	for _, item := range value.Attributes {
		attr := item
		w.message(9, func(w *writer) { encodeKeyValue(w, attr) })
	}
	w.message(15, func(w *writer) { encodeStatus(w, value.Status) })
}

func encodeStatus(w *writer, value otlp.Status) {
	w.string(2, value.Message)
	w.uint32(3, value.Code)
}

func encodeKeyValue(w *writer, value otlp.KeyValue) {
	w.string(1, value.Key)
	w.message(2, func(w *writer) { encodeAnyValue(w, value.Value) })
}

func encodeAnyValue(w *writer, value otlp.AnyValue) {
	if value.StringValue != nil {
		w.string(1, *value.StringValue)
	}
	if value.BoolValue != nil {
		w.tag(2, wireVarint)
		if *value.BoolValue {
			w.varint(1)
		} else {
			w.varint(0)
		}
	}
	if value.IntValue != nil {
		w.uint64(3, uint64(*value.IntValue))
	}
	if value.DoubleValue != nil {
		w.double(4, *value.DoubleValue)
	}
	if value.ArrayValue != nil {
		arrayValue := *value.ArrayValue
		w.message(5, func(w *writer) { encodeArrayValue(w, arrayValue) })
	}
	if value.KVListValue != nil {
		kvlist := *value.KVListValue
		w.message(6, func(w *writer) { encodeKeyValueList(w, kvlist) })
	}
	w.rawBytes(7, value.BytesValue)
}

func encodeArrayValue(w *writer, value otlp.ArrayValue) {
	for _, item := range value.Values {
		entry := item
		w.message(1, func(w *writer) { encodeAnyValue(w, entry) })
	}
}

func encodeKeyValueList(w *writer, value otlp.KeyValueList) {
	for _, item := range value.Values {
		entry := item
		w.message(1, func(w *writer) { encodeKeyValue(w, entry) })
	}
}

func encodeResourceMetrics(w *writer, value otlp.ResourceMetrics) {
	w.message(1, func(w *writer) { encodeResource(w, value.Resource) })
	for _, item := range value.ScopeMetrics {
		scopeMetric := item
		w.message(2, func(w *writer) { encodeScopeMetrics(w, scopeMetric) })
	}
	w.string(3, value.SchemaURL)
}

func encodeScopeMetrics(w *writer, value otlp.ScopeMetrics) {
	w.message(1, func(w *writer) { encodeInstrumentationScope(w, value.Scope) })
	for _, item := range value.Metrics {
		metric := item
		w.message(2, func(w *writer) { encodeMetric(w, metric) })
	}
	w.string(3, value.SchemaURL)
}

func encodeInstrumentationScope(w *writer, value otlp.InstrumentationScope) {
	w.string(1, value.Name)
	w.string(2, value.Version)
	for _, item := range value.Attributes {
		attr := item
		w.message(3, func(w *writer) { encodeKeyValue(w, attr) })
	}
}

func encodeMetric(w *writer, value otlp.Metric) {
	w.string(1, value.Name)
	w.string(2, value.Description)
	w.string(3, value.Unit)
	if value.Sum != nil {
		sum := *value.Sum
		w.message(7, func(w *writer) { encodeSum(w, sum) })
	}
	if value.Histogram != nil {
		histogram := *value.Histogram
		w.message(9, func(w *writer) { encodeHistogram(w, histogram) })
	}
}

func encodeSum(w *writer, value otlp.Sum) {
	for _, item := range value.DataPoints {
		point := item
		w.message(1, func(w *writer) { encodeNumberDataPoint(w, point) })
	}
	w.uint32(2, value.AggregationTemporality)
	w.bool(3, value.IsMonotonic)
}

func encodeHistogram(w *writer, value otlp.Histogram) {
	for _, item := range value.DataPoints {
		point := item
		w.message(1, func(w *writer) { encodeHistogramDataPoint(w, point) })
	}
	w.uint32(2, value.AggregationTemporality)
}

func encodeNumberDataPoint(w *writer, value otlp.NumberDataPoint) {
	w.fixed64(2, value.StartTimeUnixNano)
	w.fixed64(3, value.TimeUnixNano)
	if value.AsDouble != nil {
		w.double(4, *value.AsDouble)
	}
	if value.AsInt != nil {
		w.uint64(6, uint64(*value.AsInt))
	}
	for _, item := range value.Attributes {
		attr := item
		w.message(7, func(w *writer) { encodeKeyValue(w, attr) })
	}
	w.uint32(8, value.Flags)
}

func encodeHistogramDataPoint(w *writer, value otlp.HistogramDataPoint) {
	w.fixed64(2, value.StartTimeUnixNano)
	w.fixed64(3, value.TimeUnixNano)
	w.fixed64(4, value.Count)
	w.double(5, value.Sum)
	w.packedFixed64(6, value.BucketCounts)
	w.packedDouble(7, value.ExplicitBounds)
	for _, item := range value.Attributes {
		attr := item
		w.message(9, func(w *writer) { encodeKeyValue(w, attr) })
	}
	w.uint32(10, value.Flags)
	w.double(11, value.Min)
	w.double(12, value.Max)
}
