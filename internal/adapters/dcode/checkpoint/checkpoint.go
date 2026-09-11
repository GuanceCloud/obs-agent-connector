// Package checkpoint recovers message metadata omitted by DCode transcripts.
package checkpoint

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

//go:embed enrich.py
var script string

// Enrich runs in the background collector worker, never in the agent hook.
// Missing Python dependencies, incompatible databases, and timeouts fail open.
func Enrich(sessionID, transcript string) string {
	if sessionID == "" || transcript == "" {
		return transcript
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return transcript
	}
	if _, err := os.Stat(filepath.Join(home, ".deepagents", ".state", "sessions.db")); err != nil {
		return transcript
	}
	python := pythonExecutable()
	if python == "" {
		return transcript
	}
	payload := map[string]string{"session_id": sessionID, "transcript_path": transcript}
	input, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", script)
	command.Stdin = bytes.NewReader(input)
	output, err := command.Output()
	if err != nil {
		return transcript
	}
	var enriched map[string]string
	if json.Unmarshal(output, &enriched) != nil || enriched["transcript_path"] == "" {
		return transcript
	}
	return enriched["transcript_path"]
}

func pythonExecutable() string {
	if path := os.Getenv("DCODE_OTEL_PYTHON"); path != "" {
		return path
	}
	if dcode, err := exec.LookPath("dcode"); err == nil {
		if resolved, err := filepath.EvalSymlinks(dcode); err == nil {
			dcode = resolved
		}
		names := []string{"python", "python3"}
		if runtime.GOOS == "windows" {
			names = []string{"python.exe"}
		}
		for _, name := range names {
			path := filepath.Join(filepath.Dir(dcode), name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path
			}
		}
	}
	path, _ := exec.LookPath("python3")
	return path
}
