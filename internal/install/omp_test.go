package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ompbridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/bridge"
)

func TestOMPInstallUpdateAndRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "custom-agent"))
	exe := filepath.Join(home, "bin with spaces", "obs-agent-connector")
	options := CodexOptions{Home: home, SourceExecutable: exe, Endpoint: "https://example.invalid", InstallType: "gtrace", XToken: "test-token"}
	result, err := InstallOMP(options)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(result.HooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(body), ompbridge.ExtensionMarker) || strings.Contains(string(body), "__CONNECTOR_") || !strings.Contains(string(body), "bin with spaces") {
		t.Fatal("invalid extension")
	}
	before, err := os.ReadFile(result.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(filepath.Dir(result.HooksFile), "other.js")
	os.WriteFile(unrelated, []byte("// unrelated"), 0600)
	options.NoConfig = true
	options.Endpoint = "https://must-not-apply.invalid"
	if _, err := InstallOMP(options); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(result.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("update changed config")
	}
	if _, err := RemoveAdapter("omp", home, RemoveOptions{PurgeState: true, PurgeManaged: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.HooksFile); !os.IsNotExist(err) {
		t.Fatal("extension remains")
	}
	if _, err := os.Stat(result.ConfigFile); !os.IsNotExist(err) {
		t.Fatal("managed config remains")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated extension removed")
	}
}

func TestOMPRefusesUnmanagedExtension(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "")
	path := ompbridge.ExtensionPath(home)
	os.MkdirAll(filepath.Dir(path), 0700)
	os.WriteFile(path, []byte("// user-owned"), 0600)
	if _, err := InstallOMP(CodexOptions{Home: home, SourceExecutable: "/test/connector"}); err == nil {
		t.Fatal("overwrote unmanaged extension")
	}
	if _, err := RemoveAdapter("omp", home, RemoveOptions{}); err == nil {
		t.Fatal("removed unmanaged extension")
	}
}
