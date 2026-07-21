package app

import (
	"runtime"
	"strings"
	"testing"
)

func TestBuildSelfUpdateCommandUsesInstallerVerificationPath(t *testing.T) {
	command, _, err := buildSelfUpdateCommand("v9.9.9", connectorConfig{
		DownloadBaseURL: "https://static.example.com/obs-agent-connector",
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(command, "BinaryOnly") && !strings.Contains(command, "binary-only") {
		t.Fatalf("expected binary-only installer command, got %q", command)
	}
	installer := "/install.sh"
	if runtime.GOOS == "windows" {
		installer = "/install.ps1"
	}
	if !strings.Contains(command, installer) {
		t.Fatalf("expected installer %q in command %q", installer, command)
	}
	if !strings.Contains(command, "?v=v9.9.9") {
		t.Fatalf("expected version cache key in command %q", command)
	}
}

func TestShowVersionRejectsOfflineUpdateCombination(t *testing.T) {
	err := showVersion([]string{"--offline", "-u"})
	if err == nil {
		t.Fatal("expected error for --offline with -u")
	}
	if !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("unexpected error: %v", err)
	}
}
