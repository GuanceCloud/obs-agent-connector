package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestBindingCacheRotatesInsteadOfBlockingLaterTurns(t *testing.T) {
	now := time.Now()
	s := Store{Root: t.TempDir(), MaxPending: 1, Now: func() time.Time { return now }}
	for i := 0; i < 20; i++ {
		now = now.Add(time.Second)
		if err := s.BindSpans([]model.Span{toolSpan(fmt.Sprint(i), fmt.Sprint(i), "a")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.access(func(l *ledger) error {
		if len(l.Bindings) != 2 || l.Bindings[digest("session", "19", "19")].TraceID == "" {
			t.Fatal("warm cache did not rotate")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
}

func TestBindingCachePreservesAnUnresolvedRelation(t *testing.T) {
	s := Store{Root: t.TempDir(), MaxPending: 1}
	e := sampleEvent("pending", "obs.sandbox.created")
	e.SessionID, e.TurnID, e.ToolCallID = "session", "protected", "call"
	if err := s.Apply(e); err != nil {
		t.Fatal(err)
	}
	// The lifecycle fills the queue; its relation cannot yet be queued.
	if err := s.BindSpans([]model.Span{toolSpan("protected", "call", "b")}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if err := s.BindSpans([]model.Span{toolSpan(fmt.Sprint(i), fmt.Sprint(i), "c")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.access(func(l *ledger) error {
		if l.Bindings[digest("session", "protected", "call")].TraceID == "" {
			t.Fatal("evicted a binding required by an unresolved relation")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
}

func TestNativeEvidenceLifecycleAndCausalBinding(t *testing.T) {
	now := time.Now().UTC()
	s := Store{Root: t.TempDir(), Now: func() time.Time { return now }}
	input := NativeHook{Event: "PreToolUse", SessionID: "session", TurnID: "turn", ToolCallID: "call", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"printf fixture-token=secret"}`)}
	if err := RecordNativeHook(s, input); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	instance := nativeInstance{ID: strings.Repeat("a", 64), ExecutorID: "executor", ThreadID: "session", CommandHash: digest("shell", "printf fixture-token=secret"), PID: 123, StartTicks: "42"}
	if err := reconcileNative(s, []nativeInstance{instance}, func(nativeInstance) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSpans([]model.Span{toolSpan("turn", "call", "b")}); err != nil {
		t.Fatal(err)
	}
	// Restart observes the same instance; discovery and association are not replayed.
	now = now.Add(time.Second)
	if err := reconcileNative(s, []nativeInstance{instance}, func(nativeInstance) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := reconcileNative(s, nil, func(nativeInstance) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	receiver := &sink{}
	server := httptest.NewServer(receiver.handler(t))
	defer server.Close()
	if err := s.Flush(context.Background(), transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs"}}); err != nil {
		t.Fatal(err)
	}
	if len(receiver.records) != 3 {
		t.Fatalf("expected discovery, relation and terminal snapshot; got %d", len(receiver.records))
	}
	for _, r := range receiver.records {
		if attr(r, "obs.collection.source") != "codex.native.linux.proc" || attr(r, "obs.sandbox.identity_source") != "observer" {
			t.Fatal("lost observer provenance")
		}
		if attr(r, "sandbox_created_at") != "" || attr(r, "sandbox_destroyed_at") != "" {
			t.Fatal("invented native lifecycle dates")
		}
	}
	if attr(receiver.records[2], "obs.sandbox.state") != "destroyed" {
		t.Fatal("confirmed namespace-init exit did not produce a terminal snapshot")
	}
	// Neither the private command nor its token is persisted in the native state.
	body, err := os.ReadFile(filepath.Join(s.Root, "native", "journal.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "fixture-token") || strings.Contains(string(body), "printf") {
		t.Fatal("native state persisted a command")
	}
}

func TestNativeExactCommandAmbiguityAndObservationLoss(t *testing.T) {
	now := time.Now()
	s := Store{Root: t.TempDir(), Now: func() time.Time { return now }}
	for _, call := range []string{"one", "two"} {
		if err := RecordNativeHook(s, NativeHook{Event: "PreToolUse", SessionID: "session", TurnID: "turn", ToolCallID: call, ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"same"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(time.Second)
	instance := nativeInstance{ID: strings.Repeat("c", 64), ExecutorID: "executor", ThreadID: "session", CommandHash: digest("shell", "same")}
	if err := reconcileNative(s, []nativeInstance{instance}, func(nativeInstance) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.access(func(l *ledger) error {
		if len(l.Relations) != 0 {
			t.Fatal("guessed a link between concurrent identical commands")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
	if err := reconcileNative(s, nil, func(nativeInstance) (bool, error) { return false, fmt.Errorf("permission denied") }); err != nil {
		t.Fatal(err)
	}
	if err := s.access(func(l *ledger) error {
		if len(l.Queue) != 2 || l.Queue[1].Record.EventName != "obs.sandbox.observation.lost" {
			t.Fatal("inaccessible process was treated as destruction")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOutboxRecoversAfterSandboxQueueIsFull(t *testing.T) {
	s := Store{Root: t.TempDir(), MaxPending: 1}
	if err := s.Apply(sampleEvent("existing", "obs.sandbox.created")); err != nil {
		t.Fatal(err)
	}
	instance := nativeInstance{ID: strings.Repeat("d", 64), ExecutorID: "executor"}
	if err := reconcileNative(s, []nativeInstance{instance}, func(nativeInstance) (bool, error) { return true, nil }); err != ErrFull {
		t.Fatalf("expected backpressure, got %v", err)
	}
	var event Event
	if err := nativeFile(s).View(func(n *nativeState) error {
		if len(n.Pending) != 1 {
			t.Fatal("producer event was not durable")
		}
		event = n.Pending[0]
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.update(func(l *ledger) error { l.Queue = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := drainNative(s); err != nil {
		t.Fatal(err)
	}
	if err := s.access(func(l *ledger) error {
		r := l.Queue[0].Record
		if r.TimeUnixNano != uint64(event.Timestamp.UnixNano()) {
			t.Fatal("outbox retry changed event time")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalHookClosesOnlyItsOwnToolContexts(t *testing.T) {
	store := Store{Root: t.TempDir()}
	for _, turn := range []string{"one", "two"} {
		if err := RecordNativeHook(store, NativeHook{Event: "PreToolUse", SessionID: "session", TurnID: turn, ToolCallID: turn, ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"same"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := RecordNativeHook(store, NativeHook{Event: "Stop", SessionID: "session", TurnID: "one"}); err != nil {
		t.Fatal(err)
	}
	if err := nativeFile(store).View(func(s *nativeState) error {
		if s.Tools[0].Ended.IsZero() || !s.Tools[1].Ended.IsZero() {
			t.Fatal("terminal Hook crossed a turn boundary")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestObservedSessionDoesNotInventToolAssociation(t *testing.T) {
	s := Store{Root: t.TempDir()}
	e := sampleEvent("observed-session", "obs.sandbox.discovered")
	e.ObservedSessionID = "known-session"
	if err := s.Apply(e); err != nil {
		t.Fatal(err)
	}
	if err := s.access(func(l *ledger) error {
		if len(l.Relations) != 0 || l.Queue[0].Record.Attributes["gen_ai.conversation.id"] != "known-session" || l.Queue[0].Record.TraceID != "" {
			t.Fatal("session context was lost or converted into a guessed Tool binding")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
	e.SessionID, e.TurnID, e.ToolCallID = "different-session", "turn", "call"
	if e.Validate() == nil {
		t.Fatal("accepted inconsistent runtime and Tool session identities")
	}
}

func TestNativeToolIDDisambiguatesConcurrentIdenticalCommands(t *testing.T) {
	now := time.Now().UTC()
	tools := []nativeTool{{SessionID: "session", TurnID: "turn", CallID: "first", CommandHash: digest("shell", "same"), Started: now.Add(-time.Second)}, {SessionID: "session", TurnID: "turn", CallID: "second", CommandHash: digest("shell", "same"), Started: now.Add(-time.Second)}}
	i := nativeInstance{ThreadID: "session", ToolCallID: "second", CommandHash: digest("shell", "different"), FirstObserved: now}
	if tool := matchingTool(tools, i); tool == nil || tool.CallID != "second" {
		t.Fatal("exact native Tool ID did not disambiguate commands")
	}
	i.ToolCallID = "missing"
	if matchingTool(tools, i) != nil {
		t.Fatal("unknown native Tool ID fell back to a guessed command")
	}
	i.ToolCallID = "second"
	i.ContextConflict = true
	if matchingTool(tools, i) != nil {
		t.Fatal("conflicting runtime metadata was associated")
	}
}

func TestNativeContextEnrichmentAndBindingAfterExit(t *testing.T) {
	now := time.Now().UTC()
	s := Store{Root: t.TempDir(), Now: func() time.Time { return now }}
	if err := RecordNativeHook(s, NativeHook{Event: "PreToolUse", SessionID: "session", TurnID: "turn", ToolCallID: "call", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"same"}`)}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	i := nativeInstance{ID: strings.Repeat("e", 64), ExecutorID: "executor", CommandHash: digest("shell", "same")}
	if err := reconcileNative(s, []nativeInstance{i}, func(nativeInstance) (bool, error) { return true, nil }); err != nil {
		t.Fatal(err)
	}
	i.ThreadID, i.ToolCallID = "session", "call"
	now = now.Add(time.Second)
	if err := reconcileNative(s, []nativeInstance{i}, func(nativeInstance) (bool, error) { return false, nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.BindSpans([]model.Span{toolSpan("turn", "call", "b")}); err != nil {
		t.Fatal(err)
	}
	if err := s.access(func(l *ledger) error {
		if len(l.Relations) != 1 || !l.Relations[0].Bound {
			t.Fatal("late context did not bind the completed instance")
		}
		r := l.Relations[0].Record
		if r.Attributes["obs.sandbox.association.method"] != "native_process_tool_id" || r.Attributes["obs.sandbox.association.confidence"] != "observed" {
			t.Fatal("native evidence was mislabeled")
		}
		return nil
	}, false); err != nil {
		t.Fatal(err)
	}
}
