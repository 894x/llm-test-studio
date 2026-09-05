// Package fileconfig provides the durable file primitives shared by authored
// configuration catalogs.
package fileconfig

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const atomicTemporaryPattern = ".config-*.tmp"

// WriteAtomically replaces path only after the complete payload has reached
// the temporary file. Callers remain responsible for validating the payload.
func WriteAtomically(ctx context.Context, path string, payload []byte) error {
	return writeAtomically(ctx, path, payload, replaceFile)
}

func writeAtomically(ctx context.Context, path string, payload []byte, install func(string, string) error) error {
	if ctx == nil || strings.TrimSpace(path) == "" || !filepath.IsAbs(path) || len(payload) == 0 || install == nil {
		return errors.New("file config: invalid atomic write")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	directory := filepath.Dir(filepath.Clean(path))
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	file, err := os.CreateTemp(directory, atomicTemporaryPattern)
	if err != nil {
		return fmt.Errorf("create config temp file: %w", err)
	}
	temporary := file.Name()
	cleanup := func() { _ = os.Remove(temporary) }
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		cleanup()
		return fmt.Errorf("secure config temp file: %w", err)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		cleanup()
		return fmt.Errorf("write config temp file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		cleanup()
		return fmt.Errorf("sync config temp file: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close config temp file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return err
	}
	if err := install(temporary, filepath.Clean(path)); err != nil {
		cleanup()
		return fmt.Errorf("install config file: %w", err)
	}
	return nil
}

// IsAtomicTemporaryName reports whether name has the exact decimal suffix
// produced by os.CreateTemp for atomicTemporaryPattern.
func IsAtomicTemporaryName(name string) bool {
	const prefix = ".config-"
	const suffix = ".tmp"
	if filepath.Base(name) != name || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	random := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if random == "" {
		return false
	}
	if len(random) > 1 && random[0] == '0' {
		return false
	}
	_, err := strconv.ParseUint(random, 10, 32)
	return err == nil
}
