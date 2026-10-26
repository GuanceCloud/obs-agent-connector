package hook

import (
	"compress/gzip"
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/collector"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/sandbox"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestSandboxBindingUsesUploadedTraceAcrossMetricsRetry(t *testing.T) {
	base := t.TempDir()
	traceCount, metricCount := 0, 0
	var actualTrace, actualSpan, linkedTrace, linkedSpan string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		body, err := io.ReadAll(gz)
		gz.Close()
		if err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/v1/traces":
			traceCount++
			request, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil {
				t.Error(err)
				return
			}
			span := request.ResourceSpans[0].ScopeSpans[0].Spans[0]
			actualTrace, actualSpan = hex.EncodeToString(span.TraceID), hex.EncodeToString(span.SpanID)
		case "/v1/metrics":
			metricCount++
			if metricCount == 1 {
				w.WriteHeader(503)
				return
			}
		case "/v1/logs":
			request, err := proto.DecodeExportLogsServiceRequest(body)
			if err != nil {
				t.Error(err)
				return
			}
			record := request.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			if record.EventName == "obs.sandbox.trace.bound" {
				linkedTrace, linkedSpan = hex.EncodeToString(record.TraceID), hex.EncodeToString(record.SpanID)
			}
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	cfg := config.Config{Enabled: true, SandboxEnabled: true, StateDir: filepath.Join(base, "state"), Endpoint: server.URL, LogsPath: "v1/logs"}
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads")}
	uploader := transport.Client{Config: transport.Config{Endpoint: server.URL, TracePath: "v1/traces", MetricsPath: "v1/metrics"}}
	batch := collector.TurnBatch{TurnID: "turn", Fingerprint: "stable", Spans: []model.Span{{Name: "tool:shell", TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("a", 16), DurationMs: 1, Attributes: map[string]any{"gen_ai.conversation.id": "session", "run_id": "turn", "gen_ai.tool.call.id": "call"}}}}
	if err := uploadTurn(cfg, filepath.Join(base, "rollout"), "session", batch, manager, uploader); err == nil {
		t.Fatal("expected initial Metrics failure")
	}
	batch.Spans[0].TraceID = strings.Repeat("b", 32)
	batch.Spans[0].SpanID = strings.Repeat("b", 16)
	if err := uploadTurn(cfg, filepath.Join(base, "rollout"), "session", batch, manager, uploader); err != nil {
		t.Fatal(err)
	}
	if traceCount != 1 || metricCount != 2 {
		t.Fatalf("replayed signals: traces=%d metrics=%d", traceCount, metricCount)
	}
	store := sandbox.NewStore(cfg)
	// Sandbox events may arrive after the turn was already marked complete.
	if err := store.Apply(sandbox.Event{SchemaVersion: 1, EventID: "snapshot", EventName: "obs.sandbox.snapshot", Timestamp: time.Now(), SandboxID: "sbx", ExecutorID: "executor", IdentitySource: "native", RuntimeSource: "fixture-runtime", State: "running", SessionID: "session", TurnID: "turn", ToolCallID: "call"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Flush(context.Background(), sandbox.NewClient(cfg, nil)); err != nil {
		t.Fatal(err)
	}
	if actualTrace != linkedTrace || actualSpan != linkedSpan || actualTrace != strings.Repeat("a", 32) {
		t.Fatal("sandbox linked to regenerated or native trace IDs")
	}
}

func TestNativeToolHookStartsObserverWithoutParsingTranscript(t *testing.T) {
	cfg := config.Config{Enabled: true, SandboxEnabled: true, StateDir: t.TempDir(), Endpoint: "http://127.0.0.1:1", LogsPath: "v1/logs"}
	starts := 0
	input := Input{TranscriptPath: "/missing-rollout", NativeHook: sandbox.NativeHook{Event: "PreToolUse", SessionID: "session", TurnID: "turn", ToolCallID: "call", ToolName: "Bash", ToolInput: []byte(`{"command":"printf synthetic"}`)}}
	if err := RunWithOptions(RunOptions{Config: &cfg, Input: &input, StartObserver: func(config.Config) error { starts++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatal("native Hook did not start observer")
	}
	cfg.SandboxEnabled = false
	if err := RunWithOptions(RunOptions{Config: &cfg, Input: &input, StartObserver: func(config.Config) error { starts++; return nil }}); err != nil {
		t.Fatal(err)
	}
	if starts != 1 {
		t.Fatal("disabled sandbox started observer")
	}
}
