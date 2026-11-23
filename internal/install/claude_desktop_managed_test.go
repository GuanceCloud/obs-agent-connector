//go:build darwin || linux

package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func TestManagedClaudeDesktopLifecycle(t *testing.T) {
	s, source := testManagedDesktopSystem(t)
	if err := s.apply("update", source); err == nil || !strings.Contains(err.Error(), "files are absent") {
		t.Fatalf("update without an installation: %v", err)
	}
	if err := s.apply("install", source); err != nil {
		t.Fatal(err)
	}
	if state, err := s.inspect(); err != nil || !state.Policy || !state.Runtime {
		t.Fatalf("installed system state = %+v, %v", state, err)
	}
	if info, err := os.Stat(s.executable()); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("runtime mode: %v, %v", info, err)
	}
	if info, err := os.Stat(s.policy()); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("policy mode: %v, %v", info, err)
	}
	policy := readManagedTestFile(t, s.policy())
	var parsed struct {
		Parent string `json:"parentSettingsBehavior"`
		Hooks  map[string][]struct {
			Handlers []struct {
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(policy, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Parent != "merge" || len(parsed.Hooks) != 2 {
		t.Fatalf("first managed policy: %s", policy)
	}
	for _, event := range []string{"Stop", "SessionEnd"} {
		groups := parsed.Hooks[event]
		if len(groups) != 1 || len(groups[0].Handlers) != 1 {
			t.Fatalf("wrong handlers for %s: %s", event, policy)
		}
		command := groups[0].Handlers[0]
		if command.Command != s.executable() || strings.Join(command.Args, " ") != "hook claude-desktop --managed" {
			t.Fatalf("wrong command for %s: %+v", event, command)
		}
	}
	if err := s.apply("install", source); err != nil {
		t.Fatal(err)
	}
	if got := readManagedTestFile(t, s.policy()); !bytes.Equal(got, policy) {
		t.Fatalf("reinstall changed the policy: %s", got)
	}

	adminPath := filepath.Join(s.dropIns(), "10-admin.json")
	adminPolicy := `{"parentSettingsBehavior":"first-wins","permissions":{"deny":["WebSearch"]},"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/bin/true"}]}]}}`
	writeManagedTestFile(t, adminPath, adminPolicy, 0o644)
	writeManagedTestFile(t, source, "OBS version two", 0o755)
	if err := s.apply("update", source); err != nil {
		t.Fatal(err)
	}
	if merge, exists, err := s.ownedPolicy(); err != nil || !exists || merge {
		t.Fatalf("update must leave the existing administrator's parent setting effective: %v, %v, %v", merge, exists, err)
	}
	if got := readManagedTestFile(t, s.executable()); string(got) != "OBS version two" {
		t.Fatalf("runtime did not update: %s", got)
	}
	for range 2 {
		if err := s.apply("remove", ""); err != nil {
			t.Fatal(err)
		}
	}
	if state, err := s.inspect(); err != nil || state.Policy || state.Runtime {
		t.Fatalf("removed system state = %+v, %v", state, err)
	}
	if got := readManagedTestFile(t, adminPath); string(got) != adminPolicy {
		t.Fatalf("administrator's policy changed: %s", got)
	}
}

func TestManagedClaudeDesktopConflictsLeaveExistingFilesIntact(t *testing.T) {
	for _, tc := range []struct {
		name, filename, content, want string
		mode                          os.FileMode
	}{
		{"foreign slot", "managed-settings.d/" + desktop.ManagedDropIn, `{"model":"sonnet"}`, "outside the OBS registration", 0o644},
		{"hooks disabled", "managed-settings.json", `{"disableAllHooks":true}`, "disables Hooks", 0o644},
		{"external policy helper", "managed-settings.json", `{"policyHelper":"/usr/local/bin/get-policy"}`, "configures policyHelper", 0o644},
		{"invalid parent mode", "managed-settings.json", `{"parentSettingsBehavior":[]}`, "cannot safely interpret parentSettingsBehavior", 0o644},
		{"writable policy", "managed-settings.json", `{}`, "cannot safely inspect", 0o664},
		{"policy unreadable by desktop users", "managed-settings.json", `{}`, "cannot safely inspect", 0o600},
		{"foreign executable", desktop.ManagedRuntime, "unrelated runtime", "foreign executable", 0o755},
		{"non-executable runtime", desktop.ManagedRuntime, "OBS runtime", "unsafe machine runtime", 0o644},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, source := testManagedDesktopSystem(t)
			path := filepath.Join(s.paths.Directory, tc.filename)
			writeManagedTestFile(t, path, tc.content, tc.mode)
			if err := s.apply("install", source); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("install error = %v, want %q", err, tc.want)
			}
			if got := readManagedTestFile(t, path); string(got) != tc.content {
				t.Fatalf("existing file changed: %s", got)
			}
		})
	}
	t.Run("linked policy directory", func(t *testing.T) {
		s, source := testManagedDesktopSystem(t)
		if err := os.Mkdir(s.paths.Directory, 0o755); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, s.dropIns()); err != nil {
			t.Fatal(err)
		}
		if err := s.apply("install", source); err == nil || !strings.Contains(err.Error(), "unsafe machine directory") {
			t.Fatalf("install allowed a symlink parent: %v", err)
		}
		if files, err := os.ReadDir(outside); err != nil || len(files) != 0 {
			t.Fatalf("installer followed symlink: %v, %v", files, err)
		}
	})
	t.Run("machine directory missing account access", func(t *testing.T) {
		s, source := testManagedDesktopSystem(t)
		if err := os.Mkdir(s.paths.Directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := s.apply("install", source); err == nil || !strings.Contains(err.Error(), "read and traverse") {
			t.Fatalf("install allowed a system path unavailable to Desktop accounts: %v", err)
		}
		assertManagedTestFileAbsent(t, s.executable())
	})
	t.Run("higher-priority device policy", func(t *testing.T) {
		s, source := testManagedDesktopSystem(t)
		blocked := errors.New("simulated higher-priority policy")
		s.checkPriority = func() error { return blocked }
		if err := s.apply("install", source); !errors.Is(err, blocked) {
			t.Fatalf("higher-priority device policy ignored: %v", err)
		}
		assertManagedTestFileAbsent(t, s.paths.Directory)
	})
}

