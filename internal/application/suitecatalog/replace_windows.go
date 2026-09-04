//go:build windows

package suitecatalog

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceSuiteFile(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return fmt.Errorf("encode suite temporary path: %w", err)
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return fmt.Errorf("encode suite destination path: %w", err)
	}
	return windows.MoveFileEx(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
