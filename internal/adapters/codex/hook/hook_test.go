package hook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/ingest"
)

func TestRunWithOptionsUploadsTraceAndMetricsAndMarksSidecar(t *testing.T) {
	base := t.TempDir()
	var mu sync.Mutex
	requests := make([]string, 0, 2)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.Path)
		mu.Unlock()
		if r.Header.Get("content-type") != "application/x-protobuf" {
			t.Fatalf("unexpected content-type: %s", r.Header.Get("content-type"))
		}
		if r.URL.Path == "/v1/write/otel-llm" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		if r.URL.Path == "/v1/write/otel-metrics" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	rollout := filepath.Join(base, "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(joinJSONLines(
		row("2026-07-24T08:00:00Z", "session_meta", map[string]any{
			"id":             "sess-hook",
			"cli_version":    "0.145.0",
			"model_provider": "openai",
		}),
		row("2026-07-24T08:00:01Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-hook",
		}),
		row("2026-07-24T08:00:02Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "hello",
		}),
		row("2026-07-24T08:00:03Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "world"},
			},
		}),
		row("2026-07-24T08:00:04Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  11,
					"output_tokens": 7,
				},
			},
		}),
		row("2026-07-24T08:00:05Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	)), 0o644); err != nil {
		t.Fatal(err)
	}

	logFile := filepath.Join(base, "gtrace-hook.log")
	cfg := config.Config{
		Enabled:            true,
		Endpoint:           server.URL,
		TracePath:          "v1/write/otel-llm",
		MetricsPath:        "v1/write/otel-metrics",
		TimeoutMs:          5000,
		MaxChars:           4096,
		HookLogFile:        logFile,
		StateDir:           filepath.Join(base, "state"),
		LockStaleMs:        1000,
		Headers:            map[string]string{"X-Token": "agent-test"},
		ResourceAttributes: map[string]any{},
	}

	if err := RunWithOptions(RunOptions{
		Config: &cfg,
		Input:  &Input{TranscriptPath: rollout},
	}); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(gotRequests) != 2 || gotRequests[0] != "/v1/write/otel-llm" || gotRequests[1] != "/v1/write/otel-metrics" {
		t.Fatalf("unexpected upload requests: %#v", gotRequests)
	}

	sidecarBody, err := os.ReadFile(rollout + ".gtrace")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(sidecarBody), "turn-hook\t") {
		t.Fatalf("expected sidecar to contain uploaded turn marker, got %q", string(sidecarBody))
	}

	logBody, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(logBody), `"message":"uploaded spans"`) || !contains(string(logBody), `"message":"uploaded metrics"`) {
		t.Fatalf("expected upload log lines, got %s", string(logBody))
	}
}

func TestRunWithOptionsSkipsDisabledHook(t *testing.T) {
	base := t.TempDir()
	logFile := filepath.Join(base, "gtrace-hook.log")
	cfg := config.Config{
		Enabled:     false,
		HookLogFile: logFile,
	}
	if err := RunWithOptions(RunOptions{Config: &cfg, Input: &Input{TranscriptPath: filepath.Join(base, "missing.jsonl")}}); err != nil {
		t.Fatal(err)
	}
	logBody, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(logBody), "gtrace disabled") {
		t.Fatalf("expected disabled log line, got %s", string(logBody))
	}
}

func TestRunCLIWithOptionsDoesNotFailCodexByDefault(t *testing.T) {
	base := t.TempDir()
	logFile := filepath.Join(base, "gtrace-hook.log")
	cfg := config.Config{
		Enabled:     true,
		Debug:       false,
		FailOnError: false,
		HookLogFile: logFile,
		LockStaleMs: 120_000,
	}

	exitCode := RunCLIWithOptions(RunOptions{
		Config: &cfg,
		Input:  &Input{TranscriptPath: filepath.Join(base, "missing-rollout.jsonl")},
	})
	if exitCode != 0 {
		t.Fatalf("expected default hook failure exit code 0, got %d", exitCode)
	}

	body, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(body), `"message":"failed"`) || !contains(string(body), `"phase":"runHook"`) {
		t.Fatalf("expected failure details in hook log, got %s", string(body))
	}
}

