package hook

import (
	"bytes"
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

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func fixtureBody(t *testing.T) []byte {
	t.Helper()
	b, e := os.ReadFile("../parse/testdata/terminal.json")
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func testConfig(t *testing.T) config.Config {
	return config.Config{Enabled: true, MaxChars: 20000, CaptureContent: "preview", StateDir: t.TempDir(), Resource: map[string]any{"agent_name": "test-pi"}}
}

func queueFixture(t *testing.T, cfg config.Config) string {
	t.Helper()
	dir := filepath.Join(cfg.StateDir, "queue")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "turn.json")
	if err := os.WriteFile(path, fixtureBody(t), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func queueNamedFixture(t *testing.T, cfg config.Config, name, sessionID, turnID string) string {
	t.Helper()
	dir := filepath.Join(cfg.StateDir, "queue")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	body := bytes.ReplaceAll(fixtureBody(t), []byte("test-session"), []byte(sessionID))
	body = bytes.ReplaceAll(body, []byte("test-turn"), []byte(turnID))
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPiQueueRetriesMetricsOnlyAndDeduplicates(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	var mu sync.Mutex
	counts := map[string]int{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		counts[r.URL.Path]++
		if r.Header.Get("Content-Type") != "application/x-protobuf" || r.Header.Get("X-Token") != "test-token" {
			t.Error("missing OTLP headers")
		}
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		body, _ := io.ReadAll(gz)
		gz.Close()
		if r.URL.Path == "/traces" {
			decoded, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil {
				t.Error(err)
			} else {
				verifyTraceMessages(t, decoded)
			}
		} else {
			decoded, err := proto.DecodeExportMetricsServiceRequest(body)
			if err != nil {
				t.Error(err)
			}
			raw, _ := json.Marshal(decoded)
			if strings.Contains(string(raw), "test-session") {
				t.Error("session id leaked into metrics")
			}
			if counts[r.URL.Path] == 1 {
				w.WriteHeader(503)
				return
			}
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg.Transport = transport.Config{Endpoint: server.URL, TracePath: "traces", MetricsPath: "metrics", Headers: map[string]string{"X-Token": "test-token"}}
	if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, server.Client()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("processed snapshot retained")
	}

	queueFixture(t, cfg)
	if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, server.Client()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/traces"] != 1 || counts["/metrics"] != 2 {
		t.Fatalf("unexpected uploads: %v", counts)
	}
}

func TestPiDisabledDiscardsSnapshot(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	cfg.Enabled = false
	if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("disabled collector retained snapshot")
	}
}

func TestPiConcurrentWorkersUploadOnce(t *testing.T) {
	cfg := testConfig(t)
	queueFixture(t, cfg)
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg.Transport = transport.Config{Endpoint: server.URL, TracePath: "traces", MetricsPath: "metrics"}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, server.Client()); err != nil && !errors.Is(err, errClaimBusy) {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	mu.Lock()
	defer mu.Unlock()
	if counts["/traces"] != 1 || counts["/metrics"] != 1 {
		t.Fatalf("duplicate uploads: %v", counts)
	}
}

func TestPiDrainQueueProcessesPrimaryAndRetryInOneWorker(t *testing.T) {
	cfg := testConfig(t)
	primary := queueNamedFixture(t, cfg, "primary", "session-primary", "turn-primary")
	retry := queueNamedFixture(t, cfg, "retry", "session-retry", "turn-retry")
	lock := filepath.Join(cfg.StateDir, "pi-worker.lock")
	if err := os.WriteFile(lock, nil, 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	counts := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		counts[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg.Transport = transport.Config{Endpoint: server.URL, TracePath: "traces", MetricsPath: "metrics"}

	if err := DrainQueue(primary, lock, cfg, server.Client()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{primary, retry, lock} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("drain artifact retained: %s", path)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/traces"] != 2 || counts["/metrics"] != 2 {
		t.Fatalf("unexpected drained uploads: %v", counts)
	}
}

func TestPiDrainQueueRejectsUnmanagedLock(t *testing.T) {
	cfg := testConfig(t)
	primary := queueFixture(t, cfg)
	if err := DrainQueue(primary, filepath.Join(t.TempDir(), "other.lock"), cfg, nil); err == nil {
		t.Fatal("accepted unmanaged worker lock")
	}
}

// Assert the encoded OTLP payload rather than only the intermediate Turn.
func verifyTraceMessages(t *testing.T, request otlp.ExportTraceServiceRequest) {
	t.Helper()
	outputs := 0
	for _, resource := range request.ResourceSpans {
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				for _, attr := range span.Attributes {
					if strings.HasPrefix(attr.Key, "gtrace.") {
						t.Errorf("unexpected compatibility alias %s", attr.Key)
					}
					if attr.Key != "gen_ai.input.messages" && attr.Key != "gen_ai.output.messages" {
						continue
					}
					if attr.Key == "gen_ai.output.messages" {
						outputs++
					}
					if attr.Value.ArrayValue == nil || len(attr.Value.ArrayValue.Values) == 0 {
						t.Errorf("%s %s is not a structured message array", span.Name, attr.Key)
						continue
					}
					for _, message := range attr.Value.ArrayValue.Values {
						if message.KVListValue == nil {
							t.Error("message is not an object")
							continue
						}
						role, parts := false, false
						for _, field := range message.KVListValue.Values {
							if field.Key == "role" && field.Value.StringValue != nil {
								role = true
							}
							if field.Key == "parts" && field.Value.ArrayValue != nil {
								parts = true
							}
						}
						if !role || !parts {
							t.Error("message missing role/parts")
						}
					}
				}
			}
		}
	}
	if outputs != 4 {
		t.Errorf("expected root, two LLMs and assistant outputs; got %d", outputs)
	}
}

func TestPiFailedUploadsAreRetained(t *testing.T) {
	for _, status := range []int{401, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := testConfig(t)
			path := queueFixture(t, cfg)
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
			}))
			defer server.Close()
			cfg.Transport = transport.Config{Endpoint: server.URL}
			if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, server.Client()); err == nil {
				t.Fatal("expected upload failure")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal("failed snapshot was not retained")
			}
			want := 1
			if status == 503 {
				want = 3
			}
			if calls != want {
				t.Fatalf("requests = %d, want %d", calls, want)
			}
		})
	}
}

