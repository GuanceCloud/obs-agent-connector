package agent

import (
	"os"
	"strings"
)

func InstalledMarker(p Definition) (string, bool) {
	path := FirstExistingPath(p.Markers)
	return path, path != ""
}

func FirstExistingPath(paths []string) string {
	for _, path := range paths {
		expanded := ExpandHome(path)
		if PathExists(expanded) {
			return expanded
		}
	}
	return ""
}

func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ExpandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
