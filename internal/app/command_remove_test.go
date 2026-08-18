package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveCodexDryRunShowsResolvedCommandWhenExplicitBinaryExists(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	command := filepath.Join(home, "codex")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	previous := os.Getenv("CODEX_BINARY")
	if err := os.Setenv("CODEX_BINARY", command); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("CODEX_BINARY", previous)
	})

	output := captureStdout(t, func() {
		if err := remove([]string{"codex", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})

	for _, expected := range []string{
		"Command      : " + command + " plugin remove tracing@codex-otel-plugin",
		"Command      : " + command + " plugin marketplace remove codex-otel-plugin",
		"Cleanup      : ~/.codex/config.toml (remove marketplace and plugin registration)",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestRemoveCodexDryRunShowsLocalCleanupOnly(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	previousBinary := os.Getenv("CODEX_BINARY")
	previousCLIPath := os.Getenv("CODEX_CLI_PATH")
	previousPath := os.Getenv("PATH")
	if err := os.Setenv("CODEX_BINARY", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CODEX_CLI_PATH", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("PATH", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("CODEX_BINARY", previousBinary)
		_ = os.Setenv("CODEX_CLI_PATH", previousCLIPath)
		_ = os.Setenv("PATH", previousPath)
	})

	output := captureStdout(t, func() {
		if err := remove([]string{"codex", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})

	for _, expected := range []string{
		"Remove plan:",
		"Agent        : codex",
		"Cleanup      : ~/.codex/config.toml (remove marketplace and plugin registration)",
		"Cleanup      : ~/.codex/hooks.json (remove managed Stop hooks)",
		"Path         : ~/.codex/plugin-sources/codex-otel-plugin",
		"Path         : ~/.codex/plugins/cache/codex-otel-plugin",
		"Managed Files: remove ~/.obs-agent-connector/codex",
		"Config Mode  : legacy and external config kept",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "plugin remove tracing@codex-otel-plugin") {
		t.Fatalf("expected no external codex remove command in output, got:\n%s", output)
	}
}
