package sqlite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type RecoveryBackup struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// FinalizeRecoveryDirectory makes completed recovery files durable before the
// desktop selector can reference them, including Windows directory publication.
func FinalizeRecoveryDirectory(ctx context.Context, directory string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("invalid recovery directory")
	}
	return publishMigrationBackup(directory)
}

// BackupIncompatibleDatabase preserves a consistent SQLite snapshot without
// interpreting its application schema. The source and its sidecars stay in place.
func BackupIncompatibleDatabase(ctx context.Context, path, destination string) (RecoveryBackup, error) {
	source, err := openRunnerBackupSource(ctx, path)
	if err != nil {
		return RecoveryBackup{}, err
	}
	defer source.close()
	var schemaVersion int
	if err := source.conn.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&schemaVersion); err != nil {
		return RecoveryBackup{}, fmt.Errorf("establish recovery snapshot: %w", err)
	}
	absolute, err := filepath.Abs(destination)
	if err != nil || absolute == source.path {
		return RecoveryBackup{}, ErrConflict
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_, err := os.Lstat(absolute + suffix)
		if err == nil {
			return RecoveryBackup{}, fmt.Errorf("recovery destination namespace unavailable: %w", ErrConflict)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return RecoveryBackup{}, err
		}
	}
	file, err := os.OpenFile(absolute, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return RecoveryBackup{}, err
	}
	defer file.Close()
	created, err := file.Stat()
	if err != nil {
		return RecoveryBackup{}, err
	}
	if err := copyRunnerSnapshot(ctx, source.conn, absolute); err != nil {
		return RecoveryBackup{}, fmt.Errorf("copy incompatible database snapshot: %w", err)
	}
	if err := file.Sync(); err != nil {
		return RecoveryBackup{}, err
	}
	backup, err := openRunnerBackupSource(ctx, absolute)
	if err != nil {
		return RecoveryBackup{}, err
	}
	defer backup.close()
	var check string
	if err := backup.conn.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&check); err != nil {
		return RecoveryBackup{}, fmt.Errorf("check recovery backup: %w", err)
	}
	if check != "ok" {
		return RecoveryBackup{}, errors.New("recovery backup integrity check failed")
	}
	current, err := os.Lstat(absolute)
	if err != nil {
		return RecoveryBackup{}, err
	}
	if !current.Mode().IsRegular() || !os.SameFile(created, current) {
		return RecoveryBackup{}, errors.New("recovery backup file identity changed")
	}
	if err := source.verifyIdentity(); err != nil {
		return RecoveryBackup{}, err
	}
	digest, size, err := runnerBackupFileHash(ctx, file)
	if err != nil {
		return RecoveryBackup{}, err
	}
	if err := file.Close(); err != nil {
		return RecoveryBackup{}, err
	}
	return RecoveryBackup{SHA256: digest, Bytes: size}, nil
}
