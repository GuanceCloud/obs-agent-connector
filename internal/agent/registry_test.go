package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegisteredPluginNames(t *testing.T) {
	expected := map[string]string{
		"claude":    "claude-otel-plugin",
		"codex":     "codex-otel-plugin",
		"hermes":    "hermes-otel-plugin",
		"openclaw":  "openclaw-otel-plugin",
		"qoder":     "qoder-otel-plugin",
		"qoder-cn":  "qoder-otel-plugin",
		"workbuddy": "workbuddy-otel-plugin",
	}

	for name, pluginName := range expected {
		definition, ok := definitions[name]
		if !ok {
			t.Fatalf("missing Agent definition %q", name)
		}
		if definition.PluginName != pluginName {
			t.Fatalf("expected %s plugin name %q, got %q", name, pluginName, definition.PluginName)
		}
		assertNoMigrationArtifact(t, definition)
	}
}

func TestSupportedNamesForWindows(t *testing.T) {
	expected := []string{"codex", "openclaw", "qoder", "workbuddy"}
	got := SupportedNames("windows")
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected Windows supported names %v, got %v", expected, got)
	}
}

func TestWindowsSupportFlags(t *testing.T) {
	cases := map[string]bool{
		"claude":    false,
		"codex":     true,
		"hermes":    false,
		"openclaw":  true,
		"qoder":     true,
		"qoder-cn":  true,
		"workbuddy": true,
	}

	for name, expected := range cases {
		definition := definitions[name]
		if got := SupportsPlatform(definition, "windows"); got != expected {
			t.Fatalf("expected %s windows support %t, got %t", name, expected, got)
		}
	}
}

func TestDiscoverCandidatesIncludesQoderWithoutCommandWhenDataDirExists(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})

	if err := os.MkdirAll(filepath.Join(home, ".qoder"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := DiscoverCandidatesForOS("linux")
	for _, candidate := range candidates {
		if candidate.Plugin.Name != "qoder" {
			continue
		}
		if candidate.DetectedCmd != "data-dir" {
			t.Fatalf("expected qoder detect source data-dir, got %q", candidate.DetectedCmd)
		}
		return
	}
	t.Fatal("expected qoder to be discoverable from data dir")
}

func TestDiscoverCandidatesIncludesWorkBuddyWithoutCommandWhenProfileDirExists(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})

	if err := os.MkdirAll(filepath.Join(home, ".workbuddy"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := DiscoverCandidatesForOS("linux")
	for _, candidate := range candidates {
		if candidate.Plugin.Name != "workbuddy" {
			continue
		}
		if candidate.DetectedCmd != "data-dir" {
			t.Fatalf("expected workbuddy detect source data-dir, got %q", candidate.DetectedCmd)
		}
		return
	}
	t.Fatal("expected workbuddy to be discoverable from profile dir")
}

func assertNoMigrationArtifact(t *testing.T, definition Definition) {
	t.Helper()
	values := append([]string{definition.PluginName}, definition.Markers...)
	values = append(values, definition.ConfigFiles...)
	values = append(values, definition.RemovePaths...)
	for _, command := range definition.RemoveCmds {
		values = append(values, command...)
	}
	for _, value := range values {
		if strings.Contains(value, "Definition") {
			t.Fatalf("Agent %s contains invalid migrated value %q", definition.Name, value)
		}
	}
}
