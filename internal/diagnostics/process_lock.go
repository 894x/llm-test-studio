package diagnostics

import (
	"errors"
	"fmt"
	"os"
	"sync"
)

type processLock struct {
	mu       sync.Mutex
	file     *os.File
	closeErr error
}

func acquireProcessLock(path string) (*processLock, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open diagnostic log ownership file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure diagnostic log ownership file: %w", err)
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire diagnostic log ownership: %w", err)
	}
	return &processLock{file: file}, nil
}

func (lock *processLock) Close() error {
	if lock == nil {
		return nil
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.file == nil {
		return lock.closeErr
	}
	file := lock.file
	lock.file = nil
	lock.closeErr = errors.Join(unlockFile(file), file.Close())
	return lock.closeErr
}
