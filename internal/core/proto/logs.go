package proto

import (
	"fmt"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func EncodeExportLogsServiceRequest(request otlp.ExportLogsServiceRequest) []byte {
	w := &writer{}
	for _, resource := range request.ResourceLogs {
		w.message(1, func(w *writer) {
			w.message(1, func(w *writer) { encodeResource(w, resource.Resource) })
			for _, scope := range resource.ScopeLogs {
				w.message(2, func(w *writer) {
					w.message(1, func(w *writer) { encodeInstrumentationScope(w, scope.Scope) })
					for _, record := range scope.LogRecords {
						w.message(2, func(w *writer) { encodeLogRecord(w, record) })
					}
					w.string(3, scope.SchemaURL)
				})
			}
			w.string(3, resource.SchemaURL)
		})
	}
	return w.bytes()
}

func encodeLogRecord(w *writer, value otlp.LogRecord) {
	w.fixed64(1, value.TimeUnixNano)
	w.uint32(2, value.SeverityNumber)
	w.string(3, value.SeverityText)
	w.message(5, func(w *writer) { encodeAnyValue(w, value.Body) })
	for _, attribute := range value.Attributes {
		w.message(6, func(w *writer) { encodeKeyValue(w, attribute) })
	}
	w.uint32(7, value.DroppedAttributesCount)
	if value.Flags != 0 {
		w.tag(8, 5)
		w.buf = append(w.buf, byte(value.Flags), byte(value.Flags>>8), byte(value.Flags>>16), byte(value.Flags>>24))
	}
	w.rawBytes(9, value.TraceID)
	w.rawBytes(10, value.SpanID)
	w.fixed64(11, value.ObservedTimeUnixNano)
	w.string(12, value.EventName)
}

// walkLogMessage bounds nested messages before handing them to existing readers.
func walkLogMessage(data []byte, visit func(int, int, *protoReader) (bool, error)) error {
	r := protoReader{data: data}
	for !r.eof() {
		field, wire, err := r.tag()
		if err != nil {
			return err
		}
		if field == 0 {
			return fmt.Errorf("invalid protobuf field zero")
		}
		handled, err := visit(field, wire, &r)
		if err != nil {
			return err
		}
		if !handled {
			if err := r.skip(wire); err != nil {
				return err
			}
		}
	}
	return nil
}

func DecodeExportLogsServiceRequest(data []byte) (otlp.ExportLogsServiceRequest, error) {
	var out otlp.ExportLogsServiceRequest
	err := walkLogMessage(data, func(field, wire int, r *protoReader) (bool, error) {
		if field != 1 || wire != wireLengthDelimited {
			return false, nil
		}
		payload, err := r.bytes()
		if err != nil {
			return true, err
		}
		var resource otlp.ResourceLogs
		err = walkLogMessage(payload, func(field, wire int, r *protoReader) (bool, error) {
			if wire != wireLengthDelimited || field < 1 || field > 3 {
				return false, nil
			}
			body, err := r.bytes()
			if err != nil {
				return true, err
			}
			switch field {
			case 1:
				resource.Resource, err = decodeResource(body)
			case 2:
				var scope otlp.ScopeLogs
				scope, err = decodeScopeLogs(body)
				resource.ScopeLogs = append(resource.ScopeLogs, scope)
			case 3:
				resource.SchemaURL = string(body)
			}
			return true, err
		})
		out.ResourceLogs = append(out.ResourceLogs, resource)
		return true, err
	})
	return out, err
}

func decodeScopeLogs(data []byte) (otlp.ScopeLogs, error) {
	var out otlp.ScopeLogs
	err := walkLogMessage(data, func(field, wire int, r *protoReader) (bool, error) {
		if wire != wireLengthDelimited || field < 1 || field > 3 {
			return false, nil
		}
		body, err := r.bytes()
		if err != nil {
			return true, err
		}
		switch field {
		case 1:
			out.Scope, err = decodeInstrumentationScope(body)
		case 2:
			var record otlp.LogRecord
			record, err = decodeLogRecord(body)
			out.LogRecords = append(out.LogRecords, record)
		case 3:
			out.SchemaURL = string(body)
		}
		return true, err
	})
	return out, err
}

func decodeLogRecord(data []byte) (otlp.LogRecord, error) {
	var out otlp.LogRecord
	err := walkLogMessage(data, func(field, wire int, r *protoReader) (bool, error) {
		switch {
		case (field == 1 || field == 11) && wire == wireFixed64:
			n, err := r.fixed64()
			if field == 1 {
				out.TimeUnixNano = n
			} else {
				out.ObservedTimeUnixNano = n
			}
			return true, err
		case (field == 2 || field == 7) && wire == wireVarint:
			n, err := r.varint()
			if field == 2 {
				out.SeverityNumber = uint32(n)
			} else {
				out.DroppedAttributesCount = uint32(n)
			}
			return true, err
		case field == 8 && wire == 5:
			if len(r.data)-r.pos < 4 {
				return true, ioErrUnexpectedEOF("flags")
			}
			b := r.data[r.pos : r.pos+4]
			r.pos += 4
			out.Flags = uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
			return true, nil
		case (field == 3 || field == 5 || field == 6 || field == 9 || field == 10 || field == 12) && wire == wireLengthDelimited:
			body, err := r.bytes()
			if err != nil {
				return true, err
			}
			switch field {
			case 3:
				out.SeverityText = string(body)
			case 5:
				out.Body, err = decodeAnyValue(body)
			case 6:
				var attr otlp.KeyValue
				attr, err = decodeKeyValue(body)
				out.Attributes = append(out.Attributes, attr)
			case 9:
				out.TraceID = append([]byte(nil), body...)
			case 10:
				out.SpanID = append([]byte(nil), body...)
			case 12:
				out.EventName = string(body)
			}
			return true, err
		}
		return false, nil
	})
	return out, err
}

// DecodeLogsRejected returns the rejected count from an OTLP protobuf response.
func DecodeLogsRejected(data []byte) (int64, error) {
	var rejected int64
	err := walkLogMessage(data, func(field, wire int, r *protoReader) (bool, error) {
		if field != 1 || wire != wireLengthDelimited {
			return false, nil
		}
		body, err := r.bytes()
		if err != nil {
			return true, err
		}
		err = walkLogMessage(body, func(field, wire int, r *protoReader) (bool, error) {
			if field != 1 || wire != wireVarint {
				return false, nil
			}
			n, err := r.varint()
			rejected = int64(n)
			if rejected < 0 {
				return true, fmt.Errorf("negative rejected log count")
			}
			return true, err
		})
		return true, err
	})
	return rejected, err
}
