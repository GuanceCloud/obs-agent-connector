package proto

import (
	"bytes"
	"encoding/hex"
	"reflect"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func TestLogsRoundTripPreservesSourceBodyContextAndClocks(t *testing.T) {
	records := []model.LogRecord{{
		EventName: "obs.sandbox.snapshot", TimeUnixNano: 123, ObservedTimeUnixNano: 456,
		SeverityNumber: 9, SeverityText: "INFO", Flags: 1,
		TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef",
		Attributes: map[string]any{"source": "agent_sandbox"},
		Body:       map[string]any{"state": "running", "created_at": "2026-10-21T00:00:00Z"},
		Resource:   map[string]any{"obs.sandbox.id": "sbx-1"}, Scope: model.Scope{Name: "sandbox"},
	}}
	request := otlp.LogsToProtoRequest(records)
	decoded, err := DecodeExportLogsServiceRequest(EncodeExportLogsServiceRequest(request))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, decoded) {
		t.Fatalf("OTLP logs round trip changed fields: %#v", decoded)
	}
	other := records[0]
	other.Resource = map[string]any{"obs.sandbox.id": "sbx-2"}
	if got := otlp.LogsToProtoRequest(append(records, other)); len(got.ResourceLogs) != 2 {
		t.Fatal("sandbox resources merged")
	}
}

func TestLogRecordMatchesProtocolFieldNumbers(t *testing.T) {
	// Independent wire fixture: fixed64 time #1; severity #2; fixed32 flags #8;
	// observed time #11; EventName #12. No encoder is used to construct it.
	wire, err := hex.DecodeString("09010000000000000010094501000000590200000000000000620178")
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeLogRecord(wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.TimeUnixNano != 1 || got.ObservedTimeUnixNano != 2 || got.SeverityNumber != 9 || got.Flags != 1 || got.EventName != "x" {
		t.Fatalf("wrong field mapping: %+v", got)
	}
	w := &writer{}
	encodeLogRecord(w, got)
	if !bytes.Equal(w.bytes(), wire) {
		t.Fatalf("wire fixture changed: %x", w.bytes())
	}
}

func TestLogsRejectMalformedWireAndDecodePartialSuccess(t *testing.T) {
	for _, wire := range [][]byte{{0x0a, 0x7f}, {0x00}, {0x0a, 0x04, 0x12}} {
		if _, err := DecodeExportLogsServiceRequest(wire); err == nil {
			t.Fatalf("accepted malformed wire %x", wire)
		}
	}
	count, err := DecodeLogsRejected([]byte{0x0a, 0x02, 0x08, 0x01})
	if err != nil || count != 1 {
		t.Fatalf("partial success = %d, %v", count, err)
	}
	if _, err := DecodeLogsRejected([]byte{0x0a, 0x08}); err == nil {
		t.Fatal("accepted truncated response")
	}
}
