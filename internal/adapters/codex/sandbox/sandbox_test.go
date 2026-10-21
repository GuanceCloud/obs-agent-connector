package sandbox

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
	"github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func sampleEvent(id, name string) Event {
	return Event{SchemaVersion: 1, EventID: id, EventName: name, Timestamp: time.Date(2026, 10, 21, 1, 0, 0, 0, time.UTC), SandboxID: "sbx-1", ExecutorID: "executor-1", IdentitySource: "native", RuntimeSource: "fixture-runtime"}
}

func toolSpan(turn, call, trace string) model.Span {
	return model.Span{Name: "tool:shell", TraceID: strings.Repeat(trace, 32), SpanID: strings.Repeat(trace, 16), Attributes: map[string]any{"gen_ai.conversation.id": "session", "run_id": turn, "gen_ai.tool.call.id": call}}
}

type sink struct {
	mu       sync.Mutex
	records  []otlp.LogRecord
	bodies   [][]byte
	status   int
	response []byte
	path     string
}

func (s *sink) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := s.path
		if path == "" {
			path = "/otel/v1/logs"
		}
		if r.URL.Path != path || r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("wrong OTLP request: %s", r.URL.Path)
			w.WriteHeader(400)
			return
		}
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		body, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Error(err)
			return
		}
		request, err := proto.DecodeExportLogsServiceRequest(body)
		if err != nil {
			t.Error(err)
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.bodies = append(s.bodies, body)
		for _, resource := range request.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				s.records = append(s.records, scope.LogRecords...)
			}
		}
		status := s.status
		if status == 0 {
			status = 200
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(status)
		w.Write(s.response)
	}
}

func attr(r otlp.LogRecord, key string) string {
	for _, a := range r.Attributes {
		if a.Key == key && a.Value.StringValue != nil {
			return *a.Value.StringValue
		}
	}
	return ""
}

func TestInstalledProfileExportsLogsToConfiguredRoute(t *testing.T) {
	for _, tc := range []struct{ profile, path string }{
		{"gtrace", "/v1/write/otel-logs"},
		{"otlp", "/v1/logs"},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			sink := &sink{path: tc.path}
			server := httptest.NewServer(sink.handler(t))
			defer server.Close()
			home := t.TempDir()
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			enabled := true
			next, err := install.MergeCodexRuntimeConfig(nil, install.CodexOptions{Endpoint: server.URL, InstallType: tc.profile, Sandbox: install.CodexSandboxOptions{Enabled: &enabled}}, false)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			body, err := json.Marshal(next)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.Resolve(config.ResolveOptions{Home: home, Cwd: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			event, err := json.Marshal(sampleEvent("create", "obs.sandbox.created"))
			if err != nil {
				t.Fatal(err)
			}
			if err := Run(context.Background(), strings.NewReader(string(event)+"\n"), cfg, server.Client()); err != nil {
				t.Fatal(err)
			}
			if len(sink.records) != 1 || attr(sink.records[0], "source") != "agent_sandbox" {
				t.Fatal("profile route did not receive the sandbox OTLP record")
			}
		})
	}
}

func TestLifecycleAndDelayedManyToManyBindingSurviveRestart(t *testing.T) {
	store := Store{Root: t.TempDir(), CaptureContent: true}
	e := sampleEvent("create", "obs.sandbox.created")
	e.SessionID, e.TurnID, e.ToolCallID, e.ExecutionID = "session", "turn-1", "call-1", "exec-1"
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	sink := &sink{}
	server := httptest.NewServer(sink.handler(t))
	defer server.Close()
	client := transport.Client{Config: transport.Config{LogsURL: server.URL + "/otel/v1/logs"}}
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 1 || sink.records[0].EventName != "obs.sandbox.created" || attr(sink.records[0], "obs.sandbox.state") != "created" {
		t.Fatal("creation was duplicated or overwritten by relation")
	}
	if len(sink.records[0].TraceID) != 0 {
		t.Fatal("invented an early trace association")
	}
	store = Store{Root: store.Root}
	if err := store.BindSpans([]model.Span{toolSpan("turn-1", "call-1", "a"), toolSpan("turn-2", "call-2", "b")}); err != nil {
		t.Fatal(err)
	}
	second := sampleEvent("poll", "obs.sandbox.snapshot")
	second.State = "running"
	second.SessionID, second.TurnID, second.ToolCallID, second.ExecutionID = "session", "turn-2", "call-2", "exec-1"
	if err := store.Apply(second); err != nil {
		t.Fatal(err)
	}
	repeated := second
	repeated.EventID = "poll-again"
	if err := store.Apply(repeated); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	relations := 0
	traces := map[string]bool{}
	for _, record := range sink.records {
		if attr(record, "source") != Source {
			t.Fatal("lost agent_sandbox source")
		}
		if record.EventName == "obs.sandbox.destroyed" {
			t.Fatal("turn completion destroyed sandbox")
		}
		if record.EventName == "obs.sandbox.trace.bound" {
			relations++
			traces[hex.EncodeToString(record.TraceID)] = true
			if attr(record, "obs.sandbox.state") != "" {
				t.Fatal("binding mutated lifecycle state")
			}
		}
	}
	if relations != 2 || !traces[strings.Repeat("a", 32)] || !traces[strings.Repeat("b", 32)] {
		t.Fatalf("wrong cross-trace relations: %d", relations)
	}
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 5 {
		t.Fatalf("replayed accepted lifecycle event: %d", len(sink.records))
	}
}

