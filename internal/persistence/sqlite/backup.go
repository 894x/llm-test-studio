package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Backup creates a transactionally consistent SQLite snapshot suitable for
// restoring the database if a later migration cannot be completed.
func Backup(ctx context.Context, sourcePath, destinationPath string) (err error) {
	if strings.TrimSpace(sourcePath) == "" {
		return errors.New("sqlite backup source path is required")
	}
	if strings.TrimSpace(destinationPath) == "" {
		return errors.New("sqlite backup destination path is required")
	}
	sourcePath, err = filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolve sqlite backup source: %w", err)
	}
	destinationPath, err = filepath.Abs(destinationPath)
	if err != nil {
		return fmt.Errorf("resolve sqlite backup destination: %w", err)
	}
	if sourcePath == destinationPath {
		return errors.New("sqlite backup destination must differ from source")
	}
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("inspect sqlite backup source: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return errors.New("sqlite backup source must be a regular file")
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o700); err != nil {
		return fmt.Errorf("create sqlite backup directory: %w", err)
	}
	if _, err := os.Lstat(destinationPath); err == nil {
		return errors.New("sqlite backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect sqlite backup destination: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), "."+filepath.Base(destinationPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create sqlite backup temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	temporaryExists := true
	createdDestination := false
	defer func() {
		if temporaryExists {
			_ = os.Remove(temporaryPath)
		}
		if err != nil && createdDestination {
			_ = os.Remove(destinationPath)
		}
	}()
	if err = temporary.Close(); err != nil {
		return fmt.Errorf("close sqlite backup temporary file: %w", err)
	}

	db, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		return fmt.Errorf("open sqlite backup source: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	if _, err = db.ExecContext(ctx, "PRAGMA busy_timeout = 5000"); err != nil {
		return fmt.Errorf("set sqlite backup busy timeout: %w", err)
	}
	if _, err = db.ExecContext(ctx, "VACUUM main INTO ?", temporaryPath); err != nil {
		return fmt.Errorf("create sqlite backup: %w", err)
	}
	if err = db.Close(); err != nil {
		return fmt.Errorf("close sqlite backup source: %w", err)
	}
	if err = os.Chmod(temporaryPath, 0o600); err != nil {
		return fmt.Errorf("secure sqlite backup permissions: %w", err)
	}
	if err = validateBackup(ctx, temporaryPath); err != nil {
		return err
	}
	if err = syncBackupFile(temporaryPath); err != nil {
		return err
	}
	if err = atomicPublish(temporaryPath, destinationPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("sqlite backup destination already exists")
		}
		return fmt.Errorf("publish sqlite backup: %w", err)
	}
	createdDestination = true
	temporaryExists = false
	if err = syncBackupDirectory(filepath.Dir(destinationPath)); err != nil {
		return err
	}
	createdDestination = false
	return nil
}

func publishWithHardLink(sourcePath, destinationPath string) error {
	if err := os.Link(sourcePath, destinationPath); err != nil {
		return err
	}
	if err := os.Remove(sourcePath); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("remove published sqlite backup temporary link: %w", err)
	}
	return nil
}

func syncBackupFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open sqlite backup for sync: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync sqlite backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close synced sqlite backup: %w", err)
	}

	return nil
}

func syncBackupDirectory(path string) error {
	// Windows does not support syncing a directory handle. On Unix, syncing the
	// parent makes the newly published directory entry durable as well as the file.
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open sqlite backup directory for sync: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync sqlite backup directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close synced sqlite backup directory: %w", err)
	}
	return nil
}

func validateBackup(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite backup for verification: %w", err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&result); err != nil {
		return fmt.Errorf("verify sqlite backup: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("sqlite backup quick check failed: %s", result)
	}
	return nil
}
