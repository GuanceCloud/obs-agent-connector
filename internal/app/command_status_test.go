package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusCodexShowsInstalledEnabledAndVersion(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	configPath := filepath.Join(home, ".codex", "gtrace.json")
	packagePath := filepath.Join(home, ".codex", "plugins", "cache", "codex-otel-plugin", "tracing", "0.1.15")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(packagePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{\"enabled\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := status([]string{"codex"}); err != nil {
			t.Fatal(err)
		}
	})

	for _, expected := range []string{
		"Agent",
		"Installed",
		"Version",
		"Config",
		"Enabled",
		"Agent    : codex",
		"Installed: yes",
		"Version  : dev",
		"Config   : ~/.codex/gtrace.json",
		"Enabled  : true",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestStatusHermesShowsUnsupportedEnabled(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".hermes", "plugins", "hermes-otel-plugin")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := status([]string{"hermes"}); err != nil {
			t.Fatal(err)
		}
	})

	if !strings.Contains(output, "Enabled  : unsupported") {
		t.Fatalf("expected unsupported enabled status, got:\n%s", output)
	}
}

func TestStatusRequiresAgent(t *testing.T) {
	err := status(nil)
	if err == nil {
		t.Fatal("expected missing agent error")
	}
	if !strings.Contains(err.Error(), "status requires an agent") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = originalStdout
	})

	fn()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = originalStdout
	return string(data)
}
