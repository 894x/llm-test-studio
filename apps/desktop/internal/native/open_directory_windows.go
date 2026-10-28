//go:build windows

package native

import (
	"errors"
	"os/exec"
)

func openDirectoryPlatform(path string) error {
	return directoryOpenerExit(runDirectoryOpener("explorer.exe", directoryOpenerTimeout, path))
}

// directoryOpenerExit treats exit status 1 as success: explorer.exe hands the
// new window to an already running shell process and still opens the folder.
func directoryOpenerExit(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return err
}
