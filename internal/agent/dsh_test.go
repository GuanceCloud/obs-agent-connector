package agent

import (
	"path/filepath"
	"testing"
)

func TestDshProfileResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	t.Setenv("DSH_PROFILE", "headless")
	resolved := Resolve(dshPlugin())
	want := filepath.ToSlash(filepath.Join(home, "profiles", "headless")) + "/node_modules/dsh-otel-plugin"
	if resolved.Markers[0] != want || resolved.ConfigFiles[0] != home+"/gtrace.json" {
		t.Fatalf("unexpected DSH paths: markers=%v config=%v", resolved.Markers, resolved.ConfigFiles)
	}
	if got := resolved.PackageArgs; len(got) != 2 || got[1] != "headless" {
		t.Fatalf("unexpected DSH package args: %v", got)
	}
}

func TestDshDiscoveryFromHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DSH_HOME", home)
	if resolved, ok := resolveDshForDiscovery(dshPlugin()); !ok || resolved.Name != "dsh" {
		t.Fatalf("expected DSH discovery from home, got %#v, %t", resolved, ok)
	}
}
