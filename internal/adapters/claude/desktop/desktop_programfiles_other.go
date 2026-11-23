//go:build !windows

package desktop

import "errors"

func systemProgramFiles() (string, error) {
	return "", errors.New("Program Files is available on Windows")
}
