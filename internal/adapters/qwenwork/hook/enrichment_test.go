package hook

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

func TestEnrichmentWireDurationPrivacyAndReplay(t *testing.T) {
	for _, mode := range []string{"off", "on", "none"} {
		t.Run(mode, func(t *testing.T) {
			cfg, in := setup(t)
			cfg.CaptureThinking = mode != "off"
			if mode == "none" {
				cfg.CaptureContent = "none"
			}
			var mu sync.Mutex
			var captured []otlp.Span
			traceCount, metricCount := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader := io.Reader(r.Body)
				if r.Header.Get("Content-Encoding") == "gzip" {
					gz, err := gzip.NewReader(r.Body)
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					defer gz.Close()
					reader = gz
				}
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if strings.Contains(r.URL.Path, "metrics") {
					metricCount++
					if bytes.Contains(body, []byte("excluded reasoning")) {
						t.Error("reasoning leaked into metrics")
					}
					if _, err := proto.DecodeExportMetricsServiceRequest(body); err != nil {
						t.Error(err)
					}
				} else {
					traceCount++
					request, err := proto.DecodeExportTraceServiceRequest(body)
					if err != nil {
						t.Error(err)
						return
					}
					for _, resource := range request.ResourceSpans {
						for _, scope := range resource.ScopeSpans {
							captured = append(captured, scope.Spans...)
						}
					}
				}
			}))
			defer server.Close()
			cfg.Transport.Endpoint = server.URL
			for i := 0; i < 2; i++ {
				if err := Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if traceCount != 1 || metricCount != 1 || len(captured) != 3 {
				t.Fatalf("uploads/spans = %d/%d/%d", traceCount, metricCount, len(captured))
			}
			root, llm, assistant := captured[0], captured[1], captured[2]
			if assistant.Name != "assistant" || assistant.EndTimeUnixNano-assistant.StartTimeUnixNano != uint64(time.Millisecond) {
				t.Fatal("wire assistant duration is not exactly 1 ms")
			}
			if !bytes.Equal(assistant.ParentSpanID, root.SpanID) || assistant.EndTimeUnixNano > root.EndTimeUnixNano {
				t.Fatal("assistant is not contained by root")
			}
			linked := false
			for _, attr := range assistant.Attributes {
				if attr.Key == "triggered_by.llm_span_id" && attr.Value.StringValue != nil {
					linked = *attr.Value.StringValue == hex.EncodeToString(llm.SpanID)
				}
			}
			if !linked {
				t.Fatal("assistant association did not survive stable ID remapping")
			}
			for _, span := range captured {
				encoded, err := json.Marshal(span.Attributes)
				if err != nil {
					t.Fatal(err)
				}
				hasReasoning := bytes.Contains(encoded, []byte("excluded reasoning"))
				if hasReasoning != (mode == "on" && span.Name == "llm") {
					t.Fatalf("reasoning privacy mismatch on %s in mode %s", span.Name, mode)
				}
				if mode == "none" && bytes.Contains(encoded, []byte("PROBE_OK")) {
					t.Fatal("content-none leaked visible output")
				}
			}
			for _, dir := range []string{cfg.StateDir, filepath.Dir(cfg.LogFile)} {
				err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil || entry.IsDir() {
						return walkErr
					}
					body, err := os.ReadFile(path)
					if err == nil && (bytes.Contains(body, []byte("excluded reasoning")) || bytes.Contains(body, []byte("PROBE_OK"))) {
						t.Errorf("content leaked into managed state/log %s", filepath.Base(path))
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
