package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var semanticVersionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?$`)

func InstalledVersion(p Definition) string {
	paths := append([]string{}, p.Markers...)
	paths = append(paths, p.RemovePaths...)
	for _, rawPath := range paths {
		path := ExpandHome(rawPath)
		if !PathExists(path) {
			continue
		}
		if version := detectInstalledVersion(path); version != "" {
			return version
		}
	}
	return ""
}

func detectInstalledVersion(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if version := normalizeVersion(info.Name()); version != "" {
		return version
	}
	if !info.IsDir() {
		return ""
	}
	if version := versionFromKnownJSONFiles(path); version != "" {
		return version
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if version := normalizeVersion(entry.Name()); version != "" {
			return version
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		childPath := filepath.Join(path, entry.Name())
		if version := versionFromKnownJSONFiles(childPath); version != "" {
			return version
		}
		children, err := os.ReadDir(childPath)
		if err != nil {
			continue
		}
		for _, child := range children {
			if version := normalizeVersion(child.Name()); version != "" {
				return version
			}
		}
	}
	return ""
}

func normalizeVersion(value string) string {
	value = strings.TrimSpace(value)
	if !semanticVersionPattern.MatchString(value) {
		return ""
	}
	return strings.TrimPrefix(value, "v")
}

func versionFromKnownJSONFiles(dir string) string {
	for _, name := range []string{"package.json", "plugin.json", "manifest.json", "marketplace.json"} {
		path := filepath.Join(dir, name)
		version, err := versionFromJSONFile(path)
		if err == nil && version != "" {
			return version
		}
	}
	return ""
}

func versionFromJSONFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", err
	}
	return normalizeVersion(payload.Version), nil
}
