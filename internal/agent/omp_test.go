package agent

import (
	"os"
	"path/filepath"
	"testing"

	ompbridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/bridge"
)

func TestOMPInstalledMarkerAndProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "profile", "agent"))
	p := Resolve(Get("omp"))
	if !p.IsBuiltin() {
		t.Fatal("OMP must be built-in")
	}
	if path, ok := InstalledMarker(p); ok || path != "" {
		t.Fatal("missing extension considered installed")
	}
	os.MkdirAll(filepath.Dir(p.BuiltinHookFile), 0700)
	os.WriteFile(p.BuiltinHookFile, []byte("// unrelated"), 0600)
	if _, ok := InstalledMarker(p); ok {
		t.Fatal("unmanaged extension considered installed")
	}
	os.WriteFile(p.BuiltinHookFile, []byte(ompbridge.ExtensionMarker+"\n"), 0600)
	if path, ok := InstalledMarker(p); !ok || path != ompbridge.ExtensionPath(home) {
		t.Fatal("managed extension not detected")
	}
}
