package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
}
