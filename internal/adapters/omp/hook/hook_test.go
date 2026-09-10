package hook

import (
	"compress/gzip"
	"context"
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

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
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
	return config.Config{Enabled: true, MaxChars: 20000, CaptureContent: "preview", StateDir: t.TempDir(), Resource: map[string]any{"agent_name": "test-omp"}}
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

func TestOMPQueueRetriesMetricsOnlyAndDeduplicates(t *testing.T) {
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

func TestOMPDisabledDiscardsSnapshot(t *testing.T) {
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

func TestOMPConcurrentWorkersUploadOnce(t *testing.T) {
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

func TestOMPFailedUploadsAreDiscarded(t *testing.T) {
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
			for _, file := range []string{path, path + ".processing"} {
				if _, err := os.Stat(file); !os.IsNotExist(err) {
					t.Fatal("failed snapshot retained")
				}
			}
			if err := ProcessFile(filepath.Join(cfg.StateDir, "queue", "turn.json"), cfg, server.Client()); err != nil {
				t.Fatal(err)
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

func TestOMPOnlyProcessesSpecifiedFile(t *testing.T) {
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

func TestOMPDeadlineCancelsUpload(t *testing.T) {
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
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("timed out snapshot retained")
	}
}

func TestOMPMalformedSnapshotIsDiscarded(t *testing.T) {
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
