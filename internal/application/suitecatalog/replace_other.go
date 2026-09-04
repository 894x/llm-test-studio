//go:build !windows

package suitecatalog

import (
	"fmt"
	"os"
	"path/filepath"
)

func replaceSuiteFile(sourcePath, destinationPath string) error {
	if err := os.Rename(sourcePath, destinationPath); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(destinationPath))
	if err != nil {
		return fmt.Errorf("open suite directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync suite directory: %w", err)
	}
	return nil
}
