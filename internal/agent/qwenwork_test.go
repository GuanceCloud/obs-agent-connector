package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQwenWorkPlatforms(t *testing.T) {
	p := Get("qwenwork-cn")
	for _, goos := range []string{"darwin", "linux", "windows", "freebsd"} {
		if got, want := SupportsPlatform(p, goos), goos != "freebsd"; got != want {
			t.Errorf("SupportsPlatform(%q) = %t, want %t", goos, got, want)
		}
	}
}

func TestQwenWorkLinuxDiscoveryUsesProfile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	for _, exists := range []bool{false, true} {
		if exists {
			if err := os.Mkdir(filepath.Join(home, ".qwenworkcn"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		found := false
		for _, candidate := range DiscoverCandidatesForOS("linux") {
			if candidate.Plugin.Name == "qwenwork-cn" {
				found = true
				if !candidate.Supported || !candidate.Plugin.IsBuiltin() || candidate.DetectedCmd != "data-dir" {
					t.Fatalf("unexpected candidate: %+v", candidate)
				}
			}
		}
		if found != exists {
			t.Fatalf("discovered = %t, profile exists = %t", found, exists)
		}
	}
}
