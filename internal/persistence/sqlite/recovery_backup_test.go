package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryBackupPreservesUnsupportedSchemaAndRetainedWAL(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0",
		"CREATE TABLE old_records (id INTEGER PRIMARY KEY, value TEXT)",
		"INSERT INTO old_records VALUES (1,'historical record in WAL')",
	} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	mainBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.ReadFile(path + "-wal")
	if err != nil || len(walBefore) == 0 {
		t.Fatal("fixture did not retain WAL")
	}
	destination := filepath.Join(directory, "original.sqlite")
	backup, err := BackupIncompatibleDatabase(t.Context(), path, destination)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if backup.Bytes != int64(len(data)) || backup.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("backup verification metadata does not match persisted bytes")
	}
	mainAfter, _ := os.ReadFile(path)
	walAfter, _ := os.ReadFile(path + "-wal")
	if string(mainBefore) != string(mainAfter) || string(walBefore) != string(walAfter) {
		t.Fatal("online backup changed the original database or WAL")
	}
	copyDB, err := sql.Open("sqlite", runnerBackupURI(destination, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var value string
	if err := copyDB.QueryRowContext(t.Context(), "SELECT value FROM old_records WHERE id=1").Scan(&value); err != nil || value != "historical record in WAL" {
		t.Fatal("consistent backup omitted the uncheckpointed historical record")
	}
	if _, err := SchemaVersion(t.Context(), destination); err == nil {
		t.Fatal("backup silently converted the unsupported application schema")
	}
	standalone := filepath.Join(t.TempDir(), "admin-copy.sqlite")
	if err := os.WriteFile(standalone, data, 0o600); err != nil {
		t.Fatal(err)
	}
	adminCopy, err := sql.Open("sqlite", runnerBackupURI(standalone, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer adminCopy.Close()
	if err := adminCopy.QueryRowContext(t.Context(), "SELECT value FROM old_records WHERE id=1").Scan(&value); err != nil || value != "historical record in WAL" {
		t.Fatal("administrator copy required missing sidecar files")
	}
	// The original writer is still usable because no file namespace was replaced.
	if _, err := db.ExecContext(t.Context(), "INSERT INTO old_records VALUES (2,'later record')"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryBackupIsConsistentDuringWALWrites(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "source.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "CREATE TABLE state(a INTEGER,b INTEGER)", "INSERT INTO state VALUES(0,0)"} {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	started, stop, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		for count := 0; ; count++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if _, err := db.ExecContext(t.Context(), "UPDATE state SET a=a+1,b=b+1"); err != nil {
				done <- err
				return
			}
			if count == 0 {
				close(started)
			}
		}
	}()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("writer failed before backup started: %v", err)
	case <-t.Context().Done():
		close(stop)
		t.Fatal(t.Context().Err())
	}
	destination := filepath.Join(directory, "original.sqlite")
	_, backupErr := BackupIncompatibleDatabase(t.Context(), path, destination)
	close(stop)
	if err := <-done; err != nil || backupErr != nil {
		t.Fatalf("writer or backup failed: %v, %v", err, backupErr)
	}
	backup, err := sql.Open("sqlite", runnerBackupURI(destination, "ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var a, b int
	if err := backup.QueryRowContext(t.Context(), "SELECT a,b FROM state").Scan(&a, &b); err != nil || a != b || a < 1 {
		t.Fatal("backup did not contain a consistent committed WAL state")
	}
}

func TestRecoveryBackupRejectsCancellationAndExistingDestination(t *testing.T) {
	directory := t.TempDir()
	source, destination := filepath.Join(directory, "old.db"), filepath.Join(directory, "backup.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE old_records(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := BackupIncompatibleDatabase(ctx, source, destination); err == nil {
		t.Fatal("cancelled backup succeeded")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("cancelled backup created a destination")
	}
	if err := os.WriteFile(destination, []byte("existing backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := BackupIncompatibleDatabase(t.Context(), source, destination); err == nil {
		t.Fatal("existing destination was replaced")
	}
	if data, _ := os.ReadFile(destination); string(data) != "existing backup" {
		t.Fatal("existing backup changed")
	}
}
