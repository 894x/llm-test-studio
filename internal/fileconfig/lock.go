package fileconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const lockRetryInterval = 25 * time.Millisecond

var ErrLockBusy = errors.New("file_config_lock_busy")

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
		func(_ context.Context, file *os.File) error {
			locked, err := tryExclusiveLock(file)
			if err != nil {
				return err
			}
			if !locked {
				return ErrLockBusy
			}
			return nil
		},
	)
}

func withExclusiveLock(
	ctx context.Context,
	path string,
	action func() error,
	acquire func(context.Context, *os.File) error,
) (result error) {
	validInputs := ctx != nil && action != nil
	validPath := strings.TrimSpace(path) != "" && filepath.IsAbs(path)
	if !validInputs || !validPath {
		return errors.New("file config: invalid lock request")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	path = filepath.Clean(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create lock directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open config lock: %w", err)
	}
	locked := false
	defer func() {
		if locked {
			if err := unlockFile(file); err != nil && result == nil {
				result = fmt.Errorf("unlock config file: %w", err)
			}
		}
		if err := file.Close(); err != nil && result == nil {
			result = fmt.Errorf("close config lock: %w", err)
		}
	}()

	if err := acquire(ctx, file); err != nil {
		return err
	}
	locked = true
	if err := ctx.Err(); err != nil {
		return err
	}
	return action()
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
