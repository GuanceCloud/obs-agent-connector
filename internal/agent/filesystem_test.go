package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinInstalledMarkerRequiresManagedHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo unrelated"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin, ok := Get("claude").WithBuiltin()
	if !ok {
		t.Fatal("claude must support the built-in runtime")
	}
	if path, installed := InstalledMarker(plugin); installed || path != "" {
		t.Fatalf("unrelated settings must not count as an installed adapter: path=%q installed=%t", path, installed)
	}

	managed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/tmp/obs-agent-connector","args":["hook","claude"]}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, installed := InstalledMarker(plugin); !installed || path != settingsPath {
		t.Fatalf("managed hook was not detected: path=%q installed=%t", path, installed)
	}
}

func TestBuiltinInstalledMarkerRecognizesLegacyTelemetryHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/tmp/agent-telemetry hook codex"}]}]}}`
	if err := os.WriteFile(hooksPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin, ok := Get("codex").WithBuiltin()
	if !ok {
		t.Fatal("codex must support the built-in runtime")
	}
	if path, installed := InstalledMarker(plugin); !installed || path != hooksPath {
		t.Fatalf("legacy hook was not detected: path=%q installed=%t", path, installed)
	}
}
