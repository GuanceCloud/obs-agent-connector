package proto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func DecodeExportTraceServiceRequest(data []byte) (otlp.ExportTraceServiceRequest, error) {
	reader := protoReader{data: data}
	var request otlp.ExportTraceServiceRequest
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return request, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return request, err
			}
			resourceSpan, err := decodeResourceSpans(payload)
			if err != nil {
				return request, err
			}
			request.ResourceSpans = append(request.ResourceSpans, resourceSpan)
		default:
			if err := reader.skip(wire); err != nil {
				return request, err
			}
		}
	}
	return request, nil
}

func DecodeExportMetricsServiceRequest(data []byte) (otlp.ExportMetricsServiceRequest, error) {
	reader := protoReader{data: data}
	var request otlp.ExportMetricsServiceRequest
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return request, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return request, err
			}
			resourceMetric, err := decodeResourceMetrics(payload)
			if err != nil {
				return request, err
			}
			request.ResourceMetrics = append(request.ResourceMetrics, resourceMetric)
		default:
			if err := reader.skip(wire); err != nil {
				return request, err
			}
		}
	}
	return request, nil
}

type protoReader struct {
	data []byte
	pos  int
}

func (r *protoReader) eof() bool {
	return r.pos >= len(r.data)
}

func (r *protoReader) tag() (int, int, error) {
	value, err := r.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(value >> 3), int(value & 0x7), nil
}

func (r *protoReader) varint() (uint64, error) {
	var value uint64
	var shift uint
	for {
		if r.pos >= len(r.data) {
			return 0, ioErrUnexpectedEOF("varint")
		}
		b := r.data[r.pos]
		r.pos++
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, errors.New("protobuf varint overflow")
		}
	}
}

func (r *protoReader) fixed64() (uint64, error) {
	if r.pos+8 > len(r.data) {
		return 0, ioErrUnexpectedEOF("fixed64")
	}
	value := binary.LittleEndian.Uint64(r.data[r.pos : r.pos+8])
	r.pos += 8
	return value, nil
}

func (r *protoReader) bytes() ([]byte, error) {
	length, err := r.varint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(r.data)-r.pos) {
		return nil, ioErrUnexpectedEOF("length-delimited")
	}
	out := r.data[r.pos : r.pos+int(length)]
	r.pos += int(length)
	return out, nil
}

