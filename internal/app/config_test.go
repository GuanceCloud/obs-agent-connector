package app

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestLoadConnectorConfigAcceptsUTF8BOM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	data := append(
		[]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"download_base_url":"https://static.example.com/obs-agent-connector","endpoint":"https://example.com","x_token":"test-token","global_tags":["team=platform","env=prod"]}`)...,
	)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", path)

	cfg, loadedPath, err := loadConnectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != path {
		t.Fatalf("expected config path %q, got %q", path, loadedPath)
	}
	if cfg.DownloadBaseURL != "https://static.example.com/obs-agent-connector" {
		t.Fatalf("unexpected download base URL %q", cfg.DownloadBaseURL)
	}
	if cfg.Endpoint != "https://example.com" {
		t.Fatalf("unexpected endpoint %q", cfg.Endpoint)
	}
	if cfg.XToken != "test-token" {
		t.Fatalf("unexpected x-token %q", cfg.XToken)
	}
	if len(cfg.GlobalTags) != 2 || cfg.GlobalTags[0] != "team=platform" || cfg.GlobalTags[1] != "env=prod" {
		t.Fatalf("unexpected global tags %#v", cfg.GlobalTags)
	}
}

func TestLoadConnectorConfigAcceptsUTF16LEBOM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.json")
	text := `{"download_base_url":"https://static.example.com/obs-agent-connector","endpoint":"https://example.com","x_token":"test-token"}`
	encoded := encodeUTF16WithBOM(text, binary.LittleEndian, []byte{0xFF, 0xFE})
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", path)

	cfg, loadedPath, err := loadConnectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loadedPath != path {
		t.Fatalf("expected config path %q, got %q", path, loadedPath)
	}
	if cfg.DownloadBaseURL != "https://static.example.com/obs-agent-connector" {
		t.Fatalf("unexpected download base URL %q", cfg.DownloadBaseURL)
	}
	if cfg.Endpoint != "https://example.com" {
		t.Fatalf("unexpected endpoint %q", cfg.Endpoint)
	}
	if cfg.XToken != "test-token" {
		t.Fatalf("unexpected x-token %q", cfg.XToken)
	}
}

func TestLoadConnectorConfigFallsBackToLegacyTelemetryDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, ".obs-agent-connector", "config.json"))
	legacyPath := filepath.Join(home, ".agent-telemetry", "config.json")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "endpoint": "https://legacy.example.com",
  "trace_path": "v1/write/otel-llm",
  "metrics_path": "v1/write/otel-metrics",
  "x_token": "legacy-token",
  "headers": {"To-Headless": "true"},
  "resource_attributes": {"env": "prod", "team": "platform"},
  "capture_content": "none",
  "max_chars": 4096,
  "enabled": false
}`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := loadConnectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://legacy.example.com" || cfg.XToken != "legacy-token" {
		t.Fatalf("legacy endpoint/token were not loaded: %#v", cfg)
	}
	if cfg.TracePath != "v1/write/otel-llm" || cfg.MetricsPath != "v1/write/otel-metrics" {
		t.Fatalf("legacy signal paths were not loaded: %#v", cfg)
	}
	if cfg.CaptureContent != "none" || cfg.MaxChars != 4096 || cfg.Enabled == nil || *cfg.Enabled {
		t.Fatalf("legacy privacy settings were not loaded: %#v", cfg)
	}
	if strings.Join(cfg.GlobalTags, ",") != "env=prod,team=platform" {
		t.Fatalf("legacy resource attributes were not loaded: %#v", cfg.GlobalTags)
	}
}

func TestConnectorConfigOverridesLegacyDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	connectorPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", connectorPath)
	legacyPath := filepath.Join(home, ".agent-telemetry", "config.json")
	for _, path := range []string{connectorPath, legacyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(connectorPath, []byte(`{"endpoint":"https://connector.example.com","x_token":"connector-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{"endpoint":"https://legacy.example.com","x_token":"legacy-token","capture_content":"preview"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := loadConnectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://connector.example.com" || cfg.XToken != "connector-token" {
		t.Fatalf("connector values must override legacy defaults: %#v", cfg)
	}
	if cfg.CaptureContent != "preview" {
		t.Fatalf("missing connector fields should inherit legacy defaults: %#v", cfg)
	}
}

func TestMalformedLegacyConfigDoesNotBlockConnector(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	connectorPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	legacyPath := filepath.Join(home, ".agent-telemetry", "config.json")
	for _, path := range []string{connectorPath, legacyPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", connectorPath)
	if err := os.WriteFile(connectorPath, []byte(`{"endpoint":"https://connector.example.com","x_token":"connector-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte(`{invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := loadConnectorConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Endpoint != "https://connector.example.com" || cfg.XToken != "connector-token" {
		t.Fatalf("connector config was not retained: %#v", cfg)
	}
}

func TestPluginDownloadSettingsUsesConfigForGitHub(t *testing.T) {
	cfg := connectorConfig{
		PluginSource:  "github",
		PluginBaseURL: "https://github.com/GuanceCloud/",
	}
	download, err := pluginDownloadSettings("", cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if download.Source != pluginSourceGitHub {
		t.Fatalf("expected source %q, got %q", pluginSourceGitHub, download.Source)
	}
	if download.BaseURL != "https://github.com/GuanceCloud" {
		t.Fatalf("unexpected plugin base URL %q", download.BaseURL)
	}
}

func TestPluginDownloadSettingsAddsOSSDirectory(t *testing.T) {
	for _, base := range []string{
		"https://static.example.com",
		"https://static.example.com/agent_plugins",
	} {
		download, err := pluginDownloadSettings(base, connectorConfig{}, "")
		if err != nil {
			t.Fatal(err)
		}
		if download.Source != pluginSourceOSS || download.BaseURL != "https://static.example.com/agent_plugins" {
			t.Fatalf("unexpected plugin download config: %#v", download)
		}
	}
}

func TestPluginDownloadSettingsRejectsGitHubWithoutBaseURL(t *testing.T) {
	_, err := pluginDownloadSettings("", connectorConfig{PluginSource: "github"}, "")
	if err == nil {
		t.Fatal("expected plugin_base_url validation error")
	}
}

func encodeUTF16WithBOM(value string, order binary.ByteOrder, bom []byte) []byte {
	words := utf16.Encode([]rune(value))
	data := make([]byte, len(bom)+len(words)*2)
	copy(data, bom)
	offset := len(bom)
	for _, word := range words {
		order.PutUint16(data[offset:offset+2], word)
		offset += 2
	}
	return data
}
