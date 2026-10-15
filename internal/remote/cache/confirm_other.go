//go:build !windows

package cache

import (
	"errors"
	"os"
	"path/filepath"
)

func publishFile(source, target string) error {
	if err := os.Rename(source, target); err != nil {
		return err
	}
	return confirmDirectory(filepath.Dir(target))
}

func confirmDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
