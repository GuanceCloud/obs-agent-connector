package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestOpenClawBuiltinInstallUpdateAndDiscovery(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("OPENCLAW_STATE_DIR", "")
	t.Setenv("OPENCLAW_CONFIG_PATH", "")
	t.Setenv("PATH", t.TempDir())
	executable := filepath.Join(home, "obs-agent-connector")
	if err := os.WriteFile(executable, []byte("runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	original := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = original })
	if err := installBuiltinAdapter(agent.Get("openclaw"), installInput{Endpoint: "https://example.invalid", XToken: "synthetic-secret"}, false); err != nil {
		t.Fatal(err)
	}
	p := agent.Resolve(agent.Get("openclaw"))
	if !p.IsBuiltin() {
		t.Fatal("OpenClaw is external")
	}
	if _, ok := agent.InstalledMarker(p); !ok {
		t.Fatal("installed bridge not detected")
	}
	path := filepath.Join(home, ".obs-agent-connector", "openclaw", "gtrace.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !supportsEditableRuntimeConfig(p) {
		t.Fatal("config command unavailable")
	}
	output := captureStdout(t, func() {
		if err := update([]string{"openclaw", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "https://static.guance.com") || !strings.Contains(output, "Restart the OpenClaw Gateway") {
		t.Fatalf("update did not use built-in installer: %s", output)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("update changed managed config")
	}
}

func TestOpenClawRemoveDoesNotListRetainedLegacyDirectories(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("OPENCLAW_STATE_DIR", filepath.Join(home, "custom-openclaw"))
	t.Setenv("OPENCLAW_CONFIG_PATH", "")
	for _, directory := range []string{"extensions", "plugins"} {
		path := filepath.Join(os.Getenv("OPENCLAW_STATE_DIR"), directory, "openclaw-otel-plugin")
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	p := agent.Resolve(agent.Get("openclaw"))
	if _, installed := agent.InstalledMarker(p); installed {
		t.Fatal("legacy files incorrectly identify a built-in installation")
	}
	runtime := filepath.Join(home, ".obs-agent-connector", "openclaw", "plugin", "runtime.json")
	if err := os.MkdirAll(filepath.Dir(runtime), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime, []byte(`{"command":"obs-agent-connector","args":["hook","openclaw"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, installed := agent.InstalledMarker(p); !installed {
		t.Fatal("managed installation not detected")
	}
	captureStdout(t, func() {
		if err := remove([]string{"openclaw", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	output := captureStdout(t, func() {
		if err := listPlugins(nil); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "openclaw") {
		t.Fatalf("removed adapter is still listed: %s", output)
	}
	for _, directory := range []string{"extensions", "plugins"} {
		if _, err := os.Stat(filepath.Join(os.Getenv("OPENCLAW_STATE_DIR"), directory, "openclaw-otel-plugin")); err != nil {
			t.Fatalf("legacy files were not preserved: %v", err)
		}
	}
}
