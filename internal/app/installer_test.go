package app

import (
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"strings"
	"testing"
	"time"
)

func TestDefaultAgentNameIncludesAgentAndDate(t *testing.T) {
	name := defaultAgentName("claude", time.Date(2026, time.July, 15, 10, 30, 0, 0, time.Local))
	if !strings.HasSuffix(name, "_claude_20260715") {
		t.Fatalf("expected agent and date suffix, got %q", name)
	}
}

func TestResolveInstallInputKeepsExplicitAgentName(t *testing.T) {
	input, err := resolveInstallInput(installInput{AgentName: "custom_name"}, connectorConfig{
		Endpoint: "https://llm-openway.guance.com",
		XToken:   "agent_test",
	}, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if input.AgentName != "custom_name" {
		t.Fatalf("expected explicit agent name to be kept, got %q", input.AgentName)
	}
}

func TestInstallerURLForWindowsUsesGitHubReleaseScript(t *testing.T) {
	definition := agentDefinitionForTest("codex")
	url, err := installerURLForOS("https://static.example.com", definition, "windows")
	if err != nil {
		t.Fatal(err)
	}
	expected := "https://github.com/GuanceCloud/codex-otel-plugin/releases/latest/download/install-release.ps1"
	if url != expected {
		t.Fatalf("expected %q, got %q", expected, url)
	}
}

func TestRenderInstallCommandForWindowsUsesPowerShell(t *testing.T) {
	previous := currentGOOS
	currentGOOS = "windows"
	t.Cleanup(func() {
		currentGOOS = previous
	})

	command := renderInstallCommand("https://static.example.com", agentDefinitionForTest("openclaw"), installInput{
		Endpoint:  "https://llm-openway.guance.com",
		XToken:    "agent_test",
		AgentID:   "agent_123",
		AgentName: "demo_openclaw_20260721",
	})

	if !strings.Contains(command, "install-release.ps1") {
		t.Fatalf("expected PowerShell release installer in command %q", command)
	}
	if !strings.Contains(command, "-Type 'gtrace'") {
		t.Fatalf("expected Windows openclaw command to include -Type gtrace, got %q", command)
	}
	if strings.Contains(command, "OSS_ENDPOINT=") {
		t.Fatalf("expected Windows command to avoid OSS shell env, got %q", command)
	}
}

func TestUnsupportedPlatformErrorForWindows(t *testing.T) {
	err := unsupportedPlatformError(agentDefinitionForTest("claude"), "windows")
	if err == nil {
		t.Fatal("expected unsupported Windows error")
	}
	message := err.Error()
	if !strings.Contains(message, "claude is not supported on Windows") {
		t.Fatalf("unexpected error message %q", message)
	}
	if !strings.Contains(message, "codex, openclaw, qoder") {
		t.Fatalf("expected supported Windows agent list in %q", message)
	}
}

func TestDownloadSourceURLUsesOSSArchiveForCodexOnUnix(t *testing.T) {
	url, err := downloadSourceURL("https://static.example.com", agentDefinitionForTest("codex"), "linux")
	if err != nil {
		t.Fatal(err)
	}
	expected := "https://static.example.com/codex-otel-plugin/codex-otel-plugin.tar.gz"
	if url != expected {
		t.Fatalf("expected %q, got %q", expected, url)
	}
}

func TestRenderPluginUpdateCommandUsesOSSArchiveForQoder(t *testing.T) {
	previous := currentGOOS
	currentGOOS = "linux"
	t.Cleanup(func() {
		currentGOOS = previous
	})

	command := renderPluginUpdateCommand("https://static.example.com", agentDefinitionForTest("qoder"))
	if !strings.Contains(command, "https://static.example.com/qoder-otel-plugin/qoder-otel-plugin.tar.gz") {
		t.Fatalf("expected qoder OSS archive in command %q", command)
	}
	if strings.Contains(command, "github.com") {
		t.Fatalf("expected qoder update command to avoid GitHub in %q", command)
	}
}

func agentDefinitionForTest(name string) agent.Definition {
	definition := agent.Get(name)
	if definition.Name == "" {
		panic("missing test agent definition: " + name)
	}
	return definition
}
