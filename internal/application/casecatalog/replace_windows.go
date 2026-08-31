//go:build windows

package casecatalog

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func replaceCaseFile(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return fmt.Errorf("encode case temporary path: %w", err)
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return fmt.Errorf("encode case destination path: %w", err)
	}
	if err := windows.MoveFileEx(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	return nil
}
