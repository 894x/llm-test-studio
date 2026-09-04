//go:build !windows

package fileconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceFile(sourcePath, destinationPath string) error {
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destinationPath))
	if err != nil {
		return fmt.Errorf("open config directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync config directory: %w", err)
	}
	return nil
}
