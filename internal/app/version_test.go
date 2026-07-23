package app

import (
	"runtime"
	"strings"
	"testing"
)

func TestGitHubReleaseRepoFromVersionPinnedDownloadBase(t *testing.T) {
	repo, ok := githubReleaseRepo("https://github.com/GuanceCloud/obs-agent-connector/releases/download/v0.1.8")
	if !ok {
		t.Fatal("expected GitHub release repo to be detected")
	}
	if repo != "GuanceCloud/obs-agent-connector" {
		t.Fatalf("expected repo path, got %q", repo)
	}
}

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

func TestBuildSelfUpdateCommandUsesTargetTagForGitHubDownloadBase(t *testing.T) {
	command, _, err := buildSelfUpdateCommand("v9.9.9", connectorConfig{
		DownloadBaseURL: "https://github.com/GuanceCloud/obs-agent-connector/releases/download/v0.1.8",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(command, "/releases/download/v9.9.9/") {
		t.Fatalf("expected GitHub self-update command to use target tag, got %q", command)
	}
	if strings.Contains(command, "/releases/download/v0.1.8/") {
		t.Fatalf("expected GitHub self-update command to avoid pinned old tag, got %q", command)
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
