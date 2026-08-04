package hook

import (
	"bytes"
	"encoding/json"
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
	cfg.LogFile = filepath.Join(stateDir, "hook.log")
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
	logBody, err := os.ReadFile(cfg.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(logBody, []byte("Inspect the synthetic project")) {
		t.Fatalf("log leaked content: %s", logBody)
	}
}

func TestReadInputRejectsUnsupportedHook(t *testing.T) {
	_, err := readInput(bytes.NewBufferString(`{"hook_event_name":"PreToolUse"}`))
	if err == nil {
		t.Fatal("expected unsupported Hook error")
	}
}
