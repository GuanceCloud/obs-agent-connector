package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuiltInInstallersUseManagedConfigPaths(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source", "obs-agent-connector")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := true

	tests := []struct {
		name    string
		install func() (string, error)
	}{
		{name: "codex", install: func() (string, error) {
			result, err := InstallCodex(CodexOptions{Home: home, SourceExecutable: source, Endpoint: "https://example.invalid", Enabled: &enabled, SkipTrust: true})
			return result.ConfigFile, err
		}},
		{name: "claude", install: func() (string, error) {
			result, err := InstallClaude(ClaudeOptions{Home: home, SourceExecutable: source, Endpoint: "https://example.invalid", Enabled: &enabled})
			return result.ConfigFile, err
		}},
		{name: "cursor", install: func() (string, error) {
			result, err := InstallCursor(CursorOptions{Home: home, SourceExecutable: source, Endpoint: "https://example.invalid", Enabled: &enabled})
			return result.ConfigFile, err
		}},
		{name: "codebuddy", install: func() (string, error) {
			result, err := InstallCodeBuddy(CodeBuddyOptions{Home: home, SourceExecutable: source, Endpoint: "https://example.invalid", Enabled: &enabled})
			return result.ConfigFile, err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configFile, err := test.install()
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(home, ".obs-agent-connector", test.name, "gtrace.json")
			if configFile != want {
				t.Fatalf("config file = %q, want %q", configFile, want)
			}
			if _, err := os.Stat(want); err != nil {
				t.Fatalf("managed config was not generated: %v", err)
			}
		})
	}
}

func TestBuiltInPurgeRemovesManagedAndLegacyFiles(t *testing.T) {
	legacyConfig := map[string]string{
		"codex":     filepath.Join(".codex", "gtrace.json"),
		"claude":    filepath.Join(".claude", "gtrace.json"),
		"cursor":    filepath.Join(".cursor", "gtrace.json"),
		"codebuddy": filepath.Join(".codebuddy", "gtrace.json"),
	}
	for agent, legacyRelativePath := range legacyConfig {
		t.Run(agent, func(t *testing.T) {
			home := t.TempDir()
			managedConfig := filepath.Join(home, ".obs-agent-connector", agent, "gtrace.json")
			managedLog := filepath.Join(home, ".obs-agent-connector", agent, "gtrace-hooks.json")
			legacyPath := filepath.Join(home, legacyRelativePath)
			for _, path := range []string{managedConfig, managedLog, legacyPath} {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			result, err := RemoveAdapter(agent, home, RemoveOptions{PurgeConfig: true, PurgeState: true})
			if err != nil {
				t.Fatal(err)
			}
			if !result.ConfigRemoved || !result.StatePurged {
				t.Fatalf("purge result = %#v", result)
			}
			for _, path := range []string{managedConfig, managedLog, legacyPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("purged file remains at %s: %v", path, err)
				}
			}
		})
	}
}
