package buildinfo

import (
	"strings"
	"testing"
)

func TestVersionIsAvailable(t *testing.T) {
	if strings.TrimSpace(Version) == "" {
		t.Fatal("build version must not be empty")
	}
}