func (r *protoReader) string() (string, error) {
	value, err := r.bytes()
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (r *protoReader) skip(wire int) error {
	switch wire {
	case wireVarint:
		_, err := r.varint()
		return err
	case wireFixed64:
		_, err := r.fixed64()
		return err
	case wireLengthDelimited:
		_, err := r.bytes()
		return err
	default:
		return fmt.Errorf("unsupported protobuf wire type: %d", wire)
	}
}

func decodeResourceSpans(data []byte) (otlp.ResourceSpans, error) {
	reader := protoReader{data: data}
	var value otlp.ResourceSpans
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			resource, err := decodeResource(payload)
			if err != nil {
				return value, err
			}
			value.Resource = resource
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			scopeSpans, err := decodeScopeSpans(payload)
			if err != nil {
				return value, err
			}
			value.ScopeSpans = append(value.ScopeSpans, scopeSpans)
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.SchemaURL = text
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeScopeSpans(data []byte) (otlp.ScopeSpans, error) {
	reader := protoReader{data: data}
	var value otlp.ScopeSpans
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			scope, err := decodeInstrumentationScope(payload)
			if err != nil {
				return value, err
			}
			value.Scope = scope
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			span, err := decodeSpan(payload)
			if err != nil {
				return value, err
			}
			value.Spans = append(value.Spans, span)
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.SchemaURL = text
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeSpan(data []byte) (otlp.Span, error) {
	reader := protoReader{data: data}
	var value otlp.Span
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.TraceID = append([]byte(nil), payload...)
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.SpanID = append([]byte(nil), payload...)
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.TraceState = text
		case field == 4 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.ParentSpanID = append([]byte(nil), payload...)
		case field == 5 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Name = text
		case field == 6 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.Kind = uint32(number)
		case field == 7 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.StartTimeUnixNano = number
		case field == 8 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.EndTimeUnixNano = number
		case field == 9 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			attr, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Attributes = append(value.Attributes, attr)
		case field == 15 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			status, err := decodeStatus(payload)
			if err != nil {
				return value, err
			}
			value.Status = status
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeStatus(data []byte) (otlp.Status, error) {
	reader := protoReader{data: data}
	var value otlp.Status
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 2 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Message = text
		case field == 3 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.Code = uint32(number)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeResourceMetrics(data []byte) (otlp.ResourceMetrics, error) {
	reader := protoReader{data: data}
	var value otlp.ResourceMetrics
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			resource, err := decodeResource(payload)
			if err != nil {
				return value, err
			}
			value.Resource = resource
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			scopeMetrics, err := decodeScopeMetrics(payload)
			if err != nil {
				return value, err
			}
			value.ScopeMetrics = append(value.ScopeMetrics, scopeMetrics)
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.SchemaURL = text
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeScopeMetrics(data []byte) (otlp.ScopeMetrics, error) {
	reader := protoReader{data: data}
	var value otlp.ScopeMetrics
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			scope, err := decodeInstrumentationScope(payload)
			if err != nil {
				return value, err
			}
			value.Scope = scope
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			metric, err := decodeMetric(payload)
			if err != nil {
				return value, err
			}
			value.Metrics = append(value.Metrics, metric)
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.SchemaURL = text
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeMetric(data []byte) (otlp.Metric, error) {
	reader := protoReader{data: data}
	var value otlp.Metric
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Name = text
		case field == 2 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Description = text
		case field == 3 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Unit = text
		case field == 7 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			sum, err := decodeSum(payload)
			if err != nil {
				return value, err
			}
			value.Sum = &sum
		case field == 9 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			histogram, err := decodeHistogram(payload)
			if err != nil {
				return value, err
			}
			value.Histogram = &histogram
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeSum(data []byte) (otlp.Sum, error) {
	reader := protoReader{data: data}
	var value otlp.Sum
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			point, err := decodeNumberDataPoint(payload)
			if err != nil {
				return value, err
			}
			value.DataPoints = append(value.DataPoints, point)
		case field == 2 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.AggregationTemporality = uint32(number)
		case field == 3 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.IsMonotonic = number != 0
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeHistogram(data []byte) (otlp.Histogram, error) {
	reader := protoReader{data: data}
	var value otlp.Histogram
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			point, err := decodeHistogramDataPoint(payload)
			if err != nil {
				return value, err
			}
			value.DataPoints = append(value.DataPoints, point)
		case field == 2 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.AggregationTemporality = uint32(number)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeNumberDataPoint(data []byte) (otlp.NumberDataPoint, error) {
	reader := protoReader{data: data}
	var value otlp.NumberDataPoint
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 2 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.StartTimeUnixNano = number
		case field == 3 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.TimeUnixNano = number
		case field == 4 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			parsed := math.Float64frombits(number)
			value.AsDouble = &parsed
		case field == 6 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			parsed := int64(number)
			value.AsInt = &parsed
		case field == 7 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			attr, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Attributes = append(value.Attributes, attr)
		case field == 8 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.Flags = uint32(number)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeHistogramDataPoint(data []byte) (otlp.HistogramDataPoint, error) {
	reader := protoReader{data: data}
	var value otlp.HistogramDataPoint
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 2 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.StartTimeUnixNano = number
		case field == 3 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.TimeUnixNano = number
		case field == 4 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.Count = number
		case field == 5 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.Sum = math.Float64frombits(number)
		case field == 6 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.BucketCounts, err = decodePackedFixed64(payload)
			if err != nil {
				return value, err
			}
		case field == 7 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.ExplicitBounds, err = decodePackedDouble(payload)
			if err != nil {
				return value, err
			}
		case field == 9 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			attr, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Attributes = append(value.Attributes, attr)
		case field == 10 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			value.Flags = uint32(number)
		case field == 11 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.Min = math.Float64frombits(number)
		case field == 12 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			value.Max = math.Float64frombits(number)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeResource(data []byte) (otlp.Resource, error) {
	reader := protoReader{data: data}
	var value otlp.Resource
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			attr, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Attributes = append(value.Attributes, attr)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeInstrumentationScope(data []byte) (otlp.InstrumentationScope, error) {
	reader := protoReader{data: data}
	var value otlp.InstrumentationScope
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Name = text
		case field == 2 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Version = text
		case field == 3 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			attr, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Attributes = append(value.Attributes, attr)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeKeyValue(data []byte) (otlp.KeyValue, error) {
	reader := protoReader{data: data}
	var value otlp.KeyValue
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.Key = text
		case field == 2 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			anyValue, err := decodeAnyValue(payload)
			if err != nil {
				return value, err
			}
			value.Value = anyValue
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeAnyValue(data []byte) (otlp.AnyValue, error) {
	reader := protoReader{data: data}
	var value otlp.AnyValue
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			text, err := reader.string()
			if err != nil {
				return value, err
			}
			value.StringValue = &text
		case field == 2 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			parsed := number != 0
			value.BoolValue = &parsed
		case field == 3 && wire == wireVarint:
			number, err := reader.varint()
			if err != nil {
				return value, err
			}
			parsed := int64(number)
			value.IntValue = &parsed
		case field == 4 && wire == wireFixed64:
			number, err := reader.fixed64()
			if err != nil {
				return value, err
			}
			parsed := math.Float64frombits(number)
			value.DoubleValue = &parsed
		case field == 5 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			arrayValue, err := decodeArrayValue(payload)
			if err != nil {
				return value, err
			}
			value.ArrayValue = &arrayValue
		case field == 6 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			kvListValue, err := decodeKeyValueList(payload)
			if err != nil {
				return value, err
			}
			value.KVListValue = &kvListValue
		case field == 7 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			value.BytesValue = append([]byte(nil), payload...)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeArrayValue(data []byte) (otlp.ArrayValue, error) {
	reader := protoReader{data: data}
	var value otlp.ArrayValue
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			anyValue, err := decodeAnyValue(payload)
			if err != nil {
				return value, err
			}
			value.Values = append(value.Values, anyValue)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodeKeyValueList(data []byte) (otlp.KeyValueList, error) {
	reader := protoReader{data: data}
	var value otlp.KeyValueList
	for !reader.eof() {
		field, wire, err := reader.tag()
		if err != nil {
			return value, err
		}
		switch {
		case field == 1 && wire == wireLengthDelimited:
			payload, err := reader.bytes()
			if err != nil {
				return value, err
			}
			item, err := decodeKeyValue(payload)
			if err != nil {
				return value, err
			}
			value.Values = append(value.Values, item)
		default:
			if err := reader.skip(wire); err != nil {
				return value, err
			}
		}
	}
	return value, nil
}

func decodePackedFixed64(data []byte) ([]uint64, error) {
	if len(data)%8 != 0 {
		return nil, errors.New("packed fixed64 payload size is not aligned")
	}
	out := make([]uint64, 0, len(data)/8)
	for offset := 0; offset < len(data); offset += 8 {
		out = append(out, binary.LittleEndian.Uint64(data[offset:offset+8]))
	}
	return out, nil
}

func decodePackedDouble(data []byte) ([]float64, error) {
	values, err := decodePackedFixed64(data)
	if err != nil {
		return nil, err
	}
	out := make([]float64, 0, len(values))
	for _, value := range values {
		out = append(out, math.Float64frombits(value))
	}
	return out, nil
}

func ioErrUnexpectedEOF(section string) error {
	return fmt.Errorf("unexpected EOF while decoding %s", section)
}
