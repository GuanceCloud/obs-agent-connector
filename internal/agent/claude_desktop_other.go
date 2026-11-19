//go:build !windows

package agent

func isClaudeDesktopMSIXInstalled() bool {
	return false
}
