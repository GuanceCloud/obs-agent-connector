package install

import (
	"os"
	"path/filepath"
	"runtime"
)

func RuntimePath(home string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	name := "obs-agent-connector"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(home, ".local", "bin", name), nil
}

func InstallRuntime(home, source, destination string) (string, error) {
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	if destination == "" {
		var err error
		destination, err = RuntimePath(home)
		if err != nil {
			return "", err
		}
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return "", err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return "", err
		}
	}
	return absoluteDestination, nil
}
