package fileconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// EnsurePrivateDirectory creates an application-owned directory before callers
// persist credential intents. Existing ancestors keep their permissions.
func EnsurePrivateDirectory(ctx context.Context, path string) error {
	return ensurePrivateDirectory(ctx, path, syncPrivateDirectory)
}

func ensurePrivateDirectory(ctx context.Context, path string, confirm func(string) error) error {
	valid := ctx != nil && filepath.IsAbs(path) && filepath.Clean(path) == path
	if !valid || filepath.Dir(path) == path {
		return errors.New("file config: invalid directory")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	if _, err := os.Stat(parent); errors.Is(err, os.ErrNotExist) {
		if err := ensurePrivateDirectory(ctx, parent, confirm); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return WithExclusiveLock(ctx, path+".bootstrap.lock", func() error {
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() {
				return errors.New("file config: directory is not a regular directory")
			}
			if err := os.Chmod(path, 0o700); err != nil {
				return err
			}
			return confirm(path)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		temporary, err := os.MkdirTemp(parent, ".config-directory-")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(temporary) }()
		if err := os.Chmod(temporary, 0o700); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := publishPrivateDirectory(temporary, path); err != nil {
			return err
		}
		return confirm(path)
	})
}

func syncDirectoryChain(path string, sync func(string) error) error {
	for {
		if err := sync(path); err != nil {
			return err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return nil
		}
		path = parent
	}
}
