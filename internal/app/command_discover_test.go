package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverInstallsHermesIntoSelectedHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Hermes uses the Unix installer")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	home, bin := t.TempDir(), t.TempDir()
	profile := filepath.Join(home, "data", "hermes data")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HERMES_HOME", profile)
	t.Setenv("PATH", bin)
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, "connector.json"))
	t.Setenv("OBS_AGENT_CONNECTOR_PLUGIN_SOURCE", "oss")
	capture := filepath.Join(home, "installer-home")
	t.Setenv("INSTALL_HOME_CAPTURE", capture)
	if err := os.Symlink(bash, filepath.Join(bin, "bash")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(profile, 0755); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(profile, "config.yaml")
	if err := os.WriteFile(config, []byte("model: preserved\n"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent_plugins/hermes-otel-plugin/install.sh" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, "#!/usr/bin/env bash\nprintf '%s' \"$HERMES_HOME\" > \"$INSTALL_HOME_CAPTURE\"\n")
	}))
	defer server.Close()
	previousGOOS := currentGOOS
	currentGOOS = "linux"
	t.Cleanup(func() { currentGOOS = previousGOOS })
	output := captureStdout(t, func() {
		err = discover([]string{"--yes", "--endpoint", "https://example.invalid", "--x-token", "test-token", "--static-base", server.URL})
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil || string(data) != profile {
		t.Fatalf("installer did not receive selected HERMES_HOME: %q, %v; output: %s", data, err, output)
	}
	if data, err := os.ReadFile(config); err != nil || string(data) != "model: preserved\n" {
		t.Fatalf("connector modified private YAML configuration: %q, %v", data, err)
	}
}

func TestDiscoverHermesWithCustomHome(t *testing.T) {
	for _, onPath := range []bool{true, false} {
		t.Run(map[bool]string{true: "command", false: "profile"}[onPath], func(t *testing.T) {
			home, bin := t.TempDir(), t.TempDir()
			profile := filepath.Join(home, "hermes data")
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("HERMES_HOME", profile)
			t.Setenv("PATH", bin)
			t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, "connector.json"))
			if err := os.MkdirAll(profile, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profile, "config.yaml"), []byte("model: preserved\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if onPath {
				if err := os.WriteFile(filepath.Join(bin, "hermes"), []byte("#!/bin/sh\n"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			previousGOOS := currentGOOS
			currentGOOS = "linux"
			t.Cleanup(func() { currentGOOS = previousGOOS })
			var err error
			output := captureStdout(t, func() {
				err = discover([]string{"--dry-run", "--endpoint", "https://example.invalid", "--x-token", "test-token"})
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "hermes") || !strings.Contains(output, "install") {
				t.Fatalf("Hermes missing from plan: %s", output)
			}
			if !onPath && !strings.Contains(output, "data-dir") {
				t.Fatalf("profile discovery source missing: %s", output)
			}
		})
	}
}

func TestDiscoverRejectsRemovedNewRuntimeFlag(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	for _, name := range []string{"codex", "opencode"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"endpoint":"https://example.invalid","x_token":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousGOOS := currentGOOS
	currentGOOS = "linux"
	t.Cleanup(func() { currentGOOS = previousGOOS })

	err := discover([]string{"-n", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -n") {
		t.Fatalf("expected removed -n flag error, got %v", err)
	}
}
