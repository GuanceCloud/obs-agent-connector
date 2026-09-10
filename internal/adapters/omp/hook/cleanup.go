package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
)

const queueRetention = 7 * 24 * time.Hour
const stateRetention = 30 * 24 * time.Hour

// Cleanup is opportunistic and touches only OMP-managed files. A failed
// removal leaves the file for a later invocation; it does not block telemetry.
func cleanup(cfg config.Config, now time.Time) {
	pending := map[string]bool{}
	queue := filepath.Join(cfg.StateDir, "queue")
	entries, _ := os.ReadDir(queue)
	expired := 0
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || (!strings.HasSuffix(entry.Name(), ".json") && !strings.HasSuffix(entry.Name(), ".json.tmp")) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(queue, entry.Name())
		if now.Sub(info.ModTime()) > queueRetention {
			if os.Remove(path) == nil {
				expired++
				continue
			}
		}
		if info.Size() > 9*1024*1024 {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		snapshot, err := parse.DecodeSnapshot(body)
		if err != nil {
			continue
		}
		key := sha256.Sum256([]byte(snapshot.SessionID + "\x00" + snapshot.TurnID))
		pending[hex.EncodeToString(key[:])] = true
	}
	uploads := filepath.Join(cfg.StateDir, "uploads")
	entries, _ = os.ReadDir(uploads)
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || pending[entry.Name()] || len(entry.Name()) != 64 {
			continue
		}
		if _, err := hex.DecodeString(entry.Name()); err != nil {
			continue
		}
		dir := filepath.Join(uploads, entry.Name())
		// Keep live claims; abandoned claims without pending snapshots can expire.
		if claim, err := os.Lstat(filepath.Join(dir, "claim.json")); err == nil {
			if now.Sub(claim.ModTime()) <= 5*time.Minute {
				continue
			}
		} else if !os.IsNotExist(err) {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= stateRetention {
			continue
		}
		_ = os.RemoveAll(dir)
	}
	if expired > 0 {
		appendLog(cfg.LogFile, "expired OMP queue files", map[string]any{"files": expired})
	}
}

// Keep the current log and at most one previous file, each approximately 5 MiB.
func appendLog(path, message string, fields map[string]any) {
	if path == "" {
		return
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return
		}
		if info.Size() >= 5*1024*1024 {
			_ = os.Remove(path + ".1")
			_ = os.Rename(path, path+".1")
		}
	}
	_ = hooklog.Append(path, message, fields)
}
