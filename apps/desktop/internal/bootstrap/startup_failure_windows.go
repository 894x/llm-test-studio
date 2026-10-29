package bootstrap

import (
	"errors"
	"syscall"
)

func platformDiskFull(err error) bool {
	const (
		errorHandleDiskFull syscall.Errno = 39
		errorDiskFull       syscall.Errno = 112
	)
	return errors.Is(err, errorHandleDiskFull) || errors.Is(err, errorDiskFull)
}