func TestManagedClaudeDesktopMergesParentWithEmptyMachinePolicy(t *testing.T) {
	s, source := testManagedDesktopSystem(t)
	writeManagedTestFile(t, filepath.Join(s.paths.Directory, "managed-settings.json"), `{"parentSettingsBehavior":"first-wins","model":"sonnet"}`, 0o644)
	writeManagedTestFile(t, filepath.Join(s.dropIns(), "10-empty.json"), "\n", 0o644)
	writeManagedTestFile(t, filepath.Join(s.dropIns(), "20-policy.json"), `{"parentSettingsBehavior":null,"model":null,"disableAllHooks":null,"policyHelper":null,"managedSourcesBehavior":"merge"}`, 0o644)
	if err := s.apply("install", source); err != nil {
		t.Fatal(err)
	}
	if merge, exists, err := s.ownedPolicy(); !merge || !exists || err != nil {
		t.Fatalf("empty machine settings should leave parent merging enabled: %v, %v, %v", merge, exists, err)
	}
}

func TestManagedClaudeDesktopDetectsLaterParentMergeOverride(t *testing.T) {
	const clearingPolicy = `{"parentSettingsBehavior":null}`
	t.Run("base file merges first", func(t *testing.T) {
		s, source := testManagedDesktopSystem(t)
		writeManagedTestFile(t, filepath.Join(s.paths.Directory, "managed-settings.json"), clearingPolicy, 0o644)
		if err := s.apply("install", source); err != nil {
			t.Fatal(err)
		}
		if merge, exists, err := s.ownedPolicy(); !merge || !exists || err != nil {
			t.Fatalf("OBS policy must merge after the base file: %v, %v, %v", merge, exists, err)
		}
	})
	t.Run("later drop-in can clear OBS setting", func(t *testing.T) {
		s, source := testManagedDesktopSystem(t)
		adminPath := filepath.Join(s.dropIns(), "60-admin.json")
		writeManagedTestFile(t, adminPath, clearingPolicy, 0o644)
		if err := s.apply("install", source); err == nil || !strings.Contains(err.Error(), "parentSettingsBehavior merge") || !strings.Contains(err.Error(), adminPath) {
			t.Fatalf("install should report the later policy clearing required parent settings: %v", err)
		}
		assertManagedTestFileAbsent(t, s.policy())
		assertManagedTestFileAbsent(t, s.executable())
		if data := readManagedTestFile(t, adminPath); string(data) != clearingPolicy {
			t.Fatalf("administrator policy changed: %s", data)
		}
	})
}

