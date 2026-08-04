package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltinInstalledMarkerRequiresManagedHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	settingsPath := filepath.Join(home, ".codebuddy", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo unrelated"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := Get("codebuddy")
	if path, installed := InstalledMarker(plugin); installed || path != "" {
		t.Fatalf("unrelated settings must not count as an installed adapter: path=%q installed=%t", path, installed)
	}

	managed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/tmp/obs-agent-connector hook codebuddy"}]}]}}`
	if err := os.WriteFile(settingsPath, []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	if path, installed := InstalledMarker(plugin); !installed || path != settingsPath {
		t.Fatalf("managed hook was not detected: path=%q installed=%t", path, installed)
	}
}
