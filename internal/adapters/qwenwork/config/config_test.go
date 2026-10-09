package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedConfigurationAndEnvironment(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".obs-agent-connector", "qwenwork-cn")
	os.MkdirAll(dir, 0700)
	os.WriteFile(filepath.Join(dir, "gtrace.json"), []byte(`{"enabled":true,"endpoint":"https://example.invalid","captureContent":"none","maxChars":1234,"headers":{"X-Token":"test-secret"},"resourceAttributes":{"agent_id":"configured"}}`), 0600)
	cfg := Resolve(ResolveOptions{Home: home, Env: map[string]string{}})
	if !cfg.Enabled || cfg.CaptureContent != "none" || cfg.MaxChars != 1234 || cfg.Transport.Headers["X-Token"] != "test-secret" || cfg.ResourceAttributes["agent_id"] != "configured" {
		t.Fatalf("config mismatch %+v", cfg)
	}
	if cfg.ProfileDir != filepath.Join(home, ".qwenworkcn") || cfg.StateDir != filepath.Join(dir, "state") {
		t.Fatal("wrong ownership")
	}
	cfg = Resolve(ResolveOptions{Home: home, Env: map[string]string{"QWENWORK_CN_OTEL_ENABLED": "false", "OTEL_EXPORTER_OTLP_ENDPOINT": "https://override.invalid"}})
	if cfg.Enabled || cfg.Transport.Endpoint != "https://override.invalid" {
		t.Fatal("environment precedence")
	}
}

func TestCaptureThinkingPrivacyAndPrecedence(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		env  map[string]string
		want bool
	}{
		{name: "default", body: `{}`},
		{name: "enabled", body: `{"captureThinking":true}`, want: true},
		{name: "disabled", body: `{"captureThinking":false}`},
		{name: "legacy enabled", body: `{"capture_thinking":true}`, want: true},
		{name: "canonical false wins", body: `{"captureThinking":false,"capture_thinking":true}`},
		{name: "canonical null fails closed", body: `{"captureThinking":null,"capture_thinking":true}`},
		{name: "malformed string", body: `{"captureThinking":"enabled"}`},
		{name: "malformed object", body: `{"captureThinking":{"enabled":true}}`},
		{name: "malformed array", body: `{"captureThinking":[true]}`},
		{name: "malformed number", body: `{"captureThinking":2}`},
		{name: "malformed document", body: `{"captureThinking":true,`},
		{name: "environment enables", body: `{"captureThinking":false}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_THINKING": "true"}, want: true},
		{name: "environment disables", body: `{"captureThinking":true}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_THINKING": "false"}},
		{name: "environment malformed fails closed", body: `{"captureThinking":true}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_THINKING": "invalid"}},
		{name: "environment empty fails closed", body: `{"captureThinking":true}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_THINKING": ""}},
		{name: "none overrides file", body: `{"captureThinking":true,"captureContent":"none"}`},
		{name: "none overrides environment", body: `{"captureContent":"none"}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_THINKING": "true"}},
		{name: "environment none overrides file", body: `{"captureThinking":true}`, env: map[string]string{"QWENWORK_CN_OTEL_CAPTURE_CONTENT": "none"}},
		{name: "legacy content false", body: `{"capture_thinking":true,"capture_content":false}`},
		{name: "full permits opt in", body: `{"captureThinking":true,"captureContent":"full"}`, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, ".obs-agent-connector", "qwenwork-cn")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "gtrace.json"), []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			env := tt.env
			if env == nil {
				env = map[string]string{}
			}
			cfg := Resolve(ResolveOptions{Home: home, Env: env})
			if cfg.CaptureThinking != tt.want {
				t.Fatalf("CaptureThinking = %v, want %v", cfg.CaptureThinking, tt.want)
			}
		})
	}
}
