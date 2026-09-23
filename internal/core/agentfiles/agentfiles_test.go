package agentfiles

import (
	"path/filepath"
	"testing"
)

func TestManagedAgentPaths(t *testing.T) {
	home := filepath.Join("tmp", "home")
	if got, want := ConfigPath(home, "claude"), filepath.Join(home, ".obs-agent-connector", "claude", "gtrace.json"); got != want {
		t.Fatalf("ConfigPath() = %q, want %q", got, want)
	}
	if got, want := HookLogPath(home, "claude"), filepath.Join(home, ".obs-agent-connector", "claude", "gtrace-hooks.json"); got != want {
		t.Fatalf("HookLogPath() = %q, want %q", got, want)
	}
}

func TestCodexHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", "")
	if got, want := CodexHome(home), filepath.Join(home, ".codex"); got != want {
		t.Fatalf("CodexHome() = %q, want %q", got, want)
	}

	configured := filepath.Join(t.TempDir(), "codex-data")
	t.Setenv("CODEX_HOME", configured)
	if got := CodexHome(home); got != configured {
		t.Fatalf("CodexHome() = %q, want %q", got, configured)
	}
}
