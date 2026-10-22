package sandbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func TestEndpointPathQueueScopeSurvivesURLSettingRemoval(t *testing.T) {
	cfg := config.Config{Enabled: true, SandboxEnabled: true, StateDir: t.TempDir(), Endpoint: "https://collector.example", TracePath: "v1/write/otel-llm", LogsPath: "v1/write/otel-logs"}
	// This literal pins the RC3 hash layout for an empty full-URL override.
	legacy := `{"Endpoint":"https://collector.example","TracePath":"v1/write/otel-llm","TraceURL":"","LogsPath":"v1/write/otel-logs","LogsURL":"","PublicKey":"","SecretKey":"","Headers":null}`
	hash := sha256.New()
	hash.Write([]byte(legacy))
	// digest separates each input part with a NUL delimiter.
	hash.Write([]byte{0})
	if Scope(cfg) != hex.EncodeToString(hash.Sum(nil)) {
		t.Fatal("endpoint/path queues and bindings moved to a different scope")
	}
}

func TestLogsDeliveryDiagnosticsRecordOutcomesWithoutContent(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		rejected   int
		retry      bool
	}{
		{name: "accepted", status: 200},
		{name: "permanent", status: 403, body: "private-response"},
		{name: "retry", status: 503, body: "private-response", retry: true},
		{name: "partial-rejection", status: 200, body: string([]byte{0x0a, 0x02, 0x08, 0x01}), rejected: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receiver := &sink{status: tc.status, response: []byte(tc.body)}
			server := httptest.NewServer(receiver.handler(t))
			defer server.Close()
			now := time.Now().UTC()
			store := Store{Root: t.TempDir(), HookLogFile: filepath.Join(t.TempDir(), "gtrace-hooks.json"), CaptureContent: true, Now: func() time.Time { return now }}
			event := sampleEvent("event", "obs.sandbox.created")
			event.Reason = "private-runtime-reason"
			if err := store.Apply(event); err != nil {
				t.Fatal(err)
			}
			client := transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs", Headers: map[string]string{"X-Token": "private-auth"}}}
			err := store.Flush(context.Background(), client)
			failed := tc.status != 200 || tc.rejected > 0
			if (err != nil) != failed {
				t.Fatalf("unexpected delivery result: %v", err)
			}
			body, err := os.ReadFile(store.HookLogFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"private-response", "private-runtime-reason", "private-auth", server.URL} {
				if strings.Contains(string(body), secret) {
					t.Fatal("diagnostics exposed delivery content or credentials")
				}
			}
			lines := strings.Split(strings.TrimSpace(string(body)), "\n")
			wantRows := 1
			if tc.retry {
				wantRows++
			}
			if len(lines) != wantRows {
				t.Fatalf("diagnostic rows = %d, want %d", len(lines), wantRows)
			}
			var row struct {
				Message string         `json:"message"`
				Extra   map[string]any `json:"extra"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &row); err != nil {
				t.Fatal(err)
			}
			wantMessage := "uploaded logs"
			if failed {
				wantMessage = "logs upload failed"
			}
			if row.Message != wantMessage || row.Extra["signal"] != "logs" || row.Extra["source"] != Source || row.Extra["status"] != float64(tc.status) || row.Extra["log_count"] != float64(1) || row.Extra["rejected_log_records"] != float64(tc.rejected) {
				t.Fatal("missing delivery outcome metadata")
			}
			if failed && (row.Extra["category"] == nil || row.Extra["retryable"] != tc.retry) {
				t.Fatal("missing failure classification")
			}
			if tc.retry {
				if err := json.Unmarshal([]byte(lines[1]), &row); err != nil {
					t.Fatal(err)
				}
				var scheduled time.Time
				if err := store.access(func(l *ledger) error { scheduled = l.Queue[0].RetryAt; return nil }, false); err != nil {
					t.Fatal(err)
				}
				if row.Message != "logs retry scheduled" || row.Extra["retry_at"] != scheduled.Format(time.RFC3339Nano) {
					t.Fatal("retry diagnostic differs from durable schedule")
				}
			}
			// An empty queue or a not-yet-due retry must not invent an upload.
			_ = store.Flush(context.Background(), client)
			after, err := os.ReadFile(store.HookLogFile)
			if err != nil || string(after) != string(body) {
				t.Fatal("recorded a delivery without a request")
			}
		})
	}
}

func TestLogsDiagnosticWriteFailureDoesNotBlockDelivery(t *testing.T) {
	receiver := &sink{}
	server := httptest.NewServer(receiver.handler(t))
	defer server.Close()
	store := Store{Root: t.TempDir(), HookLogFile: t.TempDir()}
	if err := store.Apply(sampleEvent("event", "obs.sandbox.created")); err != nil {
		t.Fatal(err)
	}
	client := transport.Client{Config: transport.Config{Endpoint: server.URL, LogsPath: "otel/v1/logs"}}
	if err := store.Flush(context.Background(), client); err != nil {
		t.Fatal(err)
	}
	if len(receiver.records) != 1 {
		t.Fatal("diagnostic failure prevented delivery")
	}
}
