package sidecar

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndMarkUploadedTurnStates(t *testing.T) {
	base := t.TempDir()
	rolloutFile := filepath.Join(base, "session.jsonl")

	MarkTurnUploaded(rolloutFile, "turn-1", "fp-1")
	MarkTurnUploaded(rolloutFile, "turn-2", "")

	states, err := LoadUploadedTurnStates(rolloutFile)
	if err != nil {
		t.Fatal(err)
	}
	if states["turn-1"] != "fp-1" {
		t.Fatalf("unexpected fingerprint for turn-1: %#v", states)
	}
	if states["turn-2"] != LegacyMarker {
		t.Fatalf("expected legacy marker for empty fingerprint, got %#v", states["turn-2"])
	}
}

func TestAcquireAndReleaseRolloutLock(t *testing.T) {
	base := t.TempDir()
	rolloutFile := filepath.Join(base, "session.jsonl")
	lock, err := AcquireRolloutLock(rolloutFile, DefaultLockStaleMs)
	if err != nil {
		t.Fatal(err)
	}
	if lock == nil {
		t.Fatal("expected first lock acquisition to succeed")
	}

	second, err := AcquireRolloutLock(rolloutFile, DefaultLockStaleMs)
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Fatal("expected second lock acquisition to be skipped")
	}

	if err := ReleaseRolloutLock(lock); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(lock.LockFile); !os.IsNotExist(err) {
		t.Fatalf("expected lock file removed, got err=%v", err)
	}
}
