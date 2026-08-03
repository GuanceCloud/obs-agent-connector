package sidecar

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultLockStaleMs = 120_000
	LegacyMarker       = "legacy"
)

type RolloutLock struct {
	LockFile string
}

func LoadUploadedTurnStates(rolloutFile string) (map[string]string, error) {
	data, err := os.ReadFile(rolloutFile + ".gtrace")
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		turnID := strings.TrimSpace(parts[0])
		if turnID == "" {
			continue
		}
		fingerprint := LegacyMarker
		if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
			fingerprint = strings.TrimSpace(parts[1])
		}
		out[turnID] = fingerprint
	}
	return out, nil
}

func MarkTurnUploaded(rolloutFile, turnID, fingerprint string) {
	file, err := os.OpenFile(rolloutFile+".gtrace", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.WriteString(turnID + "\t" + fingerprint + "\n")
}

func IsLegacyTurnState(state string) bool {
	return state == LegacyMarker
}

func AcquireRolloutLock(rolloutFile string, staleMs int) (*RolloutLock, error) {
	if staleMs <= 0 {
		staleMs = DefaultLockStaleMs
	}
	lockFile := rolloutFile + ".gtrace.lock"
	payload := fmt.Sprintf("{\"pid\":%d,\"created_at\":\"%s\"}\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))

	for {
		file, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			if _, writeErr := file.WriteString(payload); writeErr != nil {
				file.Close()
				_ = os.Remove(lockFile)
				return nil, writeErr
			}
			if closeErr := file.Close(); closeErr != nil {
				_ = os.Remove(lockFile)
				return nil, closeErr
			}
			return &RolloutLock{LockFile: lockFile}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}

		stats, statErr := os.Stat(lockFile)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, statErr
		}
		ageMs := time.Since(stats.ModTime()).Milliseconds()
		if ageMs > int64(staleMs) {
			if removeErr := os.Remove(lockFile); removeErr != nil && !os.IsNotExist(removeErr) {
				return nil, removeErr
			}
			continue
		}
		return nil, nil
	}
}

func ReleaseRolloutLock(lock *RolloutLock) error {
	if lock == nil || lock.LockFile == "" {
		return nil
	}
	err := os.Remove(lock.LockFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func ParseLockStaleMs(value string) int {
	if strings.TrimSpace(value) == "" {
		return DefaultLockStaleMs
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return DefaultLockStaleMs
	}
	return parsed
}