func TestRunCLIWithOptionsHonorsFailOnError(t *testing.T) {
	base := t.TempDir()
	cfg := config.Config{
		Enabled:     true,
		Debug:       false,
		FailOnError: true,
		HookLogFile: filepath.Join(base, "gtrace-hook.log"),
		LockStaleMs: 120_000,
	}

	exitCode := RunCLIWithOptions(RunOptions{
		Config: &cfg,
		Input:  &Input{TranscriptPath: filepath.Join(base, "missing-rollout.jsonl")},
	})
	if exitCode != 1 {
		t.Fatalf("expected fail_on_error hook exit code 1, got %d", exitCode)
	}
}

func TestRunWithOptionsUsesBasicAuthWhenPublicAndSecretKeysAreSet(t *testing.T) {
	base := t.TempDir()
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	rollout := filepath.Join(base, "rollout.jsonl")
	if err := os.WriteFile(rollout, []byte(joinJSONLines(
		row("2026-07-24T08:10:00Z", "session_meta", map[string]any{"id": "sess-auth"}),
		row("2026-07-24T08:10:01Z", "event_msg", map[string]any{"type": "task_started", "turn_id": "turn-auth"}),
		row("2026-07-24T08:10:02Z", "event_msg", map[string]any{"type": "user_message", "message": "hello"}),
		row("2026-07-24T08:10:03Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "world"},
			},
		}),
		row("2026-07-24T08:10:04Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
			},
		}),
		row("2026-07-24T08:10:05Z", "event_msg", map[string]any{"type": "task_complete"}),
	)), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Enabled:            true,
		Endpoint:           server.URL,
		TracePath:          "",
		MetricsPath:        "",
		TimeoutMs:          5000,
		MaxChars:           4096,
		HookLogFile:        filepath.Join(base, "hook.log"),
		StateDir:           filepath.Join(base, "state"),
		LockStaleMs:        1000,
		PublicKey:          "pk-test",
		SecretKey:          "sk-test",
		ResourceAttributes: map[string]any{},
	}
	if err := RunWithOptions(RunOptions{
		Config: &cfg,
		Input:  &Input{TranscriptPath: rollout},
	}); err != nil {
		t.Fatal(err)
	}
	if authHeader == "" || !contains(authHeader, "Basic ") {
		t.Fatalf("expected basic auth header, got %q", authHeader)
	}
}

func TestRunWithOptionsUploadsToGoIngestServerEndToEnd(t *testing.T) {
	base := t.TempDir()
	dataDir := filepath.Join(base, "data")
	server := httptest.NewServer(ingest.NewHandler(ingest.ServerOptions{
		DataDir:   dataDir,
		PublicKey: "pk-test",
		SecretKey: "sk-test",
	}))
	defer server.Close()

	rollout := filepath.Join(base, "rollout-e2e.jsonl")
	if err := os.WriteFile(rollout, []byte(joinJSONLines(
		row("2026-07-24T09:00:00Z", "session_meta", map[string]any{
			"id":             "sess-go-e2e",
			"cli_version":    "0.145.0",
			"model_provider": "openai",
		}),
		row("2026-07-24T09:00:01Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-go-e2e",
		}),
		row("2026-07-24T09:00:02Z", "turn_context", map[string]any{
			"model": "gpt-5.4",
		}),
		row("2026-07-24T09:00:03Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "hello",
		}),
		row("2026-07-24T09:00:04Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "world"},
			},
		}),
		row("2026-07-24T09:00:05Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  11,
					"output_tokens": 7,
				},
			},
		}),
		row("2026-07-24T09:00:06Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	)), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Enabled:            true,
		Endpoint:           server.URL,
		TracePath:          "api/public/otel/v1/traces",
		MetricsPath:        "api/public/otel/v1/metrics",
		TimeoutMs:          5000,
		MaxChars:           4096,
		HookLogFile:        filepath.Join(base, "hook-e2e.log"),
		StateDir:           filepath.Join(base, "state"),
		LockStaleMs:        1000,
		PublicKey:          "pk-test",
		SecretKey:          "sk-test",
		ResourceAttributes: map[string]any{"app_id": "codex-monitor"},
	}

	if err := RunWithOptions(RunOptions{
		Config: &cfg,
		Input:  &Input{TranscriptPath: rollout},
	}); err != nil {
		t.Fatal(err)
	}

	store := ingest.NewFileStore(dataDir)
	spans, err := store.ListSpans(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 {
		t.Fatalf("expected 3 stored spans, got %d", len(spans))
	}
	if spans[0].TraceID == "" || spans[0].GTrace.Trace.SessionID != "sess-go-e2e" {
		t.Fatalf("unexpected stored root span: %#v", spans[0])
	}
	metrics, err := store.ListMetrics(20)
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) == 0 {
		t.Fatal("expected stored metrics")
	}
	foundWorkflow := false
	foundInputTokens := false
	for _, metric := range metrics {
		if metric.Name == "gen_ai.workflow.duration" {
			foundWorkflow = true
		}
		if metric.Name == "gen_ai.client.token.usage" && metric.Attributes["gen_ai.token.type"] == "input" {
			foundInputTokens = true
		}
	}
	if !foundWorkflow || !foundInputTokens {
		t.Fatalf("expected workflow and token metrics, got %#v", metrics)
	}

	batches, err := os.ReadDir(filepath.Join(dataDir, "batches"))
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 {
		t.Fatalf("expected 2 batch files (traces + metrics), got %d", len(batches))
	}
}