func TestManagedDesktopStagingRequiresTrustedStickyDirectory(t *testing.T) {
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "tmp")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := os.Getuid()
	if err := requireManagedDesktopStaging(path, owner); err == nil || !strings.Contains(err.Error(), "sticky directory") {
		t.Fatalf("staging accepted a directory without the sticky bit: %v", err)
	}
	if err := os.Chmod(path, os.ModeSticky|0o777); err != nil {
		t.Fatal(err)
	}
	if err := requireManagedDesktopStaging(path, owner); err != nil {
		t.Fatalf("staging rejected a trusted sticky directory: %v", err)
	}
	link := filepath.Join(parent, "linked-tmp")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := requireManagedDesktopStaging(link, owner); err == nil || !strings.Contains(err.Error(), "sticky directory") {
		t.Fatalf("staging followed a symlink: %v", err)
	}
	if err := os.Chmod(parent, 0o775); err != nil {
		t.Fatal(err)
	}
	if err := requireManagedDesktopStaging(path, owner); err == nil || !strings.Contains(err.Error(), "unsafe machine directory") {
		t.Fatalf("staging through a group-writable ancestor was accepted: %v", err)
	}
}

func TestManagedClaudeDesktopRetryAndPublishRollback(t *testing.T) {
	s, source := testManagedDesktopSystem(t)
	writeManagedTestFile(t, s.executable(), "OBS interrupted runtime", 0o755)
	if err := s.apply("update", source); err != nil {
		t.Fatal(err)
	}
	initialRuntime := readManagedTestFile(t, s.executable())
	if !bytes.Equal(initialRuntime, readManagedTestFile(t, source)) {
		t.Fatal("update did not recover a runtime-only installation")
	}
	if err := os.Remove(s.policy()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(s.policy(), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManagedTestFile(t, source, "OBS candidate", 0o755)
	if err := s.publish(source, true, true); err == nil {
		t.Fatal("replacing the policy directory should fail")
	}
	if got := readManagedTestFile(t, s.executable()); !bytes.Equal(got, initialRuntime) {
		t.Fatalf("failed policy publish lost the previous runtime: %s", got)
	}
	if err := os.Remove(s.policy()); err != nil {
		t.Fatal(err)
	}
	if err := s.apply("remove", ""); err != nil {
		t.Fatal(err)
	}
	assertManagedTestFileAbsent(t, s.executable())
}

func TestManagedClaudeDesktopConcurrentInstall(t *testing.T) {
	s, source := testManagedDesktopSystem(t)
	const callers = 4
	errs := make(chan error, callers)
	for range callers {
		go func() { errs <- s.apply("install", source) }()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(s.dropIns())
	if err != nil || len(entries) != 1 || entries[0].Name() != desktop.ManagedDropIn {
		t.Fatalf("concurrent install left unexpected files: %v, %v", entries, err)
	}
}

func TestManagedClaudeDesktopUserConfigStaysOutsidePrivilegeOperation(t *testing.T) {
	home := t.TempDir()
	path := agentfiles.ConfigPath(home, desktop.Name)
	denied := errors.New("authorization canceled")
	options := CodexOptions{Endpoint: "https://telemetry.invalid", XToken: "test-token", CaptureContent: "none"}
	if err := installClaudeDesktopManaged(options, home, func(_, _ string) error { return denied }); !errors.Is(err, denied) {
		t.Fatalf("privilege error was lost: %v", err)
	}
	assertManagedTestFileAbsent(t, path)
	if err := installClaudeDesktopManaged(options, home, func(action, source string) error {
		if action != "install" || source != "" {
			t.Fatalf("unexpected privileged operation: %q %q", action, source)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	config := readManagedTestFile(t, path)
	if !bytes.Contains(config, []byte(`"enabled": true`)) || !bytes.Contains(config, []byte("test-token")) {
		t.Fatal("the user config is missing the expected activation or credential")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("machine installation left unexpected user files: %v, %v", entries, err)
	}
	options.NoConfig = true
	options.Endpoint = "https://ignore-on-update.invalid"
	if err := installClaudeDesktopManaged(options, home, func(action, _ string) error {
		if action != "update" {
			t.Fatalf("action = %q, want update", action)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readManagedTestFile(t, path), config) {
		t.Fatal("update changed the local config")
	}
}

func testManagedDesktopSystem(t *testing.T) (managedDesktopSystem, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "obs-agent-connector")
	writeManagedTestFile(t, source, "OBS version one", 0o755)
	root := filepath.Join(dir, "ClaudeCode")
	return managedDesktopSystem{
		paths: desktop.MachinePaths{
			Directory:   root,
			PolicyPath:  filepath.Join(root, "managed-settings.d", desktop.ManagedDropIn),
			RuntimePath: filepath.Join(root, desktop.ManagedRuntime),
		},
		owner: os.Getuid(),
		verifyExecutable: func(path string) error {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.HasPrefix(data, []byte("OBS ")) {
				return fmt.Errorf("foreign executable %s", path)
			}
			return nil
		},
	}, source
}

func writeManagedTestFile(t *testing.T, path, value string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func readManagedTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertManagedTestFileAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, got %v", path, err)
	}
}
