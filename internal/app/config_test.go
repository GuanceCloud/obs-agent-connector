package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConnectorConfigAcceptsUTF8BOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := append(
		[]byte{0xEF, 0xBB, 0xBF},
		[]byte(`{"download_base_url":"https://static.example.com/obs-agent-connector","endpoint":"https://example.com","x_token":"test-token"}`)...,
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
}