func TestConcurrentRunsUploadTranscriptOnce(t *testing.T) {
	base := t.TempDir()
	rollout := filepath.Join(base, "rollout-race.jsonl")
	if err := os.WriteFile(rollout, []byte(completedRolloutFixture("sess-race", "turn-race")), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	cfg := testHookConfig(base, server.URL)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- RunWithOptions(RunOptions{
				Config: &cfg,
				Input:  &Input{TranscriptPath: rollout},
			})
		}()
	}
	close(start)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	requestCount := requests
	mu.Unlock()
	if requestCount != 2 {
		t.Fatalf("expected one trace and one metric request, got %d requests", requestCount)
	}
	sidecarBody, err := os.ReadFile(rollout + ".gtrace")
	if err != nil {
		t.Fatal(err)
	}
	if lines := nonEmptyLines(string(sidecarBody)); len(lines) != 1 {
		t.Fatalf("expected one uploaded-turn marker, got %q", string(sidecarBody))
	}
	if _, err := os.Stat(rollout + ".gtrace.lock"); !os.IsNotExist(err) {
		t.Fatalf("expected lock file to be removed, stat error: %v", err)
	}
}

func TestSequentialRunsWaitForTerminalTurnAndUploadOnce(t *testing.T) {
	base := t.TempDir()
	rollout := filepath.Join(base, "rollout-open.jsonl")
	openBody := joinJSONLines(
		row("2026-07-24T10:00:00Z", "session_meta", map[string]any{
			"id":             "sess-open",
			"cli_version":    "0.145.0",
			"model_provider": "openai",
		}),
		row("2026-07-24T10:00:01Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-open",
		}),
		row("2026-07-24T10:00:02Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "status",
		}),
		row("2026-07-24T10:00:03Z", "response_item", map[string]any{
			"type":    "reasoning",
			"summary": []any{map[string]any{"text": "still working"}},
		}),
		row("2026-07-24T10:00:04Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 5, "output_tokens": 1},
			},
		}),
	)
	if err := os.WriteFile(rollout, []byte(openBody), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	cfg := testHookConfig(base, server.URL)

	for range 2 {
		if err := RunWithOptions(RunOptions{Config: &cfg, Input: &Input{TranscriptPath: rollout}}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	beforeCompletion := requests
	mu.Unlock()
	if beforeCompletion != 0 {
		t.Fatalf("expected no requests for incomplete turn, got %d", beforeCompletion)
	}
	if _, err := os.Stat(rollout + ".gtrace"); !os.IsNotExist(err) {
		t.Fatalf("expected no sidecar for incomplete turn, stat error: %v", err)
	}

	file, err := os.OpenFile(rollout, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString(joinJSONLines(
		row("2026-07-24T10:00:05Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "healthy",
		}),
		row("2026-07-24T10:00:06Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	))
	closeErr := file.Close()
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}

	for range 2 {
		if err := RunWithOptions(RunOptions{Config: &cfg, Input: &Input{TranscriptPath: rollout}}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	afterCompletion := requests
	mu.Unlock()
	if afterCompletion != 2 {
		t.Fatalf("expected one trace and one metric request after completion, got %d", afterCompletion)
	}
	sidecarBody, err := os.ReadFile(rollout + ".gtrace")
	if err != nil {
		t.Fatal(err)
	}
	if lines := nonEmptyLines(string(sidecarBody)); len(lines) != 1 {
		t.Fatalf("expected one uploaded-turn marker, got %q", string(sidecarBody))
	}
}

func TestRunRetriesOnlyMetricsAfterPartialUpload(t *testing.T) {
	base := t.TempDir()
	rollout := filepath.Join(base, "rollout-partial.jsonl")
	if err := os.WriteFile(rollout, []byte(completedRolloutFixture("sess-partial", "turn-partial")), 0o600); err != nil {
		t.Fatal(err)
	}

	traceRequests := 0
	metricRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/traces":
			traceRequests++
			writer.WriteHeader(http.StatusOK)
		case "/v1/metrics":
			metricRequests++
			if metricRequests == 1 {
				http.Error(writer, "retry", http.StatusServiceUnavailable)
				return
			}
			writer.WriteHeader(http.StatusOK)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	cfg := testHookConfig(base, server.URL)

	if err := RunWithOptions(RunOptions{Config: &cfg, Input: &Input{TranscriptPath: rollout}}); err == nil {
		t.Fatal("expected first metrics upload to fail")
	}
	if _, err := os.Stat(rollout + ".gtrace"); !os.IsNotExist(err) {
		t.Fatalf("legacy completion marker must not be written after partial success: %v", err)
	}
	for range 2 {
		if err := RunWithOptions(RunOptions{Config: &cfg, Input: &Input{TranscriptPath: rollout}}); err != nil {
			t.Fatal(err)
		}
	}
	if traceRequests != 1 || metricRequests != 2 {
		t.Fatalf("requests: traces=%d metrics=%d", traceRequests, metricRequests)
	}
	body, err := os.ReadFile(rollout + ".gtrace")
	if err != nil {
		t.Fatal(err)
	}
	if len(nonEmptyLines(string(body))) != 1 {
		t.Fatalf("unexpected completion marker: %q", body)
	}
}

func testHookConfig(base, endpoint string) config.Config {
	return config.Config{
		Enabled:            true,
		Endpoint:           endpoint,
		TracePath:          "v1/traces",
		MetricsPath:        "v1/metrics",
		TimeoutMs:          5000,
		MaxChars:           4096,
		HookLogFile:        filepath.Join(base, "gtrace-hook.log"),
		StateDir:           filepath.Join(base, "state"),
		LockStaleMs:        1000,
		ResourceAttributes: map[string]any{},
	}
}

func completedRolloutFixture(sessionID, turnID string) string {
	return joinJSONLines(
		row("2026-07-24T08:00:00Z", "session_meta", map[string]any{
			"id":             sessionID,
			"cli_version":    "0.145.0",
			"model_provider": "openai",
		}),
		row("2026-07-24T08:00:01Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": turnID,
		}),
		row("2026-07-24T08:00:02Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "hello",
		}),
		row("2026-07-24T08:00:03Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "world"},
			},
		}),
		row("2026-07-24T08:00:04Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 11, "output_tokens": 7},
			},
		}),
		row("2026-07-24T08:00:05Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	)
}

func nonEmptyLines(value string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func row(ts, typ string, payload map[string]any) map[string]any {
	return map[string]any{
		"timestamp": ts,
		"type":      typ,
		"payload":   payload,
	}
}

func joinJSONLines(rows ...map[string]any) string {
	lines := make([]string, 0, len(rows))
	for _, entry := range rows {
		lines = append(lines, mustJSON(entry))
	}
	return stringsJoin(lines, "\n") + "\n"
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func stringsJoin(items []string, sep string) string {
	if len(items) == 0 {
		return ""
	}
	out := items[0]
	for _, item := range items[1:] {
		out += sep + item
	}
	return out
}

func contains(text, fragment string) bool {
	return len(text) >= len(fragment) && func() bool {
		for i := 0; i+len(fragment) <= len(text); i++ {
			if text[i:i+len(fragment)] == fragment {
				return true
			}
		}
		return false
	}()
}
