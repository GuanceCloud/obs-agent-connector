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
		"codebuddy": "obs-agent-connector",
		"codex":     "codex-otel-plugin",
		"hermes":    "hermes-otel-plugin",
		"opencode":  "opencode-otel-plugin",
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
	expected := []string{"codebuddy", "codex", "openclaw", "opencode", "qoder", "workbuddy"}
	got := SupportedNames("windows")
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected Windows supported names %v, got %v", expected, got)
	}
}

func TestSupportedNamesForLinux(t *testing.T) {
	expected := []string{"claude", "codebuddy", "codex", "hermes", "openclaw", "opencode", "qoder"}
	got := SupportedNames("linux")
	if strings.Join(got, ",") != strings.Join(expected, ",") {
		t.Fatalf("expected Linux supported names %v, got %v", expected, got)
	}
}

func TestWindowsSupportFlags(t *testing.T) {
	cases := map[string]bool{
		"claude":    false,
		"codebuddy": true,
		"codex":     true,
		"hermes":    false,
		"opencode":  true,
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

func TestSelectForRuntimeKeepsLegacyByDefaultAndOptsIntoBuiltin(t *testing.T) {
	legacy, err := SelectForRuntime("codex", false)
	if err != nil {
		t.Fatal(err)
	}
	if legacy[0].IsBuiltin() || legacy[0].PluginName != "codex-otel-plugin" {
		t.Fatalf("default must keep the legacy plugin: %#v", legacy[0])
	}

	builtin, err := SelectForRuntime("codex", true)
	if err != nil {
		t.Fatal(err)
	}
	if !builtin[0].IsBuiltin() || builtin[0].PluginName != "obs-agent-connector" || builtin[0].Markers[0] != "~/.codex/hooks.json" {
		t.Fatalf("-n must select the built-in runtime: %#v", builtin[0])
	}
}

func TestCodeBuddyUsesBuiltinRuntimeWithoutNewMode(t *testing.T) {
	selected, err := SelectForRuntime("codebuddy", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 1 || !selected[0].IsBuiltin() || selected[0].PluginName != "obs-agent-connector" {
		t.Fatalf("unexpected CodeBuddy definition: %#v", selected)
	}
}

func TestSelectForRuntimeRejectsUnsupportedAgent(t *testing.T) {
	_, err := SelectForRuntime("opencode", true)
	if err == nil || !strings.Contains(err.Error(), "opencode does not support -n/--new-runtime; supported agents: claude, codex") {
		t.Fatalf("expected unsupported new runtime error, got %v", err)
	}
}

func TestBuiltinClaudeSupportsWindowsWithoutChangingLegacySupport(t *testing.T) {
	legacy := Get("claude")
	builtin, ok := legacy.WithBuiltin()
	if !ok {
		t.Fatal("claude must support the built-in runtime")
	}
	if SupportsPlatform(legacy, "windows") {
		t.Fatal("legacy Claude installer must remain unsupported on Windows")
	}
	if !SupportsPlatform(builtin, "windows") {
		t.Fatal("built-in Claude runtime must support Windows")
	}
}

func TestDiscoverNewRuntimeMarksUnsupportedAgentWithoutExternalFallback(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	for _, name := range []string{"codex", "opencode"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	candidates := DiscoverCandidatesForOSRuntime("linux", true)
	found := map[string]Candidate{}
	for _, candidate := range candidates {
		found[candidate.Plugin.Name] = candidate
	}
	codex, ok := found["codex"]
	if !ok || !codex.Supported || !codex.Plugin.IsBuiltin() {
		t.Fatalf("expected supported built-in Codex candidate, got %#v", codex)
	}
	opencode, ok := found["opencode"]
	if !ok {
		t.Fatal("expected detected OpenCode candidate")
	}
	if opencode.Supported || opencode.Plugin.IsBuiltin() {
		t.Fatalf("OpenCode must not fall back to its external plugin in -n mode: %#v", opencode)
	}
	if !strings.Contains(opencode.UnsupportedReason, "new runtime is not supported") {
		t.Fatalf("expected explicit new-runtime reason, got %q", opencode.UnsupportedReason)
	}
}

func TestLinuxSupportFlags(t *testing.T) {
	cases := map[string]bool{
		"claude":    true,
		"codebuddy": true,
		"codex":     true,
		"hermes":    true,
		"opencode":  true,
		"openclaw":  true,
		"qoder":     true,
		"qoder-cn":  true,
		"workbuddy": false,
	}

	for name, expected := range cases {
		definition := definitions[name]
		if got := SupportsPlatform(definition, "linux"); got != expected {
			t.Fatalf("expected %s linux support %t, got %t", name, expected, got)
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

	candidates := DiscoverCandidatesForOS("darwin")
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

func TestDiscoverCandidatesSkipsWorkBuddyOnLinux(t *testing.T) {
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
		if candidate.Plugin.Name == "workbuddy" {
			t.Fatal("did not expect workbuddy to be discoverable on linux")
		}
	}
}

func TestDiscoverCandidatesIncludesOpencodeWithoutCommandWhenConfigDirExists(t *testing.T) {
	home := t.TempDir()
	previousHome := os.Getenv("HOME")
	previousPath := os.Getenv("PATH")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("PATH", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
		_ = os.Setenv("PATH", previousPath)
	})

	if err := os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}

	candidates := DiscoverCandidatesForOS("linux")
	for _, candidate := range candidates {
		if candidate.Plugin.Name != "opencode" {
			continue
		}
		if candidate.DetectedCmd != "data-dir" {
			t.Fatalf("expected opencode detect source data-dir, got %q", candidate.DetectedCmd)
		}
		return
	}
	t.Fatal("expected opencode to be discoverable from config dir")
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
