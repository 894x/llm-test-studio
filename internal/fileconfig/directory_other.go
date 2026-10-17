//go:build !windows

package fileconfig

import (
	"errors"
	"os"
)

func publishPrivateDirectory(temporary, path string) error {
	return os.Rename(temporary, path)
}

func syncPrivateDirectory(path string) error {
	// A visible ancestor may come from an earlier rename whose sync failed.
	// Confirm the complete chain on retries without changing ancestor modes.
	return syncDirectoryChain(path, func(name string) error {
		directory, err := os.Open(name)
		if err != nil {
			return err
		}
		return errors.Join(directory.Sync(), directory.Close())
	})
}
