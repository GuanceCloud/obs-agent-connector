package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestWorkBuddyBuiltinMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	profile := filepath.Join(home, "WorkBuddy Profile")
	t.Setenv("WORKBUDDY_CONFIG_DIR", profile)
	t.Setenv("CODEBUDDY_CONFIG_DIR", "")
	os.MkdirAll(profile, 0700)
	executable := filepath.Join(home, "obs-agent-connector")
	os.WriteFile(executable, []byte("test"), 0700)
	previous := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = previous })
	p := agent.Resolve(agent.Get("workbuddy"))
	if !p.IsBuiltin() {
		t.Fatal("WorkBuddy is not built in")
	}
	output := captureStdout(t, func() {
		if err := installBuiltinAdapter(p, installInput{Endpoint: "https://example.invalid", XToken: "test-secret"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "Download:") || strings.Contains(output, "test-secret") {
		t.Fatal("external download or secret in output")
	}
	if _, ok := agent.InstalledMarker(p); !ok {
		t.Fatal("built-in installation not detected")
	}
	if err := installBuiltinAdapter(p, installInput{}, true); err != nil {
		t.Fatal(err)
	}
}
