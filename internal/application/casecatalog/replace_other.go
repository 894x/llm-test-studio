//go:build !windows

package casecatalog

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceCaseFile(sourcePath, destinationPath string) error {
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destinationPath))
	if err != nil {
		return fmt.Errorf("open case directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync case directory: %w", err)
	}
	return nil
}
