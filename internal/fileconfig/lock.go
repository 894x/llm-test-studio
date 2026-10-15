package fileconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const lockRetryInterval = 25 * time.Millisecond

var ErrLockBusy = errors.New("file_config_lock_busy")

// ExclusiveLock keeps ownership until Close, including after acquisition's
// context ends. A canceled owner must finish its workers before releasing it.
type ExclusiveLock struct {
	file *os.File
	once sync.Once
	err  error
}

func (lock *ExclusiveLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	lock.once.Do(func() {
		lock.err = errors.Join(unlockFile(lock.file), lock.file.Close())
	})
	return lock.err
}

// TryAcquireExclusiveLock returns a lifetime-owned lock. Its sidecar remains
// on disk so later owners always acquire the same inode.
func TryAcquireExclusiveLock(ctx context.Context, path string) (*ExclusiveLock, error) {
	return acquireExclusiveLock(ctx, path, tryLockOnce)
}

// WithExclusiveLock runs action while holding an operating-system advisory
// lock for path. The sidecar file is intentionally retained: deleting a lock
// file can let two callers lock different inodes. Closing the file releases
// the lock even when a process exits unexpectedly.
func WithExclusiveLock(ctx context.Context, path string, action func() error) error {
	return withExclusiveLock(
		ctx,
		path,
		action,
		waitForExclusiveLock,
	)
}

// TryWithExclusiveLock uses the same cross-process lock and retained sidecar,
// returning ErrLockBusy immediately when another owner holds it.
func TryWithExclusiveLock(ctx context.Context, path string, action func() error) error {
	return withExclusiveLock(
		ctx,
		path,
		action,
		tryLockOnce,
	)
}

func withExclusiveLock(
	ctx context.Context,
	path string,
	action func() error,
	acquire func(context.Context, *os.File) error,
) (result error) {
	if action == nil {
		return errors.New("file config: invalid lock request")
	}
	lock, err := acquireExclusiveLock(ctx, path, acquire)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, lock.Close()) }()
	return action()
}

func acquireExclusiveLock(
	ctx context.Context,
	path string,
	acquire func(context.Context, *os.File) error,
) (*ExclusiveLock, error) {
	validInputs := ctx != nil && acquire != nil
	validPath := strings.TrimSpace(path) != "" && filepath.IsAbs(path)
	if !validInputs || !validPath {
		return nil, errors.New("file config: invalid lock request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open config lock: %w", err)
	}
	if err := acquire(ctx, file); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	lock := &ExclusiveLock{file: file}
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	return lock, nil
}

func tryLockOnce(_ context.Context, file *os.File) error {
	locked, err := tryExclusiveLock(file)
	if err != nil {
		return err
	}
	if !locked {
		return ErrLockBusy
	}
	return nil
}

func waitForExclusiveLock(ctx context.Context, file *os.File) error {
	for {
		acquired, err := tryExclusiveLock(file)
		if err != nil {
			return fmt.Errorf("lock config file: %w", err)
		}
		if acquired {
			return nil
		}

		timer := time.NewTimer(lockRetryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
