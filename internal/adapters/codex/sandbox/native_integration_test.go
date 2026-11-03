//go:build linux

package sandbox

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

// Opt-in: exercises the released Codex sandbox helper and the connector CLI.
// It needs no model/API credentials and changes only a private temporary HOME.
func TestNativeCodexSandboxCLIEndToEnd(t *testing.T) {
	connector, codex := os.Getenv("OBS_CONNECTOR_TEST_BINARY"), os.Getenv("OBS_CODEX_TEST_BINARY")
	if connector == "" || codex == "" {
		t.Skip("set OBS_CONNECTOR_TEST_BINARY and OBS_CODEX_TEST_BINARY")
	}
	var mu sync.Mutex
	var logs []otlp.LogRecord
	var traces []otlp.Span
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
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
		b, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/logs":
			req, err := proto.DecodeExportLogsServiceRequest(b)
			if err != nil {
				t.Error(err)
				return
			}
			for _, rl := range req.ResourceLogs {
				if guanceSource(rl.Resource) != Source {
					t.Error("native log source differs")
				}
				for _, sl := range rl.ScopeLogs {
					logs = append(logs, sl.LogRecords...)
				}
			}
		case "/v1/traces":
			req, err := proto.DecodeExportTraceServiceRequest(b)
			if err != nil {
				t.Error(err)
				return
			}
			for _, rs := range req.ResourceSpans {
				for _, ss := range rs.ScopeSpans {
					traces = append(traces, ss.Spans...)
				}
			}
		case "/v1/metrics":
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(home, ".obs-agent-connector", "codex")
	if err := os.MkdirAll(managed, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{"enabled": true, "sandbox": map[string]any{"enabled": true}, "endpoint": server.URL, "tracePath": "v1/traces", "metricsPath": "v1/metrics", "logsPath": "v1/logs", "state_dir": filepath.Join(home, "state"), "fail_on_error": true, "captureContent": "none"}
	body, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(managed, "gtrace.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"), "CODEX_THREAD_ID=obs-native-integration")
	runHook := func(input map[string]any) {
		t.Helper()
		b, _ := json.Marshal(input)
		cmd := exec.Command(connector, "hook", "codex")
		cmd.Env = env
		cmd.Dir = home
		cmd.Stdin = bytes.NewReader(b)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("Hook failed: %v %s", err, out)
		}
	}
	start := time.Now().UTC()
	runHook(map[string]any{"hook_event_name": "PreToolUse", "session_id": "obs-native-integration", "turn_id": "turn-native", "tool_use_id": "call-native", "tool_name": "Bash", "tool_input": map[string]any{"command": "sleep 2"}})
	// Ensure singleton starts before command execution; clean up its exact PID.
	statuses, _ := filepath.Glob(filepath.Join(home, "state", "sandbox", "*", "native", "observer.json"))
	if len(statuses) != 1 {
		t.Fatal("native Hook did not start observer")
	}
	statusBody, _ := os.ReadFile(statuses[0])
	var status observerStatus
	if json.Unmarshal(statusBody, &status) != nil || status.PID == 0 {
		t.Fatal("invalid observer status")
	}
	defer func() {
		p, err := os.FindProcess(status.PID)
		if err == nil {
			_ = p.Signal(syscall.SIGTERM)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(statuses[0]); os.IsNotExist(err) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("observer did not stop within shutdown deadline")
	}()
	command := exec.Command(codex, "sandbox", "-c", `sandbox_mode="read-only"`, "--", "/bin/sh", "-c", "sleep 2")
	command.Env = env
	command.Dir = home
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real Codex sandbox failed: %v %s", err, out)
	}
	runHook(map[string]any{"hook_event_name": "PostToolUse", "session_id": "obs-native-integration", "turn_id": "turn-native", "tool_use_id": "call-native", "tool_name": "Bash", "tool_input": map[string]any{"command": "sleep 2"}})
	// A marked transcript fixture exports real OBS IDs through the normal Stop
	// collector. The runtime evidence itself came from the actual kernel helper.
	var rows []map[string]any
	row := func(at time.Time, kind string, payload map[string]any) {
		rows = append(rows, map[string]any{"timestamp": at.Format(time.RFC3339Nano), "type": kind, "payload": payload})
	}
	row(start, "session_meta", map[string]any{"id": "obs-native-integration", "cli_version": "0.163.0", "source": "cli"})
	row(start.Add(time.Millisecond), "event_msg", map[string]any{"type": "task_started", "turn_id": "turn-native"})
	row(start.Add(2*time.Millisecond), "event_msg", map[string]any{"type": "user_message", "message": "Native sandbox integration fixture"})
	row(start.Add(3*time.Millisecond), "response_item", map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call-native", "arguments": `{"cmd":"sleep 2"}`})
	row(time.Now().Add(-time.Millisecond), "event_msg", map[string]any{"type": "exec_command_end", "call_id": "call-native", "status": "completed", "stdout": ""})
	row(time.Now(), "event_msg", map[string]any{"type": "agent_message", "message": "Native sandbox integration fixture completed"})
	row(time.Now().Add(time.Millisecond), "event_msg", map[string]any{"type": "task_complete", "turn_id": "turn-native"})
	var transcript bytes.Buffer
	for _, r := range rows {
		b, _ := json.Marshal(r)
		transcript.Write(b)
		transcript.WriteByte('\n')
	}
	path := filepath.Join(home, "rollout.jsonl")
	if err := os.WriteFile(path, transcript.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	runHook(map[string]any{"hook_event_name": "Stop", "transcript_path": path})
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(logs)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) != 3 {
		t.Fatalf("expected discovery, terminal snapshot and OBS relation; got %d", len(logs))
	}
	var sandboxID, traceID, spanID string
	discovered, terminal, linked := false, false, false
	for _, r := range logs {
		if attr(r, "obs.collection.source") != "codex.native.linux.proc" || attr(r, "obs.sandbox.type") != "linux-pid-namespace" {
			t.Fatal("not native kernel evidence")
		}
		if attr(r, "gen_ai.conversation.id") != "obs-native-integration" {
			t.Fatal("native session identity was lost")
		}
		id := attr(r, "obs.sandbox.id")
		if sandboxID != "" && id != sandboxID {
			t.Fatal("native identity changed")
		}
		sandboxID = id
		if r.EventName == "obs.sandbox.discovered" {
			discovered = true
		}
		if r.EventName == "obs.sandbox.snapshot" && attr(r, "obs.sandbox.state") == "destroyed" {
			terminal = true
		}
		if r.EventName == "obs.sandbox.trace.bound" {
			if attr(r, "obs.sandbox.association.method") != "native_thread_command_digest" || attr(r, "obs.sandbox.association.confidence") != "inferred" {
				t.Fatal("native correlation was presented as a direct runtime link")
			}
			linked = true
			traceID, spanID = hex.EncodeToString(r.TraceID), hex.EncodeToString(r.SpanID)
		}
		if attr(r, "sandbox_created_at") != "" || attr(r, "sandbox_destroyed_at") != "" {
			t.Fatal("invented native lifecycle timestamps")
		}
	}
	matched := false
	for _, s := range traces {
		if strings.HasPrefix(s.Name, "tool:") && hex.EncodeToString(s.TraceID) == traceID && hex.EncodeToString(s.SpanID) == spanID {
			matched = true
		}
	}
	if !discovered || !terminal || !linked || !matched {
		t.Fatal("native lifecycle or confirmed OBS association missing")
	}
	t.Logf("real Codex namespace observed: %s; %d logs linked to actual exported OBS Tool span", sandboxID, len(logs))
	// Disable stops the resident observer without an external kill.
	cfg["sandbox"] = map[string]any{"enabled": false}
	body, _ = json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(managed, "gtrace.json"), body, 0600); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(statuses[0]); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("disabling native collection left the observer running")
}
