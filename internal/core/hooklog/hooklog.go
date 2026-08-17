package hooklog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	HookInvoked      = "hook invoked"
	ParsedTranscript = "parsed transcript"
	UploadedSpans    = "uploaded spans"
	UploadedMetrics  = "uploaded metrics"
)

// Append writes one structured JSONL Hook event. All built-in adapters use
// this function so timestamps, envelope fields, permissions, and error
// behavior remain consistent.
func Append(path, message string, extra map[string]any) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	payload := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"message": message,
	}
	if extra != nil {
		payload["extra"] = extra
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(body, '\n'))
	return err
}
