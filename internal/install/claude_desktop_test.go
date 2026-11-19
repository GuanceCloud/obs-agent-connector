package install

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func TestClaudeDesktopBundleIsSeparateFromLegacyHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude Desktop installer targets macOS")
	}
	home := filepath.Join(t.TempDir(), "account with 'spaces'")
	source := filepath.Join(t.TempDir(), "build")
	settings := filepath.Join(home, ".claude", "settings.json")
	legacyConfig := agentfiles.ConfigPath(home, "claude")
	legacyRuntime := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	sharedState := filepath.Join(home, ".claude", "state", "gtrace-agent", "uploads", "turn", "completed.json")
	legacy := map[string]string{
		settings:      `{"hooks":{"Stop":[{"hooks":[{"command":"obs-agent-connector hook claude"}]}]}}`,
		legacyConfig:  `{"enabled":true,"endpoint":"https://legacy.invalid"}`,
		legacyRuntime: "legacy runtime",
		sharedState:   "completed",
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range legacy {
		write(path, body)
	}
	write(source, "new runtime")
	bundle, err := InstallClaudeDesktop(CodexOptions{
		Home: home, SourceExecutable: source,
		Endpoint: "https://desktop.invalid", XToken: "test-only-secret",
		InstallType: "gtrace", CaptureContent: "none",
	})
	if err != nil || bundle != desktop.BundlePath(home) {
		t.Fatalf("prepared bundle = %q, %v", bundle, err)
	}

	archive, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	files := map[string][]byte{}
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(reader)
		closeErr := reader.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("read ZIP entry: %v, %v", err, closeErr)
		}
		if strings.Contains(string(body), "test-only-secret") {
			t.Fatal("ZIP contains the X-Token")
		}
		files[file.Name] = body
	}
	if len(files) != 2 {
		t.Fatalf("expected exactly the manifest and hooks, got %d ZIP entries", len(files))
	}
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(files[".claude-plugin/plugin.json"], &manifest); err != nil || manifest.Name != desktop.PluginName || manifest.Version == "" {
		t.Fatalf("manifest = %#v, %v", manifest, err)
	}

	var hookFile struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(files["hooks/hooks.json"], &hookFile); err != nil || len(hookFile.Hooks) != 2 {
		t.Fatalf("hook file = %#v, %v", hookFile, err)
	}
	executable := desktop.ExecutablePath(home)
	for _, event := range []string{"Stop", "SessionEnd"} {
		groups := hookFile.Hooks[event]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Fatalf("%s must register one command Hook: %#v", event, groups)
		}
		handler := groups[0].Hooks[0]
		if handler.Type != "command" || handler.Command != executable || !reflect.DeepEqual(handler.Args, []string{"hook", "claude-desktop", home}) {
			t.Fatalf("%s handler = %#v", event, handler)
		}
	}
	if info, err := os.Stat(executable); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("Desktop runtime not executable: %v", err)
	}

	config := agentfiles.ConfigPath(home, desktop.Name)
	configBody, err := os.ReadFile(config)
	if err != nil || !strings.Contains(string(configBody), `"captureContent": "none"`) ||
		!strings.Contains(string(configBody), "test-only-secret") {
		t.Fatalf("unexpected Desktop configuration: %v", err)
	}
	write(source, "updated runtime")
	if _, err := InstallClaudeDesktop(CodexOptions{Home: home, SourceExecutable: source, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(config); err != nil || string(body) != string(configBody) {
		t.Fatalf("update changed the Desktop configuration: %v", err)
	}
	if body, err := os.ReadFile(executable); err != nil || string(body) != "updated runtime" {
		t.Fatalf("Desktop runtime was not updated: %v", err)
	}

	if _, err := RemoveAdapter(desktop.Name, home, RemoveOptions{PurgeManaged: true, PurgeConfig: true, PurgeState: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agentfiles.Directory(home, desktop.Name)); !os.IsNotExist(err) {
		t.Fatalf("runtime and bundle remain after removal: %v", err)
	}
	for path, want := range legacy {
		if body, err := os.ReadFile(path); err != nil || string(body) != want {
			t.Fatalf("legacy file %s changed: %v", path, err)
		}
	}
}
