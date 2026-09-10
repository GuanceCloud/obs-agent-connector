package exporter

import (
	"compress/gzip"
	"encoding/json"
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

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestRetryOnlyFailedSignalAndConcurrentDuplicate(t *testing.T) {
	var traces, metrics atomic.Int32
	var failMetrics atomic.Bool
	failMetrics.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		defer reader.Close()
		body, _ := io.ReadAll(reader)
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			t.Error("incorrect content type")
		}
		switch r.URL.Path {
		case "/v1/traces":
			traces.Add(1)
			p, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil || len(p.ResourceSpans) == 0 {
				t.Error("invalid trace protobuf")
			}
		case "/v1/metrics":
			metrics.Add(1)
			p, err := proto.DecodeExportMetricsServiceRequest(body)
			decoded, _ := json.Marshal(p)
			if !strings.Contains(string(decoded), "gen_ai.client.operation.time_to_first_chunk") {
				t.Error("first-chunk metric lost during retry")
			}
			if err != nil || len(p.ResourceMetrics) == 0 {
				t.Error("invalid metric protobuf")
			}
			if failMetrics.Load() {
				w.WriteHeader(503)
				return
			}
		default:
			t.Error("unexpected path")
		}
	}))
	defer server.Close()
	root := t.TempDir()
	e := Exporter{Root: root, LogFile: filepath.Join(root, "hooks.json"), Transport: transport.Config{Endpoint: server.URL, TracePath: "v1/traces", MetricsPath: "v1/metrics", Timeout: time.Second}, Builder: semantic.Builder{ScopeName: "test"}}
	turn := model.Turn{SessionID: "session", TurnID: "run", AgentRuntime: "openclaw", FinalStatus: model.FinalStatusCompleted, StartUnixNano: 1000000000, EndUnixNano: 2000000000, InputPreview: "hello", OutputPreview: "done"}
	latency := 125.0
	turn.LLMCalls = []model.LLMCall{{CallID: "call", Provider: "test", RequestModel: "small", Status: "ok", StartUnixNano: 1000000000, EndUnixNano: 2000000000, FirstChunkMs: &latency}}
	if err := e.Enqueue(turn); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(); err == nil {
		t.Fatal("expected failed metrics")
	}
	if traces.Load() != 1 || metrics.Load() != 1 {
		t.Fatal("unexpected initial counts")
	}
	// A new exporter instance simulates a process restart.
	resumed := e
	failMetrics.Store(false)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := resumed.Enqueue(turn); err != nil {
				t.Error(err)
			}
			if err := resumed.Flush(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if traces.Load() != 1 || metrics.Load() != 2 {
		t.Fatalf("duplicate uploads: %d traces, %d metrics", traces.Load(), metrics.Load())
	}
	if err := e.Enqueue(turn); err != nil {
		t.Fatal(err)
	}
	if err := e.Flush(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "pending"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("pending spool not cleaned: %v %v", entries, err)
	}
	if traces.Load() != 1 || metrics.Load() != 2 {
		t.Fatal("completed turn uploaded again")
	}
	logs, err := os.ReadFile(e.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	for message, want := range map[string]int{"uploaded spans": 1, "uploaded metrics": 1, "upload failed": 1} {
		if got := strings.Count(string(logs), `"message":"`+message+`"`); got != want {
			t.Fatalf("%s logged %d times, want %d", message, got, want)
		}
	}
	if !strings.Contains(string(logs), `"status":503`) {
		t.Fatal("missing failure status")
	}
}
