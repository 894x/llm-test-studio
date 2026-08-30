//go:build darwin

package sqlite

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func atomicPublish(sourcePath, destinationPath string) error {
	err := unix.RenamexNp(sourcePath, destinationPath, unix.RENAME_EXCL)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %v", os.ErrExist, err)
	}
	if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EINVAL) {
		return publishWithHardLink(sourcePath, destinationPath)
	}
	return err
}
