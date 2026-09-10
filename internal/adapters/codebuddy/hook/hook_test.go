package hook

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	codebuddyconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/config"
	codebuddyparse "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func hookFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "parse", "testdata", name, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func telemetryConfig(endpoint, stateDir string) codebuddyconfig.Config {
	return codebuddyconfig.Config{
		Enabled: true, CaptureContent: "none", MaxChars: 4000, StateDir: stateDir,
		TerminalWait: time.Second, ResourceAttributes: map[string]any{},
		Transport: structTransport(endpoint),
	}
}

func structTransport(endpoint string) transport.Config {
	return transport.Config{Endpoint: endpoint, TracePath: "traces", MetricsPath: "metrics", Timeout: time.Second}
}

func TestExportTurnConcurrentUploadsOnce(t *testing.T) {
	var traces, metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("content type=%q", request.Header.Get("Content-Type"))
		}
		switch request.URL.Path {
		case "/traces":
			traces.Add(1)
		case "/metrics":
			metricRequests.Add(1)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := telemetryConfig(server.URL, t.TempDir())
	turns, _, _, err := codebuddyparse.Read(codebuddyparse.HookInput{Event: "Stop", SessionID: "concurrent", GenerationID: "generation-1", TranscriptPath: hookFixture(t, "normal")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() { defer group.Done(); _ = exportTurn(cfg, turns[0], nil) }()
	}
	group.Wait()
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
}

func TestExportTurnRetriesOnlyFailedMetricSignal(t *testing.T) {
	var traces, metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/traces" {
			traces.Add(1)
			writer.WriteHeader(http.StatusOK)
			return
		}
		attempt := metricRequests.Add(1)
		if attempt == 1 {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := telemetryConfig(server.URL, t.TempDir())
	turns, _, _, err := codebuddyparse.Read(codebuddyparse.HookInput{Event: "Stop", SessionID: "partial", GenerationID: "generation-1", TranscriptPath: hookFixture(t, "normal")}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := exportTurn(cfg, turns[0], nil); err == nil {
		t.Fatal("expected first metrics failure")
	}
	if err := exportTurn(cfg, turns[0], nil); err != nil {
		t.Fatal(err)
	}
	if traces.Load() != 1 || metricRequests.Load() != 2 {
		t.Fatalf("traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
}

func TestRunWorkerUploadsAndRemovesQueue(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	stateDir := t.TempDir()
	cfg := telemetryConfig(server.URL, stateDir)
	cfg.HookLogFile = filepath.Join(stateDir, "gtrace-hook.log")
	input := codebuddyparse.HookInput{Event: "Stop", SessionID: "worker", GenerationID: "generation-1", TranscriptPath: hookFixture(t, "normal")}
	body, _ := json.Marshal(input)
	queuePath := filepath.Join(stateDir, "event.json")
	if err := os.WriteFile(queuePath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RunWorker(queuePath, RunOptions{Config: &cfg, SkipWait: true}); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests=%d", requests.Load())
	}
	if _, err := os.Stat(queuePath); !os.IsNotExist(err) {
		t.Fatalf("queue was not removed: %v", err)
	}
	logBody, err := os.ReadFile(cfg.HookLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(logBody, []byte("Inspect the synthetic project")) {
		t.Fatalf("log leaked content: %s", logBody)
	}
	for _, expected := range [][]byte{
		[]byte(`"message":"parsed transcript"`),
		[]byte(`"message":"uploaded spans"`),
		[]byte(`"message":"uploaded metrics"`),
		[]byte(`"status":200`),
	} {
		if !bytes.Contains(logBody, expected) {
			t.Fatalf("missing %s in upload log: %s", expected, logBody)
		}
	}
}

func TestExportTurnUsesGzipUploadByDefault(t *testing.T) {
	var traces, metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Content-Encoding") != "gzip" {
			t.Fatalf("unexpected content-encoding %q", request.Header.Get("Content-Encoding"))
		}
		reader, err := gzip.NewReader(request.Body)
		if err != nil {
			t.Fatalf("new gzip reader: %v", err)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read gzip body: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Fatalf("close gzip reader: %v", err)
		}
		switch request.URL.Path {
		case "/traces":
			traces.Add(1)
			if _, err := proto.DecodeExportTraceServiceRequest(body); err != nil {
				t.Fatalf("decode traces: %v", err)
			}
		case "/metrics":
			metricRequests.Add(1)
			if _, err := proto.DecodeExportMetricsServiceRequest(body); err != nil {
				t.Fatalf("decode metrics: %v", err)
			}
		default:
			t.Fatalf("unexpected path %s", request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := telemetryConfig(server.URL, t.TempDir())
	turns, _, _, err := codebuddyparse.Read(codebuddyparse.HookInput{
		Event:          "Stop",
		SessionID:      "gzip",
		GenerationID:   "generation-1",
		TranscriptPath: hookFixture(t, "normal"),
	}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := exportTurn(cfg, turns[0], nil); err != nil {
		t.Fatal(err)
	}
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
}

func TestReadInputRejectsUnsupportedHook(t *testing.T) {
	_, err := readInput(bytes.NewBufferString(`{"hook_event_name":"PreToolUse"}`))
	if err == nil {
		t.Fatal("expected unsupported Hook error")
	}
}

func TestJSONLWorkerStopAndSessionEndUploadOnce(t *testing.T) {
	var traces, metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer reader.Close()
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/traces":
			traces.Add(1)
			decoded, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil || len(decoded.ResourceSpans) != 1 || len(decoded.ResourceSpans[0].ScopeSpans[0].Spans) != 4 {
				t.Errorf("invalid JSONL traces: %v", err)
			}
		case "/metrics":
			metricRequests.Add(1)
			if _, err := proto.DecodeExportMetricsServiceRequest(body); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg := telemetryConfig(server.URL, t.TempDir())
	cfg.HookLogFile = filepath.Join(cfg.StateDir, "hook.log")
	transcript, err := filepath.Abs(filepath.Join("..", "parse", "testdata", "normal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"Stop", "SessionEnd"} {
		input := codebuddyparse.HookInput{Event: event, SessionID: "jsonl-worker", GenerationID: "generation-1", TranscriptPath: transcript}
		body, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(cfg.StateDir, event+".json")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := RunWorker(path, RunOptions{Config: &cfg, SkipWait: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("queue not removed: %v", err)
		}
	}
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("duplicate upload: traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
	log, err := os.ReadFile(cfg.HookLogFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-secret-value", "Inspect the synthetic project", "npm test"} {
		if bytes.Contains(log, []byte(secret)) {
			t.Fatal("hook log leaked transcript content")
		}
	}
}
