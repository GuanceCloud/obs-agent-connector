package sandbox

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestSandboxLifecycleTimesArePrefixedAndQueryable(t *testing.T) {
	created := time.Date(2026, 10, 23, 11, 7, 8, 166386981, time.FixedZone("UTC+8", 8*60*60))
	destroyed := created.Add(time.Second)
	for _, tc := range []struct {
		name, eventName, state     string
		createdAt, destroyedAt     *time.Time
		wantCreated, wantDestroyed bool
	}{
		{name: "created", eventName: "obs.sandbox.created", wantCreated: true},
		{name: "destroyed", eventName: "obs.sandbox.destroyed", createdAt: &created, wantCreated: true, wantDestroyed: true},
		{name: "historical-snapshot", eventName: "obs.sandbox.snapshot", state: "destroyed", createdAt: &created, destroyedAt: &destroyed, wantCreated: true, wantDestroyed: true},
		{name: "unknown-snapshot", eventName: "obs.sandbox.snapshot", state: "running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := sampleEvent(tc.name, tc.eventName)
			e.Timestamp, e.CreatedAt, e.DestroyedAt, e.State = destroyed, tc.createdAt, tc.destroyedAt, tc.state
			if tc.eventName == "obs.sandbox.created" {
				e.Timestamp = created
			}
			if err := e.Validate(); err != nil {
				t.Fatal(err)
			}
			record := e.record(destroyed.Add(time.Second), nil, false)
			body := jsonBody(t, record.Body)
			for _, key := range []string{"created_at", "destroyed_at"} {
				if body[key] != nil || record.Attributes[key] != nil {
					t.Fatal("unprefixed lifecycle field escaped")
				}
			}
			wire := proto.EncodeExportLogsServiceRequest(otlp.LogsToProtoRequest([]model.LogRecord{record}))
			decoded, err := proto.DecodeExportLogsServiceRequest(wire)
			if err != nil {
				t.Fatal(err)
			}
			log := decoded.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			for key, expected := range map[string]struct {
				present bool
				value   time.Time
			}{
				"sandbox_created_at":   {tc.wantCreated, created},
				"sandbox_destroyed_at": {tc.wantDestroyed, destroyed},
			} {
				if !expected.present {
					if body[key] != nil || attr(log, key) != "" {
						t.Fatal("unknown lifecycle time was manufactured")
					}
					continue
				}
				want := expected.value.UTC().Format(time.RFC3339Nano)
				if body[key] != want || attr(log, key) != want {
					t.Fatal("prefixed lifecycle time missing from body or OTLP attributes")
				}
			}
		})
	}
}

func TestSandboxTraceRelationsDoNotRepeatLifecycleTimes(t *testing.T) {
	store := Store{Root: t.TempDir()}
	e := sampleEvent("created", "obs.sandbox.created")
	e.SessionID, e.TurnID, e.ToolCallID = "session", "turn", "call"
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := store.BindSpans([]model.Span{toolSpan("turn", "call", "a")}); err != nil {
		t.Fatal(err)
	}
	receiver := &sink{}
	server := httptest.NewServer(receiver.handler(t))
	defer server.Close()
	if err := store.Flush(context.Background(), transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs"}}); err != nil {
		t.Fatal(err)
	}
	if len(receiver.records) != 2 {
		t.Fatal("missing lifecycle or relation record")
	}
	for _, record := range receiver.records {
		if record.EventName == "obs.sandbox.trace.bound" {
			if record.Body.StringValue == nil {
				t.Fatal("trace relation requires a readable body")
			}
			body := jsonBody(t, *record.Body.StringValue)
			if attr(record, "sandbox_created_at") != "" || attr(record, "sandbox_destroyed_at") != "" || body["sandbox_created_at"] != nil || body["sandbox_destroyed_at"] != nil || body["state"] != nil || body["last_observed_at"] != nil {
				t.Fatal("trace relation repeated lifecycle assertions")
			}
		}
	}
}
