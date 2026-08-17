package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveReadsCursorConfigAndEnvironmentOverrides(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".cursor", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://config.example.com","enabled":false,"captureContent":"none","resourceAttributes":{"team":"platform"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Resolve(ResolveOptions{Home: home, Cwd: t.TempDir(), Env: map[string]string{
		"CURSOR_OTEL_ENABLED":  "true",
		"CURSOR_OTEL_ENDPOINT": "https://env.example.com",
	}})
	if !cfg.Enabled || cfg.Transport.Endpoint != "https://env.example.com" {
		t.Fatalf("unexpected resolved config: %#v", cfg)
	}
	if cfg.CaptureContent != "none" || cfg.ResourceAttributes["team"] != "platform" {
		t.Fatalf("Cursor config fields were not preserved: %#v", cfg)
	}
}
