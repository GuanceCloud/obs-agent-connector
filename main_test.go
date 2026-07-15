package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePluginsForInstallRequiresQoderDataDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QODER_HOME", "")

	_, err := resolvePluginsForInstall([]plugin{plugins["qoder"]})
	if err == nil || !strings.Contains(err.Error(), "data directory was not found") {
		t.Fatalf("expected missing Qoder directory error, got %v", err)
	}
}

func TestResolvePluginsForInstallDetectsQoderVariant(t *testing.T) {
	tests := []struct {
		name         string
		directory    string
		variant      string
		agentCommand string
	}{
		{name: "global", directory: ".qoder", variant: "global", agentCommand: "qoder"},
		{name: "cn", directory: ".qoder-cn", variant: "cn", agentCommand: "qoder-cn"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("QODER_HOME", "")
			if err := os.MkdirAll(filepath.Join(home, test.directory), 0o755); err != nil {
				t.Fatal(err)
			}

			resolved, err := resolvePluginsForInstall([]plugin{plugins["qoder"]})
			if err != nil {
				t.Fatal(err)
			}
			if len(resolved) != 1 {
				t.Fatalf("expected one resolved plugin, got %d", len(resolved))
			}
			if resolved[0].AgentCommand != test.agentCommand {
				t.Fatalf("expected command %q, got %q", test.agentCommand, resolved[0].AgentCommand)
			}
			if got := resolved[0].InstallArgs; len(got) != 2 || got[1] != test.variant {
				t.Fatalf("expected variant %q, got %#v", test.variant, got)
			}
		})
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
