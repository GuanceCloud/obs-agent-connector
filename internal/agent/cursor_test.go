package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveCursorForInstallRequiresCursorHomeOrCommand(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	t.Setenv("CURSOR_BINARY", "")
	t.Setenv("CURSOR_AGENT_BINARY", "")

	if _, err := ResolveForInstall([]Definition{definitions["cursor"]}); err == nil {
		t.Fatal("expected cursor install resolution to fail without data dir or command")
	}

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveForInstall([]Definition{definitions["cursor"]})
	if err != nil {
		t.Fatal(err)
	}
	if got := resolved[0].ConfigFiles; len(got) != 1 || got[0] != "~/.cursor/gtrace.json" {
		t.Fatalf("unexpected cursor config files: %#v", got)
	}
}

func TestDiscoverCandidatesIncludesCursorWithoutCommandWhenDataDirExists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CURSOR_BINARY", "")
	t.Setenv("CURSOR_AGENT_BINARY", "")

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := DiscoverCandidatesForOS("linux")
	for _, candidate := range candidates {
		if candidate.Plugin.Name != "cursor" {
			continue
		}
		if candidate.DetectedCmd != "data-dir" {
			t.Fatalf("expected cursor detect source data-dir, got %q", candidate.DetectedCmd)
		}
		return
	}
	t.Fatal("expected cursor to be discoverable from data dir")
}

func TestResolveCursorCommandPrefersExplicitCommand(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_BINARY", "")
	t.Setenv("CURSOR_AGENT_BINARY", "")
	t.Setenv("CURSOR_CLI_PATH", "")
	t.Setenv("PATH", binDir)

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(binDir, "cursor-agent")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, ok := resolveCursorForDiscovery(definitions["cursor"])
	if !ok {
		t.Fatal("expected cursor discovery to succeed")
	}
	if resolved.AgentCommand != command {
		t.Fatalf("expected resolved cursor command %q, got %q", command, resolved.AgentCommand)
	}
}

func TestResolveCursorCommandDoesNotUseGenericAgentBinary(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CURSOR_BINARY", "")
	t.Setenv("CURSOR_AGENT_BINARY", "")
	t.Setenv("CURSOR_CLI_PATH", "")
	t.Setenv("PATH", binDir)

	if err := os.MkdirAll(filepath.Join(home, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := filepath.Join(binDir, "agent")
	if err := os.WriteFile(command, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, ok := resolveCursorForDiscovery(definitions["cursor"])
	if !ok {
		t.Fatal("expected cursor discovery to succeed from data dir")
	}
	if resolved.AgentCommand != definitions["cursor"].AgentCommand {
		t.Fatalf("expected generic agent binary to be ignored, got %q", resolved.AgentCommand)
	}
}
