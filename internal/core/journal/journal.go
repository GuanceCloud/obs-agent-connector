// Package journal provides bounded, atomic, cross-process JSON transactions.
package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
)

var ErrBusy = errors.New("journal is busy")

type File[T any] struct {
	Root     string
	MaxBytes int64
}

// Update commits only on a successful callback. Network I/O must stay outside
// this short transaction. A crashed transaction lock expires after ten minutes.
func (f File[T]) Update(update func(*T) error) error {
	return f.transaction(update, true)
}

func (f File[T]) View(view func(*T) error) error {
	return f.transaction(view, false)
}

func (f File[T]) transaction(update func(*T) error, commit bool) error {
	if f.Root == "" {
		return errors.New("journal root is empty")
	}
	manager := state.Manager{Root: filepath.Join(f.Root, "locks"), StaleAfter: 10 * time.Minute}
	claim, err := manager.Claim("journal", "transaction", "")
	if err != nil {
		return err
	}
	if claim == nil {
		return ErrBusy
	}
	defer claim.Release()
	maxBytes := f.MaxBytes
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	var value T
	file, err := os.Open(filepath.Join(f.Root, "journal.json"))
	if err == nil {
		body, readErr := io.ReadAll(io.LimitReader(file, maxBytes+1))
		file.Close()
		if readErr != nil {
			return readErr
		}
		if int64(len(body)) > maxBytes {
			return fmt.Errorf("journal exceeds byte limit")
		}
		if err := json.Unmarshal(body, &value); err != nil {
			return fmt.Errorf("decode journal: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := update(&value); err != nil {
		return err
	}
	if !commit {
		return nil
	}
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("journal exceeds byte limit")
	}
	return WriteAtomic(filepath.Join(f.Root, "journal.json"), body)
}

func WriteAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".journal-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	// Directory sync is unavailable on some platforms; the file is already synced.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
