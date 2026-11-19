package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func TestClaudeDesktopLifecycleReportsPreparedPackage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Claude Desktop installer targets macOS")
	}

	home := t.TempDir()
	setTestHome(t, home)
	source := filepath.Join(t.TempDir(), "obs-agent-connector")
	if err := os.WriteFile(source, []byte("runtime"), 0o700); err != nil {
		t.Fatal(err)
	}
	originalExecutable := currentExecutable
	originalGOOS := currentGOOS
	originalPluginVersion := buildinfo.Version
	currentExecutable = func() (string, error) { return source, nil }
	currentGOOS = "darwin"
	buildinfo.Version = "1.2.3"
	t.Cleanup(func() {
		currentExecutable = originalExecutable
		currentGOOS = originalGOOS
		buildinfo.Version = originalPluginVersion
	})

	run := func(command func([]string) error, args ...string) string {
		t.Helper()
		return captureStdout(t, func() {
			if err := command(args); err != nil {
				t.Fatal(err)
			}
		})
	}

	if output := run(install, desktop.Name, "--endpoint", "https://test.invalid", "--x-token", "test-token", "--yes"); !strings.Contains(output, desktop.BundlePath(home)) ||
		!strings.Contains(output, "plugin ZIP prepared") || !strings.Contains(output, "Customize > Plugins") {
		t.Fatalf("missing package or activation details: %s", output)
	}
	if output := run(status, desktop.Name); !strings.Contains(output, "Plugin ZIP prepared") ||
		strings.Contains(output, "Installed") || !strings.Contains(output, "Check Customize > Plugins in Claude Desktop.") ||
		!strings.Contains(output, ": 1.2.3") {
		t.Fatalf("package status implies activation: %s", output)
	}

	bundle := desktop.BundlePath(home)
	if err := os.Rename(bundle, filepath.Join(t.TempDir(), "moved-plugin.zip")); err != nil {
		t.Fatal(err)
	}
	if output := run(status, desktop.Name); !strings.Contains(output, "Plugin ZIP prepared: no") || strings.Contains(output, ": 1.2.3") {
		t.Fatalf("status reports a missing ZIP as prepared: %s", output)
	}

	run(disable, desktop.Name)
	configPath := agentfiles.ConfigPath(home, desktop.Name)
	if enabled, found, err := readJSONBoolPath(configPath, []string{"enabled"}); err != nil || !found || enabled {
		t.Fatalf("disabling Desktop failed: %v, %v, %v", enabled, found, err)
	}
	if output := run(update, desktop.Name, "--yes"); !strings.Contains(output, "runtime and plugin ZIP refreshed") {
		t.Fatalf("unexpected update result: %s", output)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatalf("update did not restore the plugin ZIP: %v", err)
	}
	if enabled, found, err := readJSONBoolPath(configPath, []string{"enabled"}); err != nil || !found || enabled {
		t.Fatalf("update changed the disabled configuration: %v, %v, %v", enabled, found, err)
	}
	run(enable, desktop.Name)
	if enabled, found, err := readJSONBoolPath(configPath, []string{"enabled"}); err != nil || !found || !enabled {
		t.Fatalf("enabling Desktop failed: %v, %v, %v", enabled, found, err)
	}
	if err := os.Remove(bundle); err != nil {
		t.Fatal(err)
	}
	if output := run(remove, desktop.Name, "--yes"); !strings.Contains(output, "Remove the OBS plugin from Customize > Plugins") {
		t.Fatalf("missing manual unregister instructions: %s", output)
	}
	if _, err := os.Stat(agentfiles.Directory(home, desktop.Name)); !os.IsNotExist(err) {
		t.Fatalf("managed files remain after remove: %v", err)
	}
}
