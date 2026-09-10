package hook

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
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
	cfg := config.Config{Enabled: true, CaptureContent: "full", MaxChars: 1000, StateDir: t.TempDir(), Transport: transport.Config{Endpoint: server.URL, TracePath: "v1/traces", MetricsPath: "v1/metrics", Timeout: time.Second}}
	payload := `{"event":"agent_end","at":2000000,"durationMs":1000,"sessionId":"s","runId":"r","success":true,"messages":[{"role":"user","timestamp":1999000,"content":"password=secret-value"},{"role":"assistant","timestamp":1999900,"content":[{"type":"text","text":"done"}],"usage":{"input":10,"output":2}}]}`
	for i := 0; i < 2; i++ {
		if err := Run(cfg, strings.NewReader(payload), nil); err != nil {
			t.Fatal(err)
		}
	}
	if count != 2 {
		t.Fatalf("expected one request per signal, got %d", count)
	}
}
