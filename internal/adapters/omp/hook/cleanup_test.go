package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/parse"
)

func TestCancelledMetricKeepsTerminalState(t *testing.T) {
	s, err := parse.DecodeSnapshot(fixtureBody(t))
	if err != nil {
		t.Fatal(err)
	}
	s.Events[len(s.Events)-1].Message.StopReason = "aborted"
	turn, err := parse.Normalize(s, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range buildMetrics(buildSpans(turn)) {
		if m.Name == "gen_ai.workflow.duration" {
			if m.Attributes["final_status"] != "cancelled" || m.Attributes["status"] != "error" {
				t.Fatalf("state collapsed: %v", m.Attributes)
			}
			return
		}
	}
	t.Fatal("workflow metric absent")
}

func TestCleanupRetentionAndActiveClaims(t *testing.T) {
	cfg := testConfig(t)
	cfg.LogFile = filepath.Join(t.TempDir(), "hooks.json")
	now := time.Now()
	old := now.Add(-31 * 24 * time.Hour)
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	queue := queueFixture(t, cfg)
	expired := filepath.Join(cfg.StateDir, "queue", "expired.json")
	write(expired)
	processing := expired + ".tmp"
	write(processing)
	stale := filepath.Join(cfg.StateDir, "uploads", strings.Repeat("a", 64))
	write(filepath.Join(stale, "completed.json"))
	os.Chtimes(stale, old, old)
	active := filepath.Join(cfg.StateDir, "uploads", strings.Repeat("b", 64))
	write(filepath.Join(active, "claim.json"))
	os.Chtimes(filepath.Join(active, "claim.json"), now, now)
	os.Chtimes(active, old, old)
	cleanup(cfg, now)
	for _, path := range []string{expired, processing, stale} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired file retained %s", path)
		}
	}
	for _, path := range []string{queue, active} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("live file deleted %s", path)
		}
	}
	if err := os.WriteFile(cfg.LogFile, make([]byte, 5*1024*1024), 0600); err != nil {
		t.Fatal(err)
	}
	appendLog(cfg.LogFile, "test", nil)
	if _, err := os.Stat(cfg.LogFile + ".1"); err != nil {
		t.Fatal("log not rotated")
	}
}
