package install

import (
	"bytes"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

const (
	ManagedDesktopAdminCommand = "claude-desktop-managed"
	managedSettingsLimit       = 1 << 20
)

type managedPathKind uint8

const (
	managedDirectoryPath managedPathKind = iota
	managedRuntimeFile
	managedPolicyFile
)

func InstallClaudeDesktopManaged(options CodexOptions) error {
	if !desktop.SupportsManagedOS(runtime.GOOS) {
		return fmt.Errorf("managed Claude Desktop installation is unavailable on %s", runtime.GOOS)
	}
	home, err := desktop.ManagedHome()
	if err != nil {
		return err
	}
	return installClaudeDesktopManaged(options, home, runManagedDesktopAdmin)
}

func installClaudeDesktopManaged(options CodexOptions, home string, apply func(string, string) error) error {
	action := "install"
	if options.NoConfig {
		action = "update"
	}
	configPath := agentfiles.ConfigPath(home, desktop.Name)
	config, err := prepareClaudeDesktopConfig(configPath, options)
	if err != nil {
		return err
	}
	if err := apply(action, options.SourceExecutable); err != nil {
		return err
	}
	if config != nil {
		if err := WriteClaudeDesktopUserConfig(configPath, config); err != nil {
			return fmt.Errorf("machine files installed. Could not write %s: %w", configPath, err)
		}
	}
	return nil
}

func prepareClaudeDesktopConfig(path string, options CodexOptions) (map[string]any, error) {
	if options.NoConfig {
		return nil, nil
	}
	current, exists, err := readJSONObjectIfExists(path)
	if err != nil {
		return nil, fmt.Errorf("parse Claude Desktop GTrace config: %w", err)
	}
	if !shouldConfigureGTrace(exists, options) {
		return nil, nil
	}
	return mergeCodexGTraceConfig(current, options, exists)
}

func WriteClaudeDesktopUserConfig(path string, config map[string]any) error {
	body, err := marshalJSON(config)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".gtrace-*.tmp")
	if err != nil {
		return err
	}
	staged := file.Name()
	defer os.Remove(staged)
	writeErr := protectClaudeDesktopUserConfig(staged)
	if writeErr == nil {
		_, writeErr = file.Write(body)
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	return os.Rename(staged, path)
}

func RemoveClaudeDesktopManaged(source string) error {
	if !desktop.SupportsManagedOS(runtime.GOOS) {
		return fmt.Errorf("managed Claude Desktop removal is unavailable on %s", runtime.GOOS)
	}
	return runManagedDesktopAdmin("remove", source)
}

func runManagedDesktopAdmin(action, source string) error {
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return err
		}
	}
	admin, err := managedDesktopAdminPrivileges()
	if err != nil {
		return err
	}
	if !admin {
		return elevateManagedDesktop(action, source)
	}
	system, err := productionManagedDesktop()
	if err != nil {
		return err
	}
	return system.apply(action, source)
}

// ApplyClaudeDesktopManaged is the privileged entry point. The action is its
// only input. Source and destination paths come from the running program.
func ApplyClaudeDesktopManaged(args []string) error {
	if !desktop.SupportsManagedOS(runtime.GOOS) {
		return fmt.Errorf("managed Claude Desktop registration is unavailable on %s", runtime.GOOS)
	}
	admin, err := managedDesktopAdminPrivileges()
	if err != nil {
		return err
	}
	if !admin {
		return errors.New("the machine file operation requires administrator privileges")
	}
	if len(args) != 1 {
		return errors.New("expected one managed Claude Desktop action")
	}
	source, err := os.Executable()
	if err != nil {
		return err
	}
	system, err := productionManagedDesktop()
	if err != nil {
		return err
	}
	return system.apply(args[0], source)
}

type ManagedDesktopState struct {
	Policy  bool
	Runtime bool
	Blocked error
}

