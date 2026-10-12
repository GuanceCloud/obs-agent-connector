package parse

import (
	"os"
)

func openSkillMetadata(path string) (*os.File, error) {
	// Avoid device namespaces; only regular filesystem paths were admitted by
	// Lstat. Recheck identity after opening before reading any bytes.
	return os.Open(path)
}
