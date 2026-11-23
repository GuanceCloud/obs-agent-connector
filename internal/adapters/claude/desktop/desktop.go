package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

const (
	Name            = "claude-desktop"
	ManagedArgument = "--managed"
	ManagedDropIn   = "50-obs-claude-desktop.json"
	ManagedRuntime  = "obs-agent-connector-claude-desktop"
)

type MachinePaths struct {
	Directory   string
	PolicyPath  string
	RuntimePath string
}

func SupportsManagedOS(goos string) bool {
	switch goos {
	case "darwin", "linux", "windows":
		return true
	default:
		return false
	}
}

func ManagedPaths() (MachinePaths, error) {
	return managedPathsForOS(runtime.GOOS, systemProgramFiles)
}

func managedPathsForOS(goos string, programFiles func() (string, error)) (MachinePaths, error) {
	var directory string
	runtimeName := ManagedRuntime
	switch goos {
	case "darwin":
		directory = "/Library/Application Support/ClaudeCode"
	case "linux":
		directory = "/etc/claude-code"
	case "windows":
		path, err := programFiles()
		if err != nil {
			return MachinePaths{}, fmt.Errorf("resolve Windows Program Files: %w", err)
		}
		if !filepath.IsAbs(path) {
			return MachinePaths{}, errors.New("Windows Program Files is not an absolute directory")
		}
		directory = filepath.Join(path, "ClaudeCode")
		runtimeName += ".exe"
	default:
		return MachinePaths{}, fmt.Errorf("Claude Desktop managed Hooks are not supported on %s", goos)
	}
	return MachinePaths{
		Directory:   directory,
		PolicyPath:  filepath.Join(directory, "managed-settings.d", ManagedDropIn),
		RuntimePath: filepath.Join(directory, runtimeName),
	}, nil
}

// ManagedHome resolves the account that owns the current session, including
// when Claude launches the Hook with different profile environment variables.
func ManagedHome() (string, error) {
	if runtime.GOOS == "windows" {
		return homeForWindowsAccount(user.Current)
	}
	return homeForUID(os.Getuid(), os.Geteuid(), user.LookupId)
}

func homeForUID(uid, effectiveUID int, lookup func(string) (*user.User, error)) (string, error) {
	if uid <= 0 || uid != effectiveUID {
		return "", errors.New("run this command as the account using Claude Desktop")
	}
	id := strconv.Itoa(uid)
	account, err := lookup(id)
	if err != nil {
		return "", fmt.Errorf("resolve the Claude Desktop account: %w", err)
	}
	if account == nil || account.Uid != id || !filepath.IsAbs(account.HomeDir) {
		return "", errors.New("the Claude Desktop account has no absolute home directory")
	}
	return account.HomeDir, nil
}

func homeForWindowsAccount(lookup func() (*user.User, error)) (string, error) {
	// os/user obtains the profile from the current process token on Windows.
	account, err := lookup()
	if err != nil {
		return "", fmt.Errorf("resolve the Claude Desktop account: %w", err)
	}
	if account == nil || account.Uid == "" || !filepath.IsAbs(account.HomeDir) {
		return "", errors.New("the Claude Desktop account has no absolute home directory")
	}
	switch account.Uid {
	case "S-1-5-18", "S-1-5-19", "S-1-5-20":
		return "", errors.New("run this command as the account using Claude Desktop")
	}
	return account.HomeDir, nil
}
