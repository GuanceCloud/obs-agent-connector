package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOMPConfigMatchesInstallerAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gtrace.json")
	os.WriteFile(path, []byte(`{"enabled":true,"endpoint":"http://127.0.0.1","max_chars":12,"timeout_ms":500,"captureContent":"none","headers":{"X-Token":"test"}}`), 0600)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.MaxChars != 12 || cfg.CaptureContent != "none" || cfg.Transport.Timeout.Milliseconds() != 500 || cfg.Transport.TracePath != "v1/write/otel-llm" {
		t.Fatalf("wrong config: %#v", cfg)
	}
}
