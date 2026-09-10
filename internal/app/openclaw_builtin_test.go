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
