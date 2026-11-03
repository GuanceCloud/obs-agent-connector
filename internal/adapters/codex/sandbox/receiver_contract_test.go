package sandbox

import (
	"context"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

// Model the receiver's source precedence, rather than assuming the record's
// source attribute controls routing. See DataKit getServiceAndSource.
func guanceSource(resource otlp.Resource) string {
	for _, key := range []string{"log.source", "service.name"} {
		for _, attr := range resource.Attributes {
			if attr.Key == key && attr.Value.StringValue != nil && *attr.Value.StringValue != "" {
				return *attr.Value.StringValue
			}
		}
	}
	return "otel_logs"
}

func TestSandboxReceiverContractForEveryEventType(t *testing.T) {
	for _, name := range []string{"discovered", "created", "ready", "snapshot", "destroyed", "failed", "observation.lost", "trace.bound"} {
		t.Run(name, func(t *testing.T) {
			e := sampleEvent(name, "obs.sandbox."+name)
			if name == "trace.bound" {
				e.SessionID, e.TurnID, e.ToolCallID = "session", "turn", "call"
			}
			e.Reason = "Authorization: Bearer sensitive-value"
			resource := map[string]any{"service.name": "actual-agent-service", "log.source": "wrong-source", "agent_id": "test-agent"}
			record := e.record(e.Timestamp.Add(time.Second), resource, true)
			if resource["log.source"] != "wrong-source" || record.Resource["service.name"] != "actual-agent-service" {
				t.Fatal("export changed configured resource or service identity")
			}
			if !reflect.DeepEqual(record, exportRecord(record)) {
				t.Fatal("record normalization must be idempotent across replay")
			}
			request, err := proto.DecodeExportLogsServiceRequest(proto.EncodeExportLogsServiceRequest(otlp.LogsToProtoRequest([]model.LogRecord{record})))
			if err != nil {
				t.Fatal(err)
			}
			if guanceSource(request.ResourceLogs[0].Resource) != Source {
				t.Fatal("receiver would use the service name instead of agent_sandbox")
			}
			wire := request.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			if wire.EventName != e.EventName || attr(wire, "event_name") != e.EventName || wire.Body.StringValue == nil {
				t.Fatal("event type or string Body missing from wire")
			}
			body := jsonBody(t, *wire.Body.StringValue)
			if body["event_name"] != e.EventName || body["sandbox_id"] != e.SandboxID || body["executor_id"] != e.ExecutorID || strings.Contains(*wire.Body.StringValue, "sensitive-value") {
				t.Fatal("body identity, event name, or privacy contract broken")
			}
			if wire.TimeUnixNano != record.TimeUnixNano || wire.ObservedTimeUnixNano != record.ObservedTimeUnixNano {
				t.Fatal("wire changed occurrence or observation time")
			}
			if name == "trace.bound" && (body["reason"] != nil || body["state"] != nil) {
				t.Fatal("association repeated lifecycle details")
			}
		})
	}
}

func TestQueuedLegacyRecordsUseCurrentWireContract(t *testing.T) {
	store := Store{Root: t.TempDir()}
	e := sampleEvent("legacy", "obs.sandbox.created")
	record := e.record(e.Timestamp.Add(time.Second), nil, false)
	delete(record.Resource, "log.source")
	delete(record.Attributes, "event_name")
	delete(record.Attributes, "sandbox_created_at")
	record.Body = map[string]any{"created_at": e.Timestamp.Format(time.RFC3339Nano)}
	if err := store.update(func(l *ledger) error {
		l.Queue = []pending{{Key: "stable-legacy-key", Record: record}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	receiver := &sink{}
	server := httptest.NewServer(receiver.handler(t))
	defer server.Close()
	if err := store.Flush(context.Background(), transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs"}}); err != nil {
		t.Fatal(err)
	}
	request, err := proto.DecodeExportLogsServiceRequest(receiver.bodies[0])
	if err != nil {
		t.Fatal(err)
	}
	wire := receiver.records[0]
	if guanceSource(request.ResourceLogs[0].Resource) != Source || attr(wire, "event_name") != e.EventName || attr(wire, "sandbox_created_at") != e.Timestamp.Format(time.RFC3339Nano) {
		t.Fatal("legacy queue did not acquire current receiver fields")
	}
	if wire.TimeUnixNano != record.TimeUnixNano || wire.ObservedTimeUnixNano != record.ObservedTimeUnixNano || attr(wire, "obs.event.id") != e.EventID {
		t.Fatal("upgrading wire contract changed event identity or clocks")
	}
	if wire.Body.StringValue == nil || jsonBody(t, *wire.Body.StringValue)["created_at"] != nil {
		t.Fatal("legacy body was not converted to prefixed JSON")
	}
}

func TestLegacyDelayedRelationDoesNotInheritLifecycleFailure(t *testing.T) {
	store := Store{Root: t.TempDir()}
	e := sampleEvent("failure", "obs.sandbox.failed")
	record := e.record(e.Timestamp, nil, false)
	record.EventName = "obs.sandbox.trace.bound"
	record.Body = nil
	delete(record.Resource, "log.source")
	if err := store.update(func(l *ledger) error {
		l.Relations = []relation{{Key: "legacy-relation", ToolKey: digest("session", "turn", "call"), Record: record, Observed: e.Timestamp}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.BindSpans([]model.Span{toolSpan("turn", "call", "c")}); err != nil {
		t.Fatal(err)
	}
	receiver := &sink{}
	server := httptest.NewServer(receiver.handler(t))
	defer server.Close()
	if err := store.Flush(context.Background(), transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs"}}); err != nil {
		t.Fatal(err)
	}
	wire := receiver.records[0]
	if wire.EventName != "obs.sandbox.trace.bound" || attr(wire, "event_name") != wire.EventName || wire.SeverityNumber != 9 || wire.SeverityText != "INFO" || len(wire.TraceID) != 16 || len(wire.SpanID) != 8 {
		t.Fatal("delayed relation has stale event type, severity, or missing OBS context")
	}
	if attr(wire, "obs.sandbox.state") != "" || wire.Body.StringValue == nil || len(jsonBody(t, *wire.Body.StringValue)) != 3 {
		t.Fatal("legacy relation was exported as a lifecycle assertion")
	}
}
