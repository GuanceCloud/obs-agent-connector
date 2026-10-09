//go:build !windows

package parse

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestSkillMetadataRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if skillMetadata(path) != nil {
		t.Fatal("FIFO metadata accepted")
	}
}
