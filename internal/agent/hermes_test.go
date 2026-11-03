package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHermesDiscoveryEvidence(t *testing.T) {
	for _, mode := range []string{"path", "config", "plugin", "custom-config", "empty-home", "missing-home", "custom-does-not-fall-back"} {
		t.Run(mode, func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PATH", bin)
			t.Setenv("HERMES_HOME", "")
			root := filepath.Join(home, ".hermes")
			if mode == "custom-config" || mode == "custom-does-not-fall-back" {
				root = filepath.Join(home, "custom hermes")
				t.Setenv("HERMES_HOME", root)
			}
			switch mode {
			case "path":
				command := "hermes"
				if runtime.GOOS == "windows" {
					command += ".exe"
				}
				if err := os.WriteFile(filepath.Join(bin, command), []byte("stub"), 0755); err != nil {
					t.Fatal(err)
				}
			case "config", "custom-config", "custom-does-not-fall-back":
				configRoot := root
				if mode == "custom-does-not-fall-back" {
					configRoot = filepath.Join(home, ".hermes")
				}
				if err := os.MkdirAll(configRoot, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(configRoot, "config.yaml"), []byte("model: test\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "plugin":
				if err := os.MkdirAll(filepath.Join(root, "plugins", "hermes-otel-plugin"), 0755); err != nil {
					t.Fatal(err)
				}
			case "empty-home":
				if err := os.MkdirAll(root, 0755); err != nil {
					t.Fatal(err)
				}
			}
			want := mode == "path" || mode == "config" || mode == "custom-config" || mode == "plugin"
			found := false
			for _, candidate := range DiscoverCandidatesForOS("linux") {
				if candidate.Plugin.Name != "hermes" {
					continue
				}
				found = true
				if candidate.Plugin.ConfigFiles[0] != filepath.Join(root, "config.yaml") {
					t.Fatalf("discovery changed Hermes home: %+v", candidate)
				}
				if mode != "path" && candidate.DetectedCmd != "data-dir" {
					t.Fatalf("unexpected discovery source: %q", candidate.DetectedCmd)
				}
			}
			if found != want {
				t.Fatalf("Hermes discovered = %t, want %t", found, want)
			}
		})
	}
}

func TestHermesHomeLifecycle(t *testing.T) {
	for _, mode := range []string{"unset", "empty", "custom", "tilde"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("HERMES_HOME", "")
			wantHome := filepath.Join(home, ".hermes")
			switch mode {
			case "unset":
				if err := os.Unsetenv("HERMES_HOME"); err != nil {
					t.Fatal(err)
				}
			case "custom":
				wantHome = filepath.Join(home, "custom hermes")
				t.Setenv("HERMES_HOME", wantHome)
			case "tilde":
				wantHome = filepath.Join(home, "custom-hermes")
				t.Setenv("HERMES_HOME", "~/custom-hermes")
			}
			pluginDir := filepath.Join(wantHome, "plugins", "hermes-otel-plugin")
			if err := os.MkdirAll(pluginDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(pluginDir, "VERSION"), []byte("0.1.9"), 0644); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(wantHome, "config.yaml")
			if err := os.WriteFile(configPath, []byte("hermes_otel_plugin: {}\n"), 0644); err != nil {
				t.Fatal(err)
			}
			selected, err := SelectInstalled("hermes")
			if err != nil {
				t.Fatal(err)
			}
			install, err := ResolveForInstall([]Definition{Get("hermes")})
			if err != nil {
				t.Fatal(err)
			}
			definitions := []Definition{Resolve(Get("hermes")), selected[0], install[0], ResolveForRemove(selected)[0]}
			bin := t.TempDir()
			command := "hermes"
			if runtime.GOOS == "windows" {
				command += ".exe"
			}
			if err := os.WriteFile(filepath.Join(bin, command), []byte("stub"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			found := false
			for _, candidate := range DiscoverCandidatesForOS("linux") {
				if candidate.Plugin.Name == "hermes" {
					found = true
					definitions = append(definitions, candidate.Plugin)
					if candidate.InstalledPath != pluginDir || candidate.InstalledVersion != "0.1.9" {
						t.Fatalf("unexpected discovery result: %+v", candidate)
					}
				}
			}
			if !found {
				t.Fatal("Hermes was not discovered")
			}
			for _, p := range definitions {
				if got, ok := InstalledMarker(p); !ok || got != pluginDir {
					t.Fatalf("installed marker = %q, %v; want %q", got, ok, pluginDir)
				}
				if got := InstalledVersion(p); got != "0.1.9" {
					t.Fatalf("version = %q", got)
				}
				if got := FirstExistingPath(p.ConfigFiles); got != configPath {
					t.Fatalf("config = %q; want %q", got, configPath)
				}
				if len(p.RemovePaths) != 1 || p.RemovePaths[0] != pluginDir {
					t.Fatalf("removal paths = %v", p.RemovePaths)
				}
				if len(p.Env) != 1 || p.Env[0] != "HERMES_HOME="+wantHome {
					t.Fatalf("installer environment = %v", p.Env)
				}
			}
		})
	}
}