func ClaudeDesktopManagedState() (ManagedDesktopState, error) {
	if !desktop.SupportsManagedOS(runtime.GOOS) {
		return ManagedDesktopState{}, nil
	}
	system, err := productionManagedDesktop()
	if err != nil {
		return ManagedDesktopState{}, err
	}
	state, err := system.inspect()
	if err == nil && state.Policy {
		state.Blocked = system.checkPriority()
		if state.Blocked == nil {
			_, state.Blocked = system.mergeParent(true)
		}
	}
	return state, err
}

func productionManagedDesktop() (managedDesktopSystem, error) {
	paths, err := desktop.ManagedPaths()
	if err != nil {
		return managedDesktopSystem{}, err
	}
	return managedDesktopSystem{
		paths: paths, owner: 0,
		verifyExecutable: verifyOBSExecutable,
		checkPriority:    checkManagedDesktopPriority,
	}, nil
}

func verifyOBSExecutable(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "github.com/GuanceCloud/obs-agent-connector/cmd/obs-agent-connector" ||
		info.Main.Path != "github.com/GuanceCloud/obs-agent-connector" {
		return fmt.Errorf("cannot establish ownership of OBS runtime %s", path)
	}
	return nil
}

type managedDesktopSystem struct {
	paths            desktop.MachinePaths
	owner            int
	verifyExecutable func(string) error
	checkPriority    func() error
}

func (s managedDesktopSystem) dropIns() string {
	return filepath.Dir(s.paths.PolicyPath)
}

func (s managedDesktopSystem) policy() string {
	return s.paths.PolicyPath
}

func (s managedDesktopSystem) executable() string {
	return s.paths.RuntimePath
}

func managedDesktopPolicy(executable string, mergeParent bool) map[string]any {
	handler := map[string]any{
		"type":          "command",
		"command":       executable,
		"args":          []string{"hook", desktop.Name, desktop.ManagedArgument},
		"timeout":       60,
		"statusMessage": "Uploading Claude telemetry to GTrace",
	}
	group := []map[string]any{{"hooks": []map[string]any{handler}}}
	policy := map[string]any{"hooks": map[string]any{"Stop": group, "SessionEnd": group}}
	if mergeParent {
		policy["parentSettingsBehavior"] = "merge"
	}
	return policy
}

func (s managedDesktopSystem) inspect() (ManagedDesktopState, error) {
	state := ManagedDesktopState{}
	if err := s.requireSupportDirectory(); err != nil {
		return state, err
	}
	if exists, err := s.systemDirectory(s.paths.Directory, false); !exists || err != nil {
		return state, err
	}
	if exists, err := s.systemDirectory(s.dropIns(), false); err != nil {
		return state, err
	} else if exists {
		_, state.Policy, err = s.ownedPolicy()
		if err != nil {
			return state, err
		}
	}
	var err error
	state.Runtime, err = s.ownedRuntime()
	return state, err
}

func (s managedDesktopSystem) apply(action, source string) error {
	switch action {
	case "install", "update", "remove":
	default:
		return errors.New("unknown managed Claude Desktop action")
	}
	if err := s.requireSupportDirectory(); err != nil {
		return err
	}
	if action != "remove" && s.checkPriority != nil {
		if err := s.checkPriority(); err != nil {
			return err
		}
	}
	exists, err := s.systemDirectory(s.paths.Directory, action == "install")
	if err != nil {
		return err
	}
	if !exists {
		if action == "remove" {
			return nil
		}
		return errors.New("managed Claude Desktop files are absent. Run install claude-desktop")
	}
	unlock, err := lockManagedDirectory(s.paths.Directory)
	if err != nil {
		return fmt.Errorf("lock managed Claude Desktop directory: %w", err)
	}
	defer unlock()

	existing, err := s.inspect()
	if err != nil {
		return err
	}
	if action == "remove" {
		return s.remove(existing)
	}
	if action == "update" && !existing.Policy && !existing.Runtime {
		return errors.New("managed Claude Desktop files are absent. Run install claude-desktop")
	}
	if err := s.verifyExecutable(source); err != nil {
		return err
	}
	mergeParent, err := s.mergeParent(existing.Policy)
	if err != nil {
		return err
	}
	if _, err := s.systemDirectory(s.dropIns(), true); err != nil {
		return err
	}
	return s.publish(source, existing.Runtime, mergeParent)
}

