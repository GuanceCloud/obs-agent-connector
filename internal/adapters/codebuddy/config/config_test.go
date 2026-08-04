package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveMergesUserProjectAndEnvironment(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codebuddy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".codebuddy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codebuddy", "gtrace.json"), []byte(`{"enabled":false,"endpoint":"https://user.example","headers":{"X-Custom":"keep"},"resourceAttributes":{"env":"user"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codebuddy", "gtrace.json"), []byte(`{"captureContent":"none","resourceAttributes":{"env":"project"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Resolve(ResolveOptions{Home: home, Cwd: project, Env: map[string]string{"CODEBUDDY_OTEL_ENABLED": "true", "CODEBUDDY_OTEL_ENDPOINT": "https://env.example"}})
	if !cfg.Enabled || cfg.Transport.Endpoint != "https://env.example" || cfg.CaptureContent != "none" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if cfg.Transport.Headers["X-Custom"] != "keep" || cfg.ResourceAttributes["env"] != "project" {
		t.Fatalf("merge lost values: %#v %#v", cfg.Transport.Headers, cfg.ResourceAttributes)
	}
}

func TestResolveDisabledDefaultsDoNotRequireEndpoint(t *testing.T) {
	cfg := Resolve(ResolveOptions{Home: t.TempDir(), Cwd: t.TempDir(), Env: map[string]string{}})
	if cfg.Enabled || cfg.Transport.Endpoint != "" {
		t.Fatalf("unexpected default: %#v", cfg)
	}
}
