package desktop

import (
	"errors"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func systemProgramFiles() (string, error) {
	path, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return "", err
	}
	volume := filepath.VolumeName(path)
	if !filepath.IsAbs(path) || len(volume) != 2 || volume[1] != ':' {
		return "", errors.New("Windows Program Files must be on a local drive")
	}
	return path, nil
}
