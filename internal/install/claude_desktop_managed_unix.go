//go:build darwin || linux

package install

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
)

func checkManagedDesktopPath(path string, info os.FileInfo, owner int, kind managedPathKind) error {
	mode := info.Mode()
	protected := managedFileOwner(info, owner) && mode.Perm()&0o022 == 0
	switch kind {
	case managedDirectoryPath:
		if !info.IsDir() || !protected {
			return fmt.Errorf("unsafe machine directory %s. It must be administrator-owned and writable only by its owner", path)
		}
		if mode.Perm()&0o055 != 0o055 {
			return fmt.Errorf("machine directory %s must allow desktop accounts to read and traverse it", path)
		}
	case managedRuntimeFile:
		if !mode.IsRegular() || !protected || mode.Perm()&0o111 != 0o111 || mode&(os.ModeSetuid|os.ModeSetgid) != 0 {
			return fmt.Errorf("unsafe machine runtime %s", path)
		}
	case managedPolicyFile:
		if !mode.IsRegular() || !protected || mode.Perm()&0o044 != 0o044 {
			return fmt.Errorf("cannot safely inspect Claude Code policy %s", path)
		}
	default:
		return fmt.Errorf("unknown machine file type for %s", path)
	}
	return requireManagedPathWithoutACL(path)
}

func prepareManagedDesktopPath(path string, _ managedPathKind, mode os.FileMode) error {
	// Use the requested mode for our new files even under a restrictive umask.
	return os.Chmod(path, mode)
}

func protectClaudeDesktopUserConfig(path string) error {
	return os.Chmod(path, 0o600)
}

func managedFileOwner(info os.FileInfo, owner int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == owner
}

func requireManagedPathWithoutACL(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	// macOS ACLs can grant write access that does not appear in the mode bits.
	cmd := exec.Command("/bin/ls", "-lbed", path)
	cmd.Env = []string{"LC_ALL=C"}
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("inspect machine ACL for %s: %w", path, err)
	}
	if bytes.Count(output, []byte{'\n'}) != 1 {
		return fmt.Errorf("cannot establish machine ownership for %s with an extended ACL", path)
	}
	return nil
}

func lockManagedDirectory(path string) (func(), error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(directory.Fd()), syscall.LOCK_EX); err != nil {
		directory.Close()
		return nil, err
	}
	return func() { directory.Close() }, nil
}

func managedDesktopAdminPrivileges() (bool, error) {
	return os.Getuid() == 0 && os.Geteuid() == 0, nil
}

func elevateManagedDesktop(action, source string) error {
	paths, err := desktop.ManagedPaths()
	if err != nil {
		return err
	}
	stagingParent := "/tmp"
	if runtime.GOOS == "darwin" {
		stagingParent = "/private/tmp"
	}
	if err := requireManagedDesktopStaging(stagingParent, 0); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(stagingParent, "obs-claude-desktop-admin-")
	if err != nil {
		return fmt.Errorf("prepare managed installer: %w", err)
	}
	defer os.RemoveAll(dir)
	staged := filepath.Join(dir, filepath.Base(paths.RuntimePath))
	if err := copyExecutable(source, staged); err != nil {
		return fmt.Errorf("stage managed installer: %w", err)
	}
	if err := verifyOBSExecutable(staged); err != nil {
		return err
	}
	cmd := exec.Command("/usr/bin/sudo", "--", staged, "internal", ManagedDesktopAdminCommand, action)
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("managed Claude Desktop %s did not complete: %w", action, err)
	}
	return nil
}

func requireManagedDesktopStaging(path string, owner int) error {
	if err := requireSystemDirectory(filepath.Dir(path), owner); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect managed installer staging directory: %w", err)
	}
	if !info.IsDir() || !managedFileOwner(info, owner) || info.Mode()&os.ModeSticky == 0 {
		return fmt.Errorf("managed installer staging requires an administrator-owned sticky directory at %s", path)
	}
	return requireManagedPathWithoutACL(path)
}

func checkManagedDesktopPriority() error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	// A device policy can take precedence over the file-based drop-ins.
	prefs := "/Library/Managed Preferences"
	names := []string{filepath.Join(prefs, "com.anthropic.claudecode.plist")}
	login := os.Getenv("SUDO_USER")
	if os.Geteuid() != 0 {
		if account, err := user.Current(); err == nil {
			login = account.Username
		}
	}
	if login != "" && login != "." && login != ".." && filepath.Base(login) == login {
		names = append(names, filepath.Join(prefs, login, "com.anthropic.claudecode.plist"))
	}
	for _, path := range names {
		if _, err := os.Lstat(path); err == nil {
			return fmt.Errorf("Claude Code has a higher-priority device policy at %s. Ask the device administrator to deploy the OBS Hook there", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check Claude Code device policy: %w", err)
		}
	}
	return nil
}
