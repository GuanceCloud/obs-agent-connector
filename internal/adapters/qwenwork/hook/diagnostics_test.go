package hook

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/diagnostics"
)

func TestIndependentSignalDiagnosticsAcrossRetry(t *testing.T) {
	cfg, in := setup(t)
	var failures atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/write/otel-metrics" && failures.Add(1) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg.Transport.Endpoint = server.URL
	o := RunOptions{Config: cfg, Input: in, SkipWait: true}
	if err := Run(o); err == nil {
		t.Fatal("expected metrics failure")
	}
	a := diagnostics.Read(cfg.StateDir, time.Now())
	if a["traces"].Outcome != "accepted" || a["metrics"].Outcome != "failed" || a["replay"].Outcome != "export_failed" {
		t.Fatal(a)
	}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	b := diagnostics.Read(cfg.StateDir, time.Now())
	if b["traces"].At != a["traces"].At || b["metrics"].Outcome != "accepted" || b["metrics"].At <= a["metrics"].At {
		t.Fatal(b)
	}
}

func TestDiagnosticsFailOpen(t *testing.T) {
	cfg, in := setup(t)
	if err := os.MkdirAll(cfg.StateDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.StateDir, "diagnostics-v1"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer server.Close()
	cfg.Transport.Endpoint = server.URL
	if err := Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err != nil {
		t.Fatal(err)
	}
}

func TestUsageDiagnosticsDistinguishMissingAndMeasuredZero(t *testing.T) {
	for _, measured := range []bool{false, true} {
		cfg, in := setup(t)
		body, err := os.ReadFile(in.TranscriptPath)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(body), "2026-10-08T06:21:09.521Z", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano))
		if measured {
			text = strings.ReplaceAll(text, `"model":"flash"`, `"model":"flash","usage":{"input_tokens":0}`)
		}
		if err := os.WriteFile(in.TranscriptPath, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
		cfg.Transport.Endpoint = server.URL
		if err := Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err != nil {
			t.Fatal(err)
		}
		server.Close()
		u := diagnostics.Read(cfg.StateDir, time.Now())["usage"]
		if u.Calls != 1 {
			t.Fatal(u)
		}
		if measured && (u.ObservedCalls != 1 || len(u.Fields) != 1 || u.Fields[0] != "input_tokens" || u.SourceAt == 0) {
			t.Fatal(u)
		}
		if !measured && (u.ObservedCalls != 0 || len(u.Fields) != 0 || u.SourceAt != 0) {
			t.Fatal(u)
		}
	}
}