func TestPiPartialUploadRetriesOnlyMissingSignalOnNextRun(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	var mu sync.Mutex
	counts := map[string]int{}
	failMetrics := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		counts[r.URL.Path]++
		if r.URL.Path == "/metrics" && failMetrics {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cfg.Transport = transport.Config{Endpoint: server.URL, TracePath: "traces", MetricsPath: "metrics"}

	if err := ProcessFile(path, cfg, server.Client()); err == nil {
		t.Fatal("expected first metrics upload to fail")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("partially uploaded snapshot was not retained")
	}
	mu.Lock()
	failMetrics = false
	mu.Unlock()
	if err := ProcessFile(path, cfg, server.Client()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("completed snapshot was retained")
	}
	mu.Lock()
	defer mu.Unlock()
	if counts["/traces"] != 1 || counts["/metrics"] != 2 {
		t.Fatalf("unexpected cross-run uploads: %v", counts)
	}
}

func TestPiOnlyProcessesSpecifiedFile(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	other := filepath.Join(filepath.Dir(path), "other.json")
	if err := os.WriteFile(other, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProcessFile(other, cfg, nil); err == nil {
		t.Fatal("expected parse failure")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("unrelated snapshot removed")
	}
}

func TestPiDeadlineCancelsUploadAndRetainsSnapshot(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	cfg.Transport = transport.Config{Endpoint: server.URL}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := processSnapshot(ctx, path, cfg, server.Client()); err == nil {
		t.Fatal("expected deadline")
	}
	if time.Since(started) > time.Second {
		t.Fatal("deadline ignored")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("timed out snapshot was not retained")
	}
}

func TestPiMalformedSnapshotIsDiscarded(t *testing.T) {
	cfg := testConfig(t)
	path := queueFixture(t, cfg)
	if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, nil); err == nil {
		t.Fatal("expected parse failure")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 0 {
		t.Fatal("malformed snapshot retained", err)
	}
}

func TestPiNativeStatusMatchesGTraceAfterEncoding(t *testing.T) {
	for _, failed := range []bool{false, true} {
		snapshot, err := parse.DecodeSnapshot(fixtureBody(t))
		if err != nil {
			t.Fatal(err)
		}
		if failed {
			for i := range snapshot.Events {
				if snapshot.Events[i].Type == "tool_end" {
					snapshot.Events[i].IsError = true
				}
				if snapshot.Events[i].Message.Role == "assistant" {
					snapshot.Events[i].Message.StopReason = "error"
				}
			}
		}
		turn, err := parse.Normalize(snapshot, testConfig(t))
		if err != nil {
			t.Fatal(err)
		}
		spans := buildSpans(turn)
		expected := map[string]int{}
		errorCount := 0
		for _, span := range spans {
			switch span.Attributes["status"] {
			case "ok":
				expected[span.SpanID] = 1
			case "error":
				expected[span.SpanID] = 2
				errorCount++
			default:
				t.Fatalf("unexpected GTrace status: %v", span.Attributes["status"])
			}
		}
		if len(spans) == 0 || (failed && errorCount == 0) {
			t.Fatal("missing status coverage")
		}
		wire := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
		decoded, err := proto.DecodeExportTraceServiceRequest(wire)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, resource := range decoded.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					count++
					if int(span.Status.Code) != expected[hex.EncodeToString(span.SpanID)] {
						t.Fatalf("%s status=%d, want %d", span.Name, span.Status.Code, expected[hex.EncodeToString(span.SpanID)])
					}
				}
			}
		}
		if count != len(spans) {
			t.Fatal("encoded spans missing")
		}
	}
}
