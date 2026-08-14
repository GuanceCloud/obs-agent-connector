package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveCodexRemoveUsesExplicitBinary(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "codex")
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

	definition := resolveCodexRemove(Get("codex"))
	if len(definition.RemoveCmds) != 2 {
		t.Fatalf("expected 2 remove commands, got %#v", definition.RemoveCmds)
	}
	if got := definition.RemoveCmds[0][0]; got != command {
		t.Fatalf("expected resolved command %q, got %q", command, got)
	}
}

func TestResolveCodexInstallUsesExplicitBinary(t *testing.T) {
	dir := t.TempDir()
	command := filepath.Join(dir, "codex")
	if err := os.WriteFile(command, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CODEX_BINARY", command)
	definition, err := resolveCodexInstall(Get("codex"))
	if err != nil {
		t.Fatal(err)
	}
	if definition.AgentCommand != command {
		t.Fatalf("expected resolved command %q, got %q", command, definition.AgentCommand)
	}
}

func TestResolveCodexCommandPathForWindowsUsesStandaloneBinary(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	command := filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	previousHome := os.Getenv("HOME")
	previousUserProfile := os.Getenv("USERPROFILE")
	previousLocalAppData := os.Getenv("LOCALAPPDATA")
	previousAppData := os.Getenv("APPDATA")
	previousBinary := os.Getenv("CODEX_BINARY")
	previousCLIPath := os.Getenv("CODEX_CLI_PATH")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("USERPROFILE", home); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("LOCALAPPDATA", filepath.Join(dir, "LocalAppData")); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("APPDATA", filepath.Join(dir, "AppData")); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CODEX_BINARY", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CODEX_CLI_PATH", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
		_ = os.Setenv("USERPROFILE", previousUserProfile)
		_ = os.Setenv("LOCALAPPDATA", previousLocalAppData)
		_ = os.Setenv("APPDATA", previousAppData)
		_ = os.Setenv("CODEX_BINARY", previousBinary)
		_ = os.Setenv("CODEX_CLI_PATH", previousCLIPath)
	})

	got, ok := resolveCodexCommandPathForOS("windows")
	if !ok {
		t.Fatal("expected windows codex command")
	}
	if got != command {
		t.Fatalf("expected standalone command %q, got %q", command, got)
	}
}

func TestResolveCodexCommandPathForWindowsUsesNPMVendorBinary(t *testing.T) {
	dir := t.TempDir()
	localAppData := filepath.Join(dir, "LocalAppData")
	command := filepath.Join(
		localAppData,
		"npm",
		"node_modules",
		"@openai",
		"codex-win32-x64",
		"vendor",
		"x86_64-pc-windows-msvc",
		"bin",
		"codex.exe",
	)
	if err := os.MkdirAll(filepath.Dir(command), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(command, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	previousLocalAppData := os.Getenv("LOCALAPPDATA")
	previousAppData := os.Getenv("APPDATA")
	previousBinary := os.Getenv("CODEX_BINARY")
	previousCLIPath := os.Getenv("CODEX_CLI_PATH")
	if err := os.Setenv("LOCALAPPDATA", localAppData); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("APPDATA", filepath.Join(dir, "AppData")); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CODEX_BINARY", ""); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("CODEX_CLI_PATH", ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("LOCALAPPDATA", previousLocalAppData)
		_ = os.Setenv("APPDATA", previousAppData)
		_ = os.Setenv("CODEX_BINARY", previousBinary)
		_ = os.Setenv("CODEX_CLI_PATH", previousCLIPath)
	})

	got, ok := resolveCodexCommandPathForOS("windows")
	if !ok {
		t.Fatal("expected windows codex command")
	}
	if got != command {
		t.Fatalf("expected npm vendor command %q, got %q", command, got)
	}
}

func TestRemoveCodexConfigSectionsRemovesMarketplacePluginAndHookState(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "config.toml")
	hooksFile := filepath.Join(dir, "hooks.json")
	initial := `
[marketplaces.codex-otel-plugin]
source_type = "local"
source = "/old/path"

[plugins."tracing@codex-otel-plugin"]
enabled = true

[plugins."tracing@codex-observability-plugin"]
enabled = true

[hooks.state."/tmp/other-hooks.json:stop:0:0"]
trusted_hash = "keep"

[hooks.state."` + hooksFile + `:stop:0:0"]
trusted_hash = "remove-me"
`
	if err := os.WriteFile(configFile, []byte(strings.TrimSpace(initial)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeCodexConfigSections(configFile, hooksFile); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, fragment := range []string{
		"[marketplaces.codex-otel-plugin]",
		`[plugins."tracing@codex-otel-plugin"]`,
		`[plugins."tracing@codex-observability-plugin"]`,
		"remove-me",
	} {
		if strings.Contains(text, fragment) {
			t.Fatalf("expected %q removed, got:\n%s", fragment, text)
		}
	}
	if !strings.Contains(text, `trusted_hash = "keep"`) {
		t.Fatalf("expected unrelated hook state kept, got:\n%s", text)
	}
}

func TestRemoveCodexHooksRemovesOnlyManagedStopHooks(t *testing.T) {
	dir := t.TempDir()
	hooksFile := filepath.Join(dir, "hooks.json")
	initial := `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          { "type": "command", "command": "node /tmp/codex-otel-plugin/src/codex-hook-wrapper.js" }
        ]
      },
      {
        "hooks": [
          { "type": "command", "command": "/tmp/.codex/plugins/cache/codex-otel-plugin/tracing/0.1.18/bin/darwin-arm64/codex-hook" }
        ]
      },
      {
        "hooks": [
          { "type": "command", "command": "echo keep-me" }
        ]
      }
    ]
  }
}`
	if err := os.WriteFile(hooksFile, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := removeCodexHooks(hooksFile); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, "codex-otel-plugin") || strings.Contains(text, "codex-hook-wrapper.js") {
		t.Fatalf("expected managed hooks removed, got:\n%s", text)
	}
	if !strings.Contains(text, "keep-me") {
		t.Fatalf("expected unrelated hook kept, got:\n%s", text)
	}
}

func TestCodexDefinitionHasRemoveCleanup(t *testing.T) {
	definition := Get("codex")
	if definition.RemoveCleanup == nil {
		t.Fatal("expected codex remove cleanup hook")
	}
	if len(definition.RemoveCleanupDetails) != 2 {
		t.Fatalf("expected codex remove cleanup details, got %#v", definition.RemoveCleanupDetails)
	}
	if definition.ResolveRemove == nil {
		t.Fatal("expected codex remove resolver")
	}
}
