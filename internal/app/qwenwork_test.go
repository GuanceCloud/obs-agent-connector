package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestQwenWorkBuiltinMigration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	profile := filepath.Join(home, ".qwenworkcn")
	os.MkdirAll(profile, 0700)
	executable := filepath.Join(home, "obs-agent-connector")
	os.WriteFile(executable, []byte("test"), 0700)
	previous := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = previous })
	p := agent.Resolve(agent.Get("qwenwork-cn"))
	if !p.IsBuiltin() {
		t.Fatal("QwenWork is not built in")
	}
	output := captureStdout(t, func() {
		if err := installBuiltinAdapter(p, installInput{Endpoint: "https://example.invalid", XToken: "test-secret"}, false); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "Download:") || strings.Contains(output, "test-secret") {
		t.Fatal("external download or secret in output")
	}
	if !strings.Contains(output, "QODERCN_EXPOSE_TOKEN_USAGE=1") {
		t.Fatal("token reporting setup missing from install output")
	}
	envPath := filepath.Join(profile, ".env")
	env, err := os.ReadFile(envPath)
	if err != nil || !strings.Contains(string(env), "QODERCN_EXPOSE_TOKEN_USAGE=1") {
		t.Fatal("install did not enable native token reporting")
	}
	if _, ok := agent.InstalledMarker(p); !ok {
		t.Fatal("built-in installation not detected")
	}
	if err := disable([]string{"qwenwork-cn"}); err != nil {
		t.Fatal(err)
	}
	configPath := agent.ExpandHome(p.ConfigFiles[0])
	if readJSONFile(t, configPath)["enabled"] != false {
		t.Fatal("disable failed")
	}
	if err := enable([]string{"qwenwork-cn"}); err != nil {
		t.Fatal(err)
	}
	if readJSONFile(t, configPath)["enabled"] != true {
		t.Fatal("enable failed")
	}
	output = captureStdout(t, func() {
		if err := status([]string{"qwenwork-cn"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "qwenwork-cn") || !strings.Contains(output, "yes") {
		t.Fatal("status missing")
	}
	found := false
	for _, candidate := range agent.DiscoverCandidatesForOS("darwin") {
		if candidate.Plugin.Name == "qwenwork-cn" {
			found = true
		}
	}
	if !found {
		t.Fatal("not discovered")
	}
	if err := os.WriteFile(envPath, []byte("QODERCN_EXPOSE_TOKEN_USAGE=0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output = captureStdout(t, func() {
		if err := installBuiltinAdapter(p, installInput{}, true); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, "QODERCN_EXPOSE_TOKEN_USAGE=1") {
		t.Fatal("update incorrectly claimed to enable token reporting")
	}
	env, err = os.ReadFile(envPath)
	if err != nil || string(env) != "QODERCN_EXPOSE_TOKEN_USAGE=0\n" {
		t.Fatal("update changed native token reporting config")
	}
}
