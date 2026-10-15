//go:build !windows

package runnerartifacts

import (
	"errors"
	"os"
	"path/filepath"
)

func ensureReleaseDirectory(path string) error {
	err := os.Mkdir(path, 0o700)
	if err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		return ErrIntegrity
	}
	return syncDirectory(filepath.Dir(path))
}

func publishFile(temporary, target string) error {
	if err := os.Rename(temporary, target); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
