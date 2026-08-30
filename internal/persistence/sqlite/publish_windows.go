//go:build windows

package sqlite

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func atomicPublish(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return fmt.Errorf("encode sqlite backup temporary path: %w", err)
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return fmt.Errorf("encode sqlite backup destination path: %w", err)
	}
	if err := windows.MoveFileEx(source, destination, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		if err == windows.ERROR_ALREADY_EXISTS || err == windows.ERROR_FILE_EXISTS {
			return fmt.Errorf("%w: %v", os.ErrExist, err)
		}
		return err
	}
	return nil
}
