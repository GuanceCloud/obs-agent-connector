package hook

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	claudeconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestDisabledShortCircuitsBeforeInput(t *testing.T) {
	called := false
	cfg := testHookConfig(t, "")
	cfg.Enabled = false
	err := RunWithOptions(RunOptions{
		Config: &cfg,
		ReadInput: func() (map[string]any, error) {
			called = true
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("disabled hook read stdin")
	}
}

func TestHookUploadsDecodableTraceAndMetricsOnce(t *testing.T) {
	var traces atomic.Int32
	var metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Content-Encoding") != "gzip" {
			t.Errorf("unexpected content-encoding %q", request.Header.Get("Content-Encoding"))
		}
		body, err := readMaybeGzipBody(request)
		if err != nil {
			t.Error(err)
		}
		switch request.URL.Path {
		case "/v1/traces":
			traces.Add(1)
			decoded, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil {
				t.Errorf("decode traces: %v", err)
				break
			}
			if len(decoded.ResourceSpans) != 1 || len(decoded.ResourceSpans[0].ScopeSpans) != 1 {
				t.Errorf("unexpected traces: %#v", decoded)
				break
			}
			spans := decoded.ResourceSpans[0].ScopeSpans[0].Spans
			if len(spans) != 5 || spans[0].Name != "invoke_agent" {
				t.Errorf("unexpected span tree: %#v", spans)
			}
		case "/v1/metrics":
			metricRequests.Add(1)
			decoded, err := proto.DecodeExportMetricsServiceRequest(body)
			if err != nil {
				t.Errorf("decode metrics: %v", err)
				break
			}
			if len(decoded.ResourceMetrics) != 1 || len(decoded.ResourceMetrics[0].ScopeMetrics) != 1 ||
				len(decoded.ResourceMetrics[0].ScopeMetrics[0].Metrics) == 0 {
				t.Errorf("unexpected metrics: %#v", decoded)
			}
		default:
			t.Errorf("unexpected path: %s", request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transcript := writeHookTranscript(t)
	cfg := testHookConfig(t, server.URL)
	payload := hookPayload(transcript)
	for range 2 {
		if err := RunWithOptions(RunOptions{Config: &cfg, Payload: payload, SkipWait: true}); err != nil {
			t.Fatal(err)
		}
	}
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("requests: traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
}

func TestHookUploadsStopPayloadWhenTranscriptLags(t *testing.T) {
	var traces atomic.Int32
	var metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/traces":
			traces.Add(1)
		case "/v1/metrics":
			metricRequests.Add(1)
		default:
			http.NotFound(writer, request)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user","uuid":"lagged-turn","timestamp":"2026-08-17T08:00:00Z","message":{"content":"summarize"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := testHookConfig(t, server.URL)
	payload := map[string]any{
		"session_id":             "lagged-session",
		"transcript_path":        transcript,
		"hook_event_name":        "Stop",
		"last_assistant_message": "Summary from the Stop payload.",
	}
	if err := RunWithOptions(RunOptions{Config: &cfg, Payload: payload, SkipWait: true}); err != nil {
		t.Fatal(err)
	}
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("requests: traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
	logBody, err := os.ReadFile(cfg.HookLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBody), `"message":"turn uploaded"`) {
		t.Fatalf("missing upload log: %s", logBody)
	}
}

func readMaybeGzipBody(request *http.Request) ([]byte, error) {
	if request.Header.Get("Content-Encoding") != "gzip" {
		return io.ReadAll(request.Body)
	}
	reader, err := gzip.NewReader(request.Body)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func TestHookRetriesOnlyFailedSignal(t *testing.T) {
	var traces atomic.Int32
	var metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/traces":
			traces.Add(1)
			writer.WriteHeader(http.StatusOK)
		case "/v1/metrics":
			count := metricRequests.Add(1)
			if count == 1 {
				http.Error(writer, "try again", http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	transcript := writeHookTranscript(t)
	cfg := testHookConfig(t, server.URL)
	payload := hookPayload(transcript)
	for range 3 {
		if err := RunWithOptions(RunOptions{Config: &cfg, Payload: payload, SkipWait: true}); err != nil {
			t.Fatal(err)
		}
	}
	if traces.Load() != 1 {
		t.Fatalf("trace upload was repeated: %d", traces.Load())
	}
	if metricRequests.Load() != 2 {
		t.Fatalf("metrics requests = %d, want 2", metricRequests.Load())
	}
}

func TestConcurrentHooksClaimTurnOnce(t *testing.T) {
	var traces atomic.Int32
	var metricRequests atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/traces" {
			traces.Add(1)
			once.Do(func() { close(entered) })
			<-release
		} else if request.URL.Path == "/v1/metrics" {
			metricRequests.Add(1)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transcript := writeHookTranscript(t)
	cfg := testHookConfig(t, server.URL)
	payload := hookPayload(transcript)
	errs := make(chan error, 2)
	go func() {
		errs <- RunWithOptions(RunOptions{Config: &cfg, Payload: payload, SkipWait: true})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first hook did not reach receiver")
	}
	go func() {
		errs <- RunWithOptions(RunOptions{Config: &cfg, Payload: payload, SkipWait: true})
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if traces.Load() != 1 || metricRequests.Load() != 1 {
		t.Fatalf("requests: traces=%d metrics=%d", traces.Load(), metricRequests.Load())
	}
}

func testHookConfig(t *testing.T, endpoint string) claudeconfig.Config {
	t.Helper()
	root := t.TempDir()
	return claudeconfig.Config{
		Enabled: true,
		Transport: transport.Config{
			Endpoint:    endpoint,
			TracePath:   "v1/traces",
			MetricsPath: "v1/metrics",
			Timeout:     time.Second,
		},
		ResourceAttributes: map[string]any{"service.name": "gtrace-claude-test"},
		CaptureContent:     "preview",
		MaxChars:           20_000,
		StateDir:           filepath.Join(root, "state"),
		HookLogFile:        filepath.Join(root, "gtrace-hook.log"),
	}
}

func writeHookTranscript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	body := `
{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"list files"}}
{"type":"assistant","timestamp":"2026-06-16T01:00:01Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"tool-1","name":"Bash","input":{"command":"ls"}}],"usage":{"input_tokens":10,"output_tokens":3}}}
{"type":"user","timestamp":"2026-06-16T01:00:02Z","toolUseResult":{"durationMs":200},"message":{"content":[{"type":"tool_result","tool_use_id":"tool-1","content":"README.md"}]}}
{"type":"assistant","timestamp":"2026-06-16T01:00:03Z","message":{"id":"msg-2","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"found README.md"}],"usage":{"input_tokens":4,"output_tokens":5}}}
{"type":"system","subtype":"turn_duration","durationMs":3200}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hookPayload(transcript string) map[string]any {
	return map[string]any{
		"session_id":      "session-1",
		"transcript_path": transcript,
		"hook_event_name": "Stop",
	}
}
