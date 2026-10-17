//go:build windows

package sqlite

import "golang.org/x/sys/windows"

func publishMigrationBackup(directory string) (string, error) {
	// Windows cannot fsync directory handles. Publish the verified private
	// directory using the same write-through rename as configuration storage.
	destination := directory + "-verified"
	from, err := windows.UTF16PtrFromString(directory)
	if err != nil {
		return "", err
	}
	to, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return "", err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return "", err
	}
	return destination, nil
}
