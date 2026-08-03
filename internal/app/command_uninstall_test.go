package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovePathLineFromContent(t *testing.T) {
	original := "export PATH=\"/a/bin:$PATH\"\nexport PATH=\"/b/bin:$PATH\"\n"
	updated, changed := removePathLineFromContent(original, `export PATH="/a/bin:$PATH"`)
	if !changed {
		t.Fatal("expected content to change")
	}
	if strings.Contains(updated, `/a/bin`) {
		t.Fatalf("expected target PATH line to be removed, got %q", updated)
	}
	if !strings.Contains(updated, `/b/bin`) {
		t.Fatalf("expected unrelated PATH line to be kept, got %q", updated)
	}
}

func TestRemovePathEntry(t *testing.T) {
	updated, changed := removePathEntry(`C:\A;C:\B;C:\C`, `C:\B`, ";")
	if !changed {
		t.Fatal("expected path list to change")
	}
	if updated != `C:\A;C:\C` {
		t.Fatalf("unexpected updated PATH %q", updated)
	}
}

func TestUninstallDryRun(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	executablePath := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	if err := os.MkdirAll(filepath.Dir(executablePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executablePath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	configDir := filepath.Join(home, ".obs-agent-connector")
	configPath := filepath.Join(configDir, "config.json")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	zshrcPath := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(zshrcPath, []byte("export PATH=\""+filepath.Dir(executablePath)+":$PATH\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	previousExecutable := currentExecutable
	previousEvalSymlinks := currentEvalSymlinks
	previousGOOS := currentGOOS
	currentExecutable = func() (string, error) { return executablePath, nil }
	currentEvalSymlinks = func(path string) (string, error) { return path, nil }
	currentGOOS = "linux"
	t.Cleanup(func() {
		currentExecutable = previousExecutable
		currentEvalSymlinks = previousEvalSymlinks
		currentGOOS = previousGOOS
	})

	output := captureStdout(t, func() {
		if err := uninstallConnector([]string{"--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})

	for _, expected := range []string{
		"Uninstall plan:",
		"Binary        : " + executablePath,
		"Built-in Hooks: remove claude and codex; keep Agent config and state",
		"Config        : remove " + configPath,
		"Shell PATH    : remove managed entry from " + zshrcPath,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
	if _, err := os.Stat(executablePath); err != nil {
		t.Fatalf("expected binary to remain during dry-run: %v", err)
	}
}
