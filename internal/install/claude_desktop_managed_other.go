//go:build !darwin && !linux && !windows

package install

import (
	"errors"
	"os"
)

var errUnsupportedManagedDesktop = errors.New("managed Claude Desktop registration is unavailable on this operating system")

func checkManagedDesktopPath(string, os.FileInfo, int, managedPathKind) error {
	return errUnsupportedManagedDesktop
}

func prepareManagedDesktopPath(string, managedPathKind, os.FileMode) error {
	return errUnsupportedManagedDesktop
}

func protectClaudeDesktopUserConfig(string) error {
	return errUnsupportedManagedDesktop
}

func lockManagedDirectory(string) (func(), error) {
	return nil, errUnsupportedManagedDesktop
}

func managedDesktopAdminPrivileges() (bool, error) {
	return false, errUnsupportedManagedDesktop
}

func elevateManagedDesktop(string, string) error {
	return errUnsupportedManagedDesktop
}

func checkManagedDesktopPriority() error {
	return errUnsupportedManagedDesktop
}
