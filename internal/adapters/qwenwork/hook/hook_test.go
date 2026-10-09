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

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func setup(t *testing.T) (config.Config, parse.HookInput) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "session.jsonl")
	bodyBytes, err := os.ReadFile("../parse/testdata/personal-1.2.5.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Join(strings.Split(string(bodyBytes), "\n")[:3], "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Resolve(config.ResolveOptions{Home: home, Env: map[string]string{}})
	cfg.Enabled = true
	return cfg, parse.HookInput{SessionID: "session", TranscriptPath: path, Event: "Stop", ParentRequestSetID: "turn-1", ObservedAt: 1791440470000000000}
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

func TestJournalKeepsMetadataNotContent(t *testing.T) {
	cfg, in := setup(t)
	in.Event = "PreToolUse"
	in.ToolName = "Bash"
	in.ToolInput = map[string]any{"command": "secret body"}
	in.InputKey = parse.ToolKey(in.ToolName, in.ToolInput)
	in.ToolInput = nil
	in.Reason = "secret body"
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

func TestQueueCoalescingPreservesTerminalEvidence(t *testing.T) {
	cfg, in := setup(t)
	var paths []string
	for i := 0; i < 4; i++ {
		in.ParentRequestSetID = strings.Repeat("turn", i+1)
		path, err := writeEvent(filepath.Join(cfg.StateDir, "queue"), in)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	if got := coalesceQueues(cfg, paths); len(got) != 1 {
		t.Fatal("redundant retries retained")
	}
	if got := readEvents(cfg, in); len(got) != 3 {
		t.Fatal("removed queues lost Stop evidence")
	}
}

func TestIncompleteTranscriptRetainsQueue(t *testing.T) {
	cfg, in := setup(t)
	os.WriteFile(in.TranscriptPath, []byte(`{"uuid":"u","requestSetId":"turn-1","timestamp":1784000000000,"type":"user","humanInput":{"text":"waiting"}}`), 0600)
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
	_, err := install.InstallQwenWork(install.QwenWorkOptions{ProfileDir: cfg.ProfileDir, CodeBuddyOptions: install.CodeBuddyOptions{
		Home: home, SourceExecutable: binary, DestinationExecutable: binary, Endpoint: server.URL, InstallType: "gtrace",
	}})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(in)
	command := exec.Command(binary, "hook", "qwenwork-cn", "--profile", cfg.ProfileDir)
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

func TestStableIdentitiesAndNoneStructure(t *testing.T) {
	cfg, in := setup(t)
	cfg.CaptureContent = "none"
	turns, _, err := parse.Read(in, cfg, nil)
	if err != nil || len(turns) != 1 {
		t.Fatal(err)
	}
	a := (semantic.Builder{}).Build(turns[0])
	b := (semantic.Builder{}).Build(turns[0])
	stableSpanIDs(a, turns[0])
	stableSpanIDs(b, turns[0])
	if len(a) != 3 {
		t.Fatalf("wanted root, LLM and assistant: %d", len(a))
	}
	for i := range a {
		if a[i].TraceID != b[i].TraceID || a[i].SpanID != b[i].SpanID {
			t.Fatal("unstable IDs")
		}
		if i > 0 && a[i].ParentID != a[0].SpanID {
			t.Fatal("broken root link")
		}
		if a[i].Attributes["gen_ai.input.messages"] != nil || a[i].Attributes["gen_ai.output.messages"] != nil {
			t.Fatal("none content leak")
		}
	}
}
