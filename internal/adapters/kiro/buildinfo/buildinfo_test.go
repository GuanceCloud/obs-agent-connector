package buildinfo

import "testing"

func TestVersionIsNotEmpty(t *testing.T) {
	if Version == "" {
		t.Fatal("build version must not be empty")
	}
}
