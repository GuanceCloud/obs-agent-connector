package hooklog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendWritesStructuredJSONL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "gtrace-hook.log")
	if err := Append(path, UploadedSpans, map[string]any{"spans": 3, "status": 200}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entry struct {
		Timestamp string         `json:"ts"`
		Message   string         `json:"message"`
		Extra     map[string]any `json:"extra"`
	}
	if err := json.Unmarshal(body, &entry); err != nil {
		t.Fatalf("decode log entry: %v", err)
	}
	if entry.Timestamp == "" || entry.Message != UploadedSpans || entry.Extra["status"] != float64(200) {
		t.Fatalf("unexpected log entry: %#v", entry)
	}
}

func TestAppendIgnoresEmptyPath(t *testing.T) {
	if err := Append("", HookInvoked, nil); err != nil {
		t.Fatal(err)
	}
}
