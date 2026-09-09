package hook

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func setup(t *testing.T) (config.Config, parse.HookInput) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "session.jsonl")
	body := `{"id":"u1","timestamp":1784000000000,"type":"message","role":"user","content":"hi"}
{"id":"a1","timestamp":1784000001000,"type":"message","role":"assistant","status":"completed","content":"hello","providerData":{"model":"test-model","usage":{"input_tokens":12,"output_tokens":3}}}
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Resolve(config.ResolveOptions{Home: home, Env: map[string]string{}})
	cfg.Enabled = true
	return cfg, parse.HookInput{SessionID: "session", TranscriptPath: path, Event: "Stop"}
}

func TestUploadRetryDedupAndProtobuf(t *testing.T) {
	cfg, in := setup(t)
	var traces, metrics atomic.Int32
	var payloadError atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader := io.Reader(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			decoded, err := gzip.NewReader(r.Body)
			if err != nil {
				payloadError.Store(true)
				return
			}
			defer decoded.Close()
			reader = decoded
		}
		body, _ := io.ReadAll(reader)
		if r.Header.Get("Content-Type") != "application/x-protobuf" {
			payloadError.Store(true)
		}
		if strings.Contains(r.URL.Path, "metrics") {
			metrics.Add(1)
			if _, err := proto.DecodeExportMetricsServiceRequest(body); err != nil {
				payloadError.Store(true)
			}
			if metrics.Load() == 1 {
				w.WriteHeader(503)
				return
			}
		} else {
			traces.Add(1)
			if _, err := proto.DecodeExportTraceServiceRequest(body); err != nil {
				payloadError.Store(true)
			}
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg.Transport.Endpoint = server.URL
	cfg.Transport.Headers = map[string]string{"X-Token": "test-only"}
	o := RunOptions{Config: cfg, Input: in, SkipWait: true}
	if err := Run(o); err == nil {
		t.Fatal("expected failed metrics upload")
	}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if err := Run(o); err != nil {
		t.Fatal(err)
	}
	if traces.Load() != 1 || metrics.Load() != 2 || payloadError.Load() {
		t.Fatalf("traces=%d metrics=%d protobuf error=%v", traces.Load(), metrics.Load(), payloadError.Load())
	}
	logBody, _ := os.ReadFile(cfg.LogFile)
	if strings.Contains(string(logBody), "test-only") {
		t.Fatal("credentials leaked")
	}
}

func TestLegacyCompletionAndDisabled(t *testing.T) {
	cfg, in := setup(t)
	turns, _, err := parse.Read(in, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyTurnDir(cfg, turns[0])
	os.MkdirAll(legacy, 0700)
	os.WriteFile(filepath.Join(legacy, "completed.json"), []byte(`{}`), 0600)
	cfg.Transport.Endpoint = "http://127.0.0.1:1"
	if err = Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err != nil {
		t.Fatal("replayed legacy completion", err)
	}
	cfg.Enabled = false
	in.TranscriptPath = "/missing"
	if err = Run(RunOptions{Config: cfg, Input: in}); err != nil {
		t.Fatal(err)
	}
}

func TestJournalKeepsMetadataNotContent(t *testing.T) {
	cfg, in := setup(t)
	in.Event = "PreToolUse"
	in.ToolName = "Bash"
	in.ToolInput = map[string]any{"command": "secret body"}
	in.InputKey = parse.ToolKey(in.ToolName, in.ToolInput)
	in.ToolInput = nil
	in.ObservedAt = 123
	if err := record(cfg, in); err != nil {
		t.Fatal(err)
	}
	events := readEvents(cfg, in)
	if len(events) != 1 || events[0].InputKey != in.InputKey {
		t.Fatal("journal mismatch")
	}
	body, _ := json.Marshal(events)
	if strings.Contains(string(body), "secret body") {
		t.Fatal("content persisted")
	}
	other := in
	other.TranscriptPath += "other"
	if len(readEvents(cfg, other)) != 0 {
		t.Fatal("transcript journal mixed")
	}
}

func TestIncompleteTranscriptRetainsQueue(t *testing.T) {
	cfg, in := setup(t)
	os.WriteFile(in.TranscriptPath, []byte(`{"id":"u","timestamp":1784000000000,"type":"message","role":"user","content":"waiting"}`), 0600)
	if err := Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err == nil {
		t.Fatal("incomplete transcript treated as final")
	}
}

func TestBinaryHookWorker(t *testing.T) {
	if testing.Short() {
		t.Skip("binary integration test")
	}
	cfg, in := setup(t)
	binary := filepath.Join(t.TempDir(), "obs-agent-connector")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "../../../../cmd/obs-agent-connector")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	var traces, metricCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "metrics") {
			metricCount.Add(1)
		} else {
			traces.Add(1)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	home := filepath.Dir(cfg.ProfileDir)
	_, err := install.InstallWorkBuddy(install.WorkBuddyOptions{ProfileDir: cfg.ProfileDir, CodeBuddyOptions: install.CodeBuddyOptions{
		Home: home, SourceExecutable: binary, DestinationExecutable: binary, Endpoint: server.URL, InstallType: "gtrace",
	}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(in)
	command := exec.Command(binary, "hook", "workbuddy", "--profile", cfg.ProfileDir)
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH"), "SystemRoot=" + os.Getenv("SystemRoot")}
	command.Stdin = strings.NewReader(string(payload))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("hook: %v: %s", err, output)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		completed, _ := filepath.Glob(filepath.Join(cfg.StateDir, "uploads", "*", "completed.json"))
		queues, _ := filepath.Glob(filepath.Join(cfg.StateDir, "queue", "*.json"))
		if len(completed) == 1 && len(queues) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if traces.Load() != 1 || metricCount.Load() != 1 {
		body, _ := os.ReadFile(cfg.LogFile)
		t.Fatalf("worker uploads: traces=%d metrics=%d log=%s", traces.Load(), metricCount.Load(), body)
	}
}

func TestConcurrentStopUploadsOnce(t *testing.T) {
	cfg, in := setup(t)
	var traces, metricCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "metrics") {
			metricCount.Add(1)
		} else {
			traces.Add(1)
		}
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(200)
	}))
	defer server.Close()
	cfg.Transport.Endpoint = server.URL
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = Run(RunOptions{Config: cfg, Input: in, SkipWait: true}) }()
	}
	wg.Wait()
	if err := Run(RunOptions{Config: cfg, Input: in, SkipWait: true}); err != nil {
		t.Fatal(err)
	}
	if traces.Load() != 1 || metricCount.Load() != 1 {
		t.Fatalf("duplicate uploads: traces=%d metrics=%d", traces.Load(), metricCount.Load())
	}
}
