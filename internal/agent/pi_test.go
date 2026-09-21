package agent

import (
	"os"
	"path/filepath"
	"testing"

	pibridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/bridge"
)

func TestPiInstalledRequiresManagedExtensionAndRegistration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	definition := Resolve(Get("pi"))
	if !definition.IsBuiltin() {
		t.Fatal("Pi must be built-in")
	}
	if _, ok := InstalledMarker(definition); ok {
		t.Fatal("missing extension considered installed")
	}
	if err := os.MkdirAll(filepath.Dir(definition.BuiltinHookFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition.BuiltinHookFile, []byte(pibridge.ExtensionMarker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := InstalledMarker(definition); ok {
		t.Fatal("unregistered extension considered installed")
	}
	if err := os.MkdirAll(filepath.Dir(pibridge.SettingsPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`{"theme":"dark","extensions":["` + definition.BuiltinHookFile + `"]}`)
	if err := os.WriteFile(pibridge.SettingsPath(home), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	if path, ok := InstalledMarker(definition); !ok || path != definition.BuiltinHookFile {
		t.Fatal("managed registered extension not detected")
	}
}

func TestPiVersionComparison(t *testing.T) {
	for _, item := range []struct {
		left, right string
		want        int
	}{
		{"0.85.0", "0.85.1", -1}, {"0.85.1", "0.85.1", 0}, {"0.86.0", "0.85.1", 1},
	} {
		if got := comparePiVersions(item.left, item.right); got != item.want {
			t.Fatalf("compare %s %s = %d", item.left, item.right, got)
		}
	}
}
