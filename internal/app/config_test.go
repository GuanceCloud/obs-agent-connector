package app

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"
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

func TestLoadConnectorConfigAcceptsUTF16LEBOM(t *testing.T) {
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