func (s managedDesktopSystem) requireSupportDirectory() error {
	parent := filepath.Dir(s.paths.Directory)
	if (runtime.GOOS == "darwin" && parent == "/Library/Application Support") ||
		(runtime.GOOS == "linux" && parent == "/etc") {
		if err := requireSystemDirectory(filepath.Dir(parent), s.owner); err != nil {
			return err
		}
	}
	return requireSystemDirectory(parent, s.owner)
}

func (s managedDesktopSystem) systemDirectory(path string, create bool) (bool, error) {
	err := requireSystemDirectory(path, s.owner)
	if create && os.IsNotExist(err) {
		mkdirErr := os.Mkdir(path, 0o755)
		if mkdirErr == nil {
			if err := prepareManagedDesktopPath(path, managedDirectoryPath, 0o755); err != nil {
				return false, errors.Join(fmt.Errorf("prepare machine directory %s: %w", path, err), os.Remove(path))
			}
		} else if !os.IsExist(mkdirErr) {
			return false, mkdirErr
		}
		err = requireSystemDirectory(path, s.owner)
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func requireSystemDirectory(path string, owner int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("unsafe machine directory %s", path)
	}
	return checkManagedDesktopPath(path, info, owner, managedDirectoryPath)
}

func (s managedDesktopSystem) ownedRuntime() (bool, error) {
	info, err := os.Lstat(s.executable())
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("unsafe machine runtime %s", s.executable())
	}
	if err := checkManagedDesktopPath(s.executable(), info, s.owner, managedRuntimeFile); err != nil {
		return false, err
	}
	if err := s.verifyExecutable(s.executable()); err != nil {
		return false, err
	}
	return true, nil
}

func (s managedDesktopSystem) readPolicy(path string) (map[string]any, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > managedSettingsLimit {
		return nil, false, fmt.Errorf("cannot safely inspect Claude Code policy %s", path)
	}
	if err := checkManagedDesktopPath(path, info, s.owner, managedPolicyFile); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, true, nil
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil || value == nil {
		return nil, false, fmt.Errorf("invalid Claude Code policy object in %s", path)
	}
	return value, true, nil
}

func (s managedDesktopSystem) ownedPolicy() (mergeParent bool, present bool, err error) {
	value, present, err := s.readPolicy(s.policy())
	if err != nil || !present {
		return false, present, err
	}
	mergeParent = value["parentSettingsBehavior"] == "merge"
	observed, _ := json.Marshal(value)
	expected, _ := json.Marshal(managedDesktopPolicy(s.executable(), mergeParent))
	if !bytes.Equal(observed, expected) {
		return false, true, fmt.Errorf("Claude Code policy %s contains settings outside the OBS registration", s.policy())
	}
	return mergeParent, true, nil
}

