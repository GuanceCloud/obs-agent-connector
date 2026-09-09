package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLegacyAndManagedConfig(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "custom profile")
	os.MkdirAll(profile, 0700)
	os.WriteFile(filepath.Join(profile, "gtrace.json"), []byte(`{"endpoint":"https://old.invalid","enabled":false,"capture_content":false,"max_chars":1234,"timeout_ms":1700,"headers":{"X-Token":"test-only"},"resourceAttributes":{"agent_id":"keep"}}`), 0600)
	cfg := Resolve(ResolveOptions{Home: home, Env: map[string]string{"WORKBUDDY_CONFIG_DIR": profile}})
	if cfg.Enabled || cfg.CaptureContent != "none" || cfg.MaxChars != 1234 || cfg.Transport.Timeout != 1700*time.Millisecond || cfg.Transport.Headers["X-Token"] != "test-only" {
		t.Fatalf("legacy config lost: %+v", cfg)
	}
	managed := filepath.Join(home, ".obs-agent-connector", "workbuddy")
	os.MkdirAll(managed, 0700)
	os.WriteFile(filepath.Join(managed, "gtrace.json"), []byte(`{"enabled":true,"endpoint":"https://new.invalid","captureContent":"full","maxChars":5000}`), 0600)
	cfg = Resolve(ResolveOptions{Home: home, Env: map[string]string{"WORKBUDDY_CONFIG_DIR": profile, "WORKBUDDY_OTEL_ENABLED": "false", "WORKBUDDY_OTEL_CAPTURE_CONTENT": "false"}})
	if cfg.Enabled || cfg.CaptureContent != "none" || cfg.MaxChars != 5000 || cfg.Transport.Endpoint != "https://new.invalid" || cfg.ResourceAttributes["agent_id"] != "keep" {
		t.Fatal("wrong precedence")
	}
}
