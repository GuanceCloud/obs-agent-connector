package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestHermesRemovalUsesCustomHome(t *testing.T) {
	for _, purge := range []bool{false, true} {
		t.Run(map[bool]string{false: "keep config", true: "purge config"}[purge], func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			custom := filepath.Join(home, "custom hermes")
			t.Setenv("HERMES_HOME", custom)
			for _, root := range []string{filepath.Join(home, ".hermes"), custom} {
				plugin := filepath.Join(root, "plugins", "hermes-otel-plugin")
				if err := os.MkdirAll(plugin, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "config.yaml"), []byte("sentinel"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"hermes", "--yes"}
			if purge {
				args = append(args, "--purge-config")
			}
			if err := remove(args); err != nil {
				t.Fatal(err)
			}
			if agent.PathExists(filepath.Join(custom, "plugins", "hermes-otel-plugin")) {
				t.Fatal("custom plugin was not removed")
			}
			if agent.PathExists(filepath.Join(custom, "config.yaml")) == purge {
				t.Fatal("custom config retention mismatch")
			}
			if !agent.PathExists(filepath.Join(home, ".hermes", "plugins", "hermes-otel-plugin")) {
				t.Fatal("default plugin was removed")
			}
			data, err := os.ReadFile(filepath.Join(home, ".hermes", "config.yaml"))
			if err != nil || string(data) != "sentinel" {
				t.Fatalf("default config changed: %v", err)
			}
		})
	}
}

func TestHermesInstallerHomeAndUpdateConfig(t *testing.T) {
	home := filepath.Join(t.TempDir(), "custom hermes")
	t.Setenv("HERMES_HOME", home)
	p := agent.Resolve(agent.Get("hermes"))
	download := pluginDownloadConfig{Source: pluginSourceOSS, BaseURL: "https://static.example.com"}
	if got := strings.Join(pluginEnv(download, p), "\n"); !strings.Contains(got, "HERMES_HOME="+home) {
		t.Fatalf("installer environment is missing custom home: %s", got)
	}
	for _, command := range []string{renderInstallCommand(download, p, installInput{}), renderPluginUpdateCommand(download, p)} {
		if !strings.Contains(command, "HERMES_HOME="+shellQuote(home)) {
			t.Fatalf("command is missing custom home: %s", command)
		}
	}
	if command := renderPluginUpdateCommand(download, p); !strings.Contains(command, "--no-config") {
		t.Fatalf("update does not preserve config: %s", command)
	}
}