func (s managedDesktopSystem) mergeParent(hasOwnPolicy bool) (bool, error) {
	files := []string{filepath.Join(s.paths.Directory, "managed-settings.json")}
	entries, err := os.ReadDir(s.dropIns())
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") && name != desktop.ManagedDropIn {
			files = append(files, filepath.Join(s.dropIns(), name))
		}
	}
	effectivePolicyKeys := map[string]bool{}
	var disablingPolicy, helperPolicy, laterParentClear string
	for _, path := range files {
		value, _, err := s.readPolicy(path)
		if err != nil {
			return false, err
		}
		for key, setting := range value {
			if key == "managedSourcesBehavior" || key == "wslInheritsWindowsSettings" {
				continue
			}
			if setting == nil {
				delete(effectivePolicyKeys, key)
			} else {
				effectivePolicyKeys[key] = true
			}
		}
		if mode, found := value["parentSettingsBehavior"]; found {
			if mode != nil && mode != "merge" && mode != "first-wins" {
				return false, fmt.Errorf("cannot safely interpret parentSettingsBehavior in %s", path)
			}
			// The base file is loaded before every sorted drop-in.
			if mode == nil && filepath.Dir(path) == s.dropIns() && filepath.Base(path) > desktop.ManagedDropIn {
				laterParentClear = path
			}
		}
		if lock, found := value["disableAllHooks"]; found {
			disabled, valid := lock.(bool)
			if lock != nil && !valid {
				return false, fmt.Errorf("cannot safely interpret disableAllHooks in %s", path)
			}
			disablingPolicy = ""
			if disabled {
				disablingPolicy = path
			}
		}
		if helper, found := value["policyHelper"]; found {
			helperPolicy = ""
			if helper != nil {
				helperPolicy = path
			}
		}
	}
	if disablingPolicy != "" {
		return false, fmt.Errorf("Claude Code machine policy %s disables Hooks", disablingPolicy)
	}
	if helperPolicy != "" {
		return false, fmt.Errorf("Claude Code machine policy %s configures policyHelper. Ask the device administrator to include the OBS Hook in the helper's policy", helperPolicy)
	}
	previouslyMerged := false
	if hasOwnPolicy {
		var err error
		previouslyMerged, _, err = s.ownedPolicy()
		if err != nil {
			return false, err
		}
	}
	mergeParent := !effectivePolicyKeys["parentSettingsBehavior"] && (previouslyMerged || len(effectivePolicyKeys) == 0)
	if mergeParent && laterParentClear != "" {
		return false, fmt.Errorf("Claude Code policy %s can override the parentSettingsBehavior merge required by the OBS Hook policy", laterParentClear)
	}
	return mergeParent, nil
}

func (s managedDesktopSystem) publish(source string, hadRuntime, mergeParent bool) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	nextRuntime, err := s.stageFile(s.executable(), sourceFile, managedRuntimeFile, 0o755)
	if err != nil {
		return err
	}
	defer os.Remove(nextRuntime)

	body, err := marshalJSON(managedDesktopPolicy(s.executable(), mergeParent))
	if err != nil {
		return err
	}
	nextPolicy, err := s.stageFile(s.policy(), bytes.NewReader(body), managedPolicyFile, 0o644)
	if err != nil {
		return err
	}
	defer os.Remove(nextPolicy)

	var previousRuntime string
	if hadRuntime {
		previousRuntime = nextRuntime + ".previous"
		if err := os.Link(s.executable(), previousRuntime); err != nil {
			return fmt.Errorf("back up machine runtime: %w", err)
		}
		defer func() {
			if previousRuntime != "" {
				_ = os.Remove(previousRuntime)
			}
		}()
	}
	if err := os.Rename(nextRuntime, s.executable()); err != nil {
		return fmt.Errorf("install machine runtime: %w", err)
	}
	if err := os.Rename(nextPolicy, s.policy()); err != nil {
		publishErr := fmt.Errorf("install machine policy: %w", err)
		if hadRuntime {
			if restoreErr := os.Rename(previousRuntime, s.executable()); restoreErr != nil {
				backup := previousRuntime
				previousRuntime = ""
				return errors.Join(publishErr, fmt.Errorf("restore prior runtime from %s: %w", backup, restoreErr))
			}
			return publishErr
		}
		return errors.Join(publishErr, os.Remove(s.executable()))
	}
	return nil
}

func (s managedDesktopSystem) stageFile(target string, src io.Reader, kind managedPathKind, mode os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), ".obs-claude-desktop-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	_, writeErr := io.Copy(file, src)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := prepareManagedDesktopPath(path, kind, mode); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("prepare machine file %s: %w", target, err)
	}
	info, err := os.Lstat(path)
	if err == nil {
		err = checkManagedDesktopPath(path, info, s.owner, kind)
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func (s managedDesktopSystem) remove(state ManagedDesktopState) error {
	if state.Policy {
		if err := os.Remove(s.policy()); err != nil {
			return fmt.Errorf("remove managed Claude Code policy: %w", err)
		}
	}
	if state.Runtime {
		if err := os.Remove(s.executable()); err != nil {
			return fmt.Errorf("remove managed Claude Desktop runtime: %w", err)
		}
	}
	return nil
}
