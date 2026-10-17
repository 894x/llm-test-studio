package native

import (
	"errors"
	"path/filepath"
	"strings"
)

func StoragePaths(configurationRoot string) (directory string, database string, err error) {
	if configurationRoot == "" || configurationRoot != strings.TrimSpace(configurationRoot) {
		return "", "", errors.New("user configuration directory must be a non-blank absolute path")
	}
	root := filepath.Clean(configurationRoot)
	if !filepath.IsAbs(root) {
		return "", "", errors.New("user configuration directory must be an absolute path")
	}
	directory = filepath.Join(root, "llm-test-studio")
	database = filepath.Join(directory, "llm-test-studio.db")
	return directory, database, nil
}
