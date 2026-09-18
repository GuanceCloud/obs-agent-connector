package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pibridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/bridge"
)

func TestPiInstallUpdateAndRemovePreservesSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	settingsPath := pibridge.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"theme":"dark","extensions":["/keep/extension.ts"],"unknown":{"keep":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(home, "bin with spaces", "obs-agent-connector")
	options := CodexOptions{Home: home, SourceExecutable: executable, Endpoint: "https://example.invalid", InstallType: "gtrace", XToken: "test-token"}
	result, err := InstallPi(options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(result.HooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), pibridge.ExtensionMarker) || strings.Contains(string(body), "__CONNECTOR_") || !strings.Contains(string(body), "bin with spaces") {
		t.Fatal("invalid extension")
	}
	var settings map[string]any
	settingsBody, _ := os.ReadFile(settingsPath)
	if err := json.Unmarshal(settingsBody, &settings); err != nil {
		t.Fatal(err)
	}
	extensions, _ := settings["extensions"].([]any)
	if !containsPath(extensions, "/keep/extension.ts") || !containsPath(extensions, result.HooksFile) || settings["theme"] != "dark" {
		t.Fatalf("settings were not merged: %#v", settings)
	}
	before, _ := os.ReadFile(result.ConfigFile)
	options.NoConfig = true
	options.Endpoint = "https://must-not-apply.invalid"
	if _, err := InstallPi(options); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(result.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("update changed config")
	}
	if _, err := RemoveAdapter("pi", home, RemoveOptions{PurgeState: true, PurgeManaged: true}); err != nil {
		t.Fatal(err)
	}
	settingsBody, _ = os.ReadFile(settingsPath)
	if err := json.Unmarshal(settingsBody, &settings); err != nil {
		t.Fatal(err)
	}
	extensions, _ = settings["extensions"].([]any)
	if !containsPath(extensions, "/keep/extension.ts") || containsPath(extensions, result.HooksFile) {
		t.Fatalf("remove damaged settings: %#v", settings)
	}
}

func TestPiRefusesUnmanagedExtensionAndInvalidSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	path := pibridge.ExtensionPath(home)
	settingsPath := pibridge.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("// user-owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	settingsBefore := []byte(`{"extensions":["` + path + `"],"theme":"dark"}`)
	if err := os.WriteFile(settingsPath, settingsBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPi(CodexOptions{Home: home, SourceExecutable: "/test/connector"}); err == nil {
		t.Fatal("overwrote unmanaged extension")
	}
	if _, err := RemoveAdapter("pi", home, RemoveOptions{}); err == nil {
		t.Fatal("removed unmanaged extension")
	}
	settingsAfter, err := os.ReadFile(settingsPath)
	if err != nil || string(settingsAfter) != string(settingsBefore) {
		t.Fatal("unmanaged extension refusal changed Pi settings")
	}

	home = t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "pi-agent"))
	settingsPath = pibridge.SettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"extensions":"broken"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallPi(CodexOptions{Home: home, SourceExecutable: "/test/connector"}); err == nil {
		t.Fatal("invalid extensions value was overwritten")
	}
}
