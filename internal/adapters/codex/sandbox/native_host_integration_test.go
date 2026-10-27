//go:build linux

package sandbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// The model is a local deterministic fixture. Codex itself executes commands,
// emits Hooks, assigns session/turn IDs and writes transcripts. Unlike the
// helper-only test, no Hook input, native context or transcript is fabricated.
func TestNativeCodexRealHostAssociation(t *testing.T) {
	connector, codex := os.Getenv("OBS_CONNECTOR_TEST_BINARY"), os.Getenv("OBS_CODEX_TEST_BINARY")
	if connector == "" || codex == "" {
		t.Skip("set OBS_CONNECTOR_TEST_BINARY and OBS_CODEX_TEST_BINARY")
	}
	var mu sync.Mutex
	var logs []otlp.LogRecord
	var spans []otlp.Span
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
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		if r.URL.Path == "/v1/responses" {
			var request struct {
				Input []struct {
					Type string `json:"type"`
				} `json:"input"`
			}
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			done := false
			for _, item := range request.Input {
				if item.Type == "function_call_output" {
					done = true
				}
			}
			var item map[string]any
			if !done {
				item = map[string]any{"type": "function_call", "id": "fc_fixture", "call_id": "call-native-fixture", "name": "exec_command", "arguments": `{"cmd":"sleep 2","yield_time_ms":5000,"max_output_tokens":64}`}
			} else {
				item = map[string]any{"type": "message", "id": "msg_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Native sandbox fixture completed.", "annotations": []any{}}}}
			}
			response := map[string]any{"id": "resp_fixture", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 5, "output_tokens": 5, "total_tokens": 10, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}
			events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_fixture", "status": "in_progress", "output": []any{}}}, {"type": "response.output_item.added", "output_index": 0, "item": item}, {"type": "response.output_item.done", "output_index": 0, "item": item}, {"type": "response.completed", "response": response}}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range events {
				b, _ := json.Marshal(event)
				fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], b)
			}
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/v1/logs":
			req, err := proto.DecodeExportLogsServiceRequest(body)
			if err != nil {
				t.Error(err)
				return
			}
			for _, resource := range req.ResourceLogs {
				if guanceSource(resource.Resource) != Source {
					t.Error("wrong native log source")
				}
				for _, scope := range resource.ScopeLogs {
					logs = append(logs, scope.LogRecords...)
				}
			}
		case "/v1/traces":
			req, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil {
				t.Error(err)
				return
			}
			for _, resource := range req.ResourceSpans {
				for _, scope := range resource.ScopeSpans {
					spans = append(spans, scope.Spans...)
				}
			}
		case "/v1/metrics":
		default:
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer server.Close()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("model=\"obs-local-fixture\"\nmodel_provider=\"fixture\"\napproval_policy=\"never\"\nsandbox_mode=\"read-only\"\n[features]\ncode_mode=false\n[model_providers.fixture]\nname=\"Local fixture\"\nbase_url=%q\nwire_api=\"responses\"\nrequires_openai_auth=false\n", server.URL+"/v1")
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(toml), 0600); err != nil {
		t.Fatal(err)
	}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "HOME" || strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OBS_") || strings.HasPrefix(key, "GTRACE_") || strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "OTEL_") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "HOME="+home, "CODEX_HOME="+codexHome)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	run := func(executable string, args ...string) error {
		cmd := exec.CommandContext(ctx, executable, args...)
		cmd.Env = env
		cmd.Dir = home
		cmd.Stdin = bytes.NewReader(nil)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", filepath.Base(executable), err, out)
		}
		return nil
	}
	if err := run(connector, "install", "codex", "--yes", "--endpoint", server.URL, "--x-token", "fixture", "--sandbox-enabled=true", "--logs-path=v1/logs", "--trace-path=v1/traces", "--metrics-path=v1/metrics", "--capture-content=none"); err != nil {
		t.Fatal(err)
	}
	// Reproduce the observed installation drift: only Stop remains registered.
	// Normal update must restore native Hooks without rewriting runtime settings.
	configPath := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	hooksPath := filepath.Join(codexHome, "hooks.json")
	hooksBody, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(hooksBody, &hooks); err != nil {
		t.Fatal(err)
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(hooks["hooks"], &events); err != nil {
		t.Fatal(err)
	}
	for event := range events {
		if event != "Stop" {
			delete(events, event)
		}
	}
	hooks["hooks"], _ = json.Marshal(events)
	hooksBody, _ = json.Marshal(hooks)
	if err := os.WriteFile(hooksPath, hooksBody, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(connector, "update", "codex", "--yes"); err != nil {
		t.Fatal(err)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil || !bytes.Equal(configBefore, configAfter) {
		t.Fatal("update changed runtime configuration")
	}
	defer func() {
		files, _ := filepath.Glob(filepath.Join(codexHome, "state", "gtrace-agent", "sandbox", "*", "native", "observer.json"))
		for _, file := range files {
			body, _ := os.ReadFile(file)
			var status observerStatus
			if json.Unmarshal(body, &status) == nil && status.PID > 0 {
				p, _ := os.FindProcess(status.PID)
				_ = p.Signal(syscall.SIGTERM)
			}
		}
	}()
	// Identical commands and identical call IDs in two concurrent sessions must
	// still resolve to their own actual OBS Trace/Tool span.
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			results <- run(codex, "exec", "--skip-git-repo-check", "-C", home, "--json", "Run the native sandbox fixture command and finish.")
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := 0
		for _, r := range logs {
			if r.EventName == "obs.sandbox.trace.bound" {
				count++
			}
		}
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	traceIDs, sessions, sandboxIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	bindings := 0
	for _, r := range logs {
		if r.EventName != "obs.sandbox.trace.bound" {
			continue
		}
		bindings++
		if attr(r, "obs.sandbox.association.method") != "native_process_tool_id" || attr(r, "obs.sandbox.association.confidence") != "observed" {
			t.Fatal("real host did not use observed native Tool ID")
		}
		if attr(r, "gen_ai.tool.call.id") != "call-native-fixture" {
			t.Fatal("unexpected runtime call identity")
		}
		found := false
		for _, s := range spans {
			if strings.HasPrefix(s.Name, "tool:") && bytes.Equal(s.TraceID, r.TraceID) && bytes.Equal(s.SpanID, r.SpanID) {
				values := map[string]string{}
				for _, a := range s.Attributes {
					if a.Value.StringValue != nil {
						values[a.Key] = *a.Value.StringValue
					}
				}
				found = values["gen_ai.conversation.id"] == attr(r, "gen_ai.conversation.id") && values["run_id"] != "" && values["run_id"] == attr(r, "run_id") && values["gen_ai.tool.call.id"] == attr(r, "gen_ai.tool.call.id")
			}
		}
		if !found {
			t.Fatal("sandbox binding did not match an uploaded Tool span in the same session")
		}
		traceIDs[hex.EncodeToString(r.TraceID)] = true
		sessions[attr(r, "gen_ai.conversation.id")] = true
		sandboxIDs[attr(r, "obs.sandbox.id")] = true
	}
	if bindings != 2 || len(traceIDs) != 2 || len(sessions) != 2 || len(sandboxIDs) != 2 {
		t.Fatalf("bindings=%d traces=%d sessions=%d sandboxes=%d logs=%d spans=%d", bindings, len(traceIDs), len(sessions), len(sandboxIDs), len(logs), len(spans))
	}
	t.Logf("two real Codex sessions: distinct sandboxes and OBS Traces, native Tool IDs, automatic Hooks/transcripts, %d lifecycle/association logs", len(logs))
}
