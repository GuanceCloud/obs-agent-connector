package agent

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// Query the official MSIX package in the current user's package registration.
// The executable under WindowsApps may be inaccessible to the caller.
func isClaudeDesktopMSIXInstalled() bool {
	systemDir, err := windows.GetSystemDirectory()
	if err != nil {
		return false
	}
	powershell := filepath.Join(systemDir, "WindowsPowerShell", "v1.0", "powershell.exe")
	const installed = "claude-desktop-installed"
	const script = "if (Get-AppxPackage -Name 'Claude' -ErrorAction Stop) { [Console]::WriteLine('" + installed + "') }"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script).Output()
	return err == nil && strings.TrimSpace(string(output)) == installed
}
