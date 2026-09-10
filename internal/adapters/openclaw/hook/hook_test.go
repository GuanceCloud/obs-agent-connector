package hook

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type forbiddenReader struct{}

func (forbiddenReader) Read([]byte) (int, error) { panic("disabled hook read input") }
func TestDisabledEarlyExit(t *testing.T) {
	if err := Run(config.Config{}, forbiddenReader{}, nil); err != nil {
		t.Fatal(err)
	}
}
func TestTerminalHookToOTLP(t *testing.T) {
	count := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := gzip.NewReader(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		defer reader.Close()
		body, _ := io.ReadAll(reader)
		if strings.Contains(string(body), "secret-value") {
			t.Error("secret in OTLP")
		}
		count++
	}))
	defer server.Close()
	cfg := config.Config{Enabled: true, LogFile: filepath.Join(t.TempDir(), "gtrace-hooks.json"), CaptureContent: "full", MaxChars: 1000, StateDir: t.TempDir(), Transport: transport.Config{Endpoint: server.URL, TracePath: "v1/traces", MetricsPath: "v1/metrics", Timeout: time.Second}}
	payload := `{"event":"agent_end","at":2000000,"durationMs":1000,"sessionId":"s","runId":"r","success":true,"messages":[{"role":"user","timestamp":1999000,"content":"password=secret-value"},{"role":"assistant","timestamp":1999900,"content":[{"type":"text","text":"done"}],"usage":{"input":10,"output":2}}]}`
	for i := 0; i < 2; i++ {
		if err := Run(cfg, strings.NewReader(payload), nil); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := os.ReadFile(cfg.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	messages := map[string]int{}
	for _, line := range strings.Split(strings.TrimSpace(string(logs)), "\n") {
		var event struct {
			TS      string         `json:"ts"`
			Message string         `json:"message"`
			Extra   map[string]any `json:"extra"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if _, err := time.Parse(time.RFC3339Nano, event.TS); err != nil {
			t.Fatal(err)
		}
		messages[event.Message]++
		if strings.HasPrefix(event.Message, "uploaded ") && event.Extra["status"] != float64(200) {
			t.Fatalf("missing upload status: %s", line)
		}
	}
	for name, want := range map[string]int{"hook invoked": 2, "parsed transcript": 2, "uploaded spans": 1, "uploaded metrics": 1} {
		if messages[name] != want {
			t.Fatalf("%s count = %d, want %d", name, messages[name], want)
		}
	}
	if strings.Contains(string(logs), "secret-value") || strings.Contains(string(logs), "password") {
		t.Fatal("payload leaked to logs")
	}
	if count != 2 {
		t.Fatalf("expected one request per signal, got %d", count)
	}
}

func TestInvalidAndSkippedEventsAreLogged(t *testing.T) {
	cfg := config.Config{Enabled: true, LogFile: filepath.Join(t.TempDir(), "gtrace-hooks.json"), StateDir: t.TempDir()}
	if err := Run(cfg, strings.NewReader("secret-invalid-json"), nil); err == nil {
		t.Fatal("expected decode failure")
	}
	if err := Run(cfg, strings.NewReader(`{"event":"agent_end"}`), nil); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(cfg.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "hook failed") || !strings.Contains(string(body), "turn skipped") || strings.Contains(string(body), "secret-invalid-json") {
		t.Fatalf("incorrect diagnostics: %s", body)
	}
	// A log write failure must not fail an otherwise valid flush.
	cfg.LogFile = t.TempDir()
	if err := Run(cfg, strings.NewReader(`{"event":"flush"}`), nil); err != nil {
		t.Fatal(err)
	}
}