func TestRetryAndPermanentRejectionHaveExplicitOutcomes(t *testing.T) {
	clock := time.Now().UTC()
	store := Store{Root: t.TempDir(), Now: func() time.Time { return clock }}
	if err := store.Apply(sampleEvent("event", "obs.sandbox.discovered")); err != nil {
		t.Fatal(err)
	}
	sink := &sink{status: 503}
	server := httptest.NewServer(sink.handler(t))
	defer server.Close()
	client := transport.Client{Config: transport.Config{LogsURL: server.URL + "/otel/v1/logs"}}
	if err := store.Flush(context.Background(), client); err == nil {
		t.Fatal("expected retryable failure")
	}
	sink.status = 200
	store = Store{Root: store.Root, Now: store.Now}
	if err := store.Flush(context.Background(), client); !errors.Is(err, ErrDeferred) {
		t.Fatal("retry backoff was lost across restart")
	}
	clock = clock.Add(time.Minute)
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(sink.bodies) != 2 || string(sink.bodies[0]) != string(sink.bodies[1]) {
		t.Fatal("retry changed event identity or timestamps")
	}
	if err := store.Apply(sampleEvent("reject", "obs.sandbox.failed")); err != nil {
		t.Fatal(err)
	}
	sink.response = []byte{0x0a, 0x02, 0x08, 0x01}
	if err := store.Flush(context.Background(), client); err == nil {
		t.Fatal("partial rejection reported success")
	}
	before := len(sink.bodies)
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(sink.bodies) != before {
		t.Fatal("replayed partial success")
	}
	if err := store.update(func(l *ledger) error {
		if l.Rejected != 1 {
			t.Fatal("rejection not recorded")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityAndConflictingIdentityCannotOverwritePendingData(t *testing.T) {
	store := Store{Root: t.TempDir(), MaxPending: 1}
	e := sampleEvent("one", "obs.sandbox.discovered")
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(sampleEvent("two", "obs.sandbox.destroyed")); !errors.Is(err, ErrFull) {
		t.Fatalf("capacity result: %v", err)
	}
	e.SandboxID = "another"
	if err := store.Apply(e); err == nil {
		t.Fatal("accepted reused event ID")
	}
	if err := store.update(func(l *ledger) error {
		if len(l.Queue) != 1 || l.Queue[0].Record.EventName != "obs.sandbox.discovered" || l.Dropped != 1 {
			t.Fatal("pending lifecycle overwritten")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityOneDefersAssociationUntilLifecycleIsAcknowledged(t *testing.T) {
	store := Store{Root: t.TempDir(), MaxPending: 1}
	if err := store.BindSpans([]model.Span{toolSpan("turn", "call", "d")}); err != nil {
		t.Fatal(err)
	}
	e := sampleEvent("created", "obs.sandbox.created")
	e.SessionID, e.TurnID, e.ToolCallID = "session", "turn", "call"
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	sink := &sink{}
	server := httptest.NewServer(sink.handler(t))
	defer server.Close()
	if err := store.Flush(context.Background(), transport.Client{Config: transport.Config{LogsURL: server.URL + "/otel/v1/logs"}}); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 2 || sink.records[1].EventName != "obs.sandbox.trace.bound" {
		t.Fatal("capacity delayed relation was lost")
	}
}

func TestEventEvidenceValidationAndContentMinimization(t *testing.T) {
	e := sampleEvent("event", "obs.sandbox.discovered")
	r := e.record(time.Now(), nil, false)
	body := r.Body.(map[string]any)
	if body["created_at"] != nil || body["destroyed_at"] != nil {
		t.Fatal("discovery invented lifecycle")
	}
	for _, mutate := range []func(*Event){
		func(e *Event) { e.EventName = "process_exit" },
		func(e *Event) { e.IdentitySource = "" },
		func(e *Event) { e.SandboxID = "" },
		func(e *Event) { e.SessionID = "session" },
		func(e *Event) { e.Timestamp = time.Time{} },
		func(e *Event) { e.EventName = "obs.sandbox.observation.lost"; e.State = "destroyed" },
	} {
		bad := e
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatal("accepted unsupported evidence")
		}
	}
	e.Reason = "Authorization: Bearer sensitive-value"
	r = e.record(time.Now(), nil, true)
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "sensitive-value") {
		t.Fatal("reason leaked a credential")
	}
	r = e.record(time.Now(), nil, false)
	if r.Body.(map[string]any)["reason"] != nil {
		t.Fatal("captureContent=none kept error details")
	}
}

func TestConcurrentProducersPreserveAllEvents(t *testing.T) {
	store := Store{Root: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := sampleEvent(strings.Repeat("x", i+1), "obs.sandbox.snapshot")
			if err := store.Apply(e); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if err := store.update(func(l *ledger) error {
		if len(l.Queue) != 8 {
			t.Fatalf("lost concurrent events: %d", len(l.Queue))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeConsumesStrictEventsAndFlushesOTLP(t *testing.T) {
	sink := &sink{}
	server := httptest.NewServer(sink.handler(t))
	defer server.Close()
	cfg := config.Config{Enabled: true, SandboxEnabled: true, StateDir: t.TempDir(), OtelLogsURL: server.URL + "/otel/v1/logs", CaptureContent: "none", HookLogFile: filepath.Join(t.TempDir(), "hook.jsonl")}
	e := sampleEvent("bridge", "obs.sandbox.snapshot")
	e.State = "running"
	body, _ := json.Marshal(e)
	if err := Run(context.Background(), strings.NewReader(string(body)+"\n"), cfg, nil); err != nil {
		t.Fatal(err)
	}
	if len(sink.records) != 1 || attr(sink.records[0], "source") != Source {
		t.Fatal("bridge did not export sandbox log")
	}
	bad := strings.TrimSuffix(string(body), "}") + `,"trace_id":"native-id"}`
	if err := Run(context.Background(), strings.NewReader(bad+"\n"), cfg, nil); err == nil {
		t.Fatal("accepted arbitrary native trace context")
	}
	log, err := os.ReadFile(cfg.HookLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "native-id") {
		t.Fatal("decoder logged input payload")
	}
}

func TestDisabledBridgeDoesNotReadOrCreateState(t *testing.T) {
	cfg := config.Config{Enabled: true, StateDir: filepath.Join(t.TempDir(), "absent")}
	if err := Run(context.Background(), failReader{t}, cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.StateDir); !os.IsNotExist(err) {
		t.Fatal("disabled bridge touched disk")
	}
	cfg.SandboxEnabled = true
	if err := Run(context.Background(), failReader{t}, cfg, nil); err == nil {
		t.Fatal("invented Logs route")
	}
}

type failReader struct{ t *testing.T }

func (r failReader) Read([]byte) (int, error) {
	r.t.Error("input should not be read")
	return 0, io.EOF
}

func TestRecoveryIsScopedToTheConfirmedDestination(t *testing.T) {
	cfg := config.Config{Enabled: true, SandboxEnabled: true, StateDir: t.TempDir(), Endpoint: "https://trace.example", TracePath: "v1/traces", OtelLogsURL: "https://logs.example/v1/logs", Headers: map[string]string{"X-Token": "fixture-only"}}
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads")}
	claim, err := manager.Claim("session", "turn", "")
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	span := toolSpan("turn", "call", "c")
	if _, err := claim.StabilizeSpans([]model.Span{span}); err != nil {
		t.Fatal(err)
	}
	if err := claim.MarkSignalUploaded("traces", map[string]any{"sandbox_scope": Scope(cfg)}); err != nil {
		t.Fatal(err)
	}
	claim.Release()
	store := NewStore(cfg)
	e := sampleEvent("binding", "obs.sandbox.trace.bound")
	e.SessionID, e.TurnID, e.ToolCallID = "session", "turn", "call"
	if err := store.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindings(cfg, store); err != nil {
		t.Fatal(err)
	}
	if err := store.update(func(l *ledger) error {
		if len(l.Queue) != 1 || l.Queue[0].Record.TraceID != span.TraceID {
			t.Fatal("confirmed binding did not recover")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	other := cfg
	other.Headers = map[string]string{"X-Token": "different-workspace"}
	otherStore := NewStore(other)
	if otherStore.Root == store.Root {
		t.Fatal("credentials did not partition the queue")
	}
	if err := otherStore.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := recoverBindings(other, otherStore); err != nil {
		t.Fatal(err)
	}
	if err := otherStore.update(func(l *ledger) error {
		if len(l.Queue) != 0 || len(l.Bindings) != 0 {
			t.Fatal("old workspace trace binding crossed destinations")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(store.Root, "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "fixture-only") {
		t.Fatal("journal persisted authentication")
	}
}
