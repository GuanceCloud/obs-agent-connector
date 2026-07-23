package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledVersionFromVersionDirectory(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})

	versionDir := filepath.Join(home, ".qoder", "plugins", "cache", "qoder-marketplace", "qoder-otel-plugin", "0.2.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := InstalledVersion(qoderPlugin()); got != "0.2.0" {
		t.Fatalf("expected qoder version 0.2.0, got %q", got)
	}
}

func TestInstalledVersionFromPackageJSON(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})

	pluginDir := filepath.Join(home, ".openclaw", "extensions", "openclaw-otel-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "package.json"), []byte(`{"version":"1.4.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := InstalledVersion(openClawPlugin()); got != "1.4.2" {
		t.Fatalf("expected openclaw version 1.4.2, got %q", got)
	}
}

func TestDiscoverCandidatesIncludesInstalledVersion(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})

	versionDir := filepath.Join(home, ".qoder", "plugins", "cache", "qoder-marketplace", "qoder-otel-plugin", "0.2.1")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := DiscoverCandidatesForOS("linux")
	for _, candidate := range candidates {
		if candidate.Plugin.Name != "qoder" {
			continue
		}
		if candidate.InstalledVersion != "0.2.1" {
			t.Fatalf("expected qoder installed version 0.2.1, got %q", candidate.InstalledVersion)
		}
		return
	}
	t.Fatal("expected qoder candidate")
}
