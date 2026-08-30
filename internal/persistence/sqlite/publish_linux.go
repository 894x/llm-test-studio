//go:build linux

package sqlite

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func atomicPublish(sourcePath, destinationPath string) error {
	err := unix.Renameat2(unix.AT_FDCWD, sourcePath, unix.AT_FDCWD, destinationPath, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	if errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("%w: %v", os.ErrExist, err)
	}
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return publishWithHardLink(sourcePath, destinationPath)
	}
	return err
}
