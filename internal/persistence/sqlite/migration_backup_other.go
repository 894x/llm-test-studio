//go:build !windows

package sqlite

import (
	"errors"
	"os"
	"path/filepath"
)

func publishMigrationBackup(directory string) (string, error) {
	for _, path := range []string{directory, filepath.Dir(directory)} {
		file, err := os.Open(path)
		if err != nil {
			return "", err
		}
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return "", err
		}
	}
	return directory, nil
}
