package sqlite

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRepositoryWaitsForTransactionAndPreservesQueueCancellation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scheduling.db")
	if err := Migrate(ctx, path, MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRepository(ctx, path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	tx, err := repository.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	queued, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := repository.ListRunProjections(queued); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("read behind an open transaction = %v, want cancellable queue wait", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListRunProjections(ctx); err != nil {
		t.Fatalf("read after transaction released = %v", err)
	}
}

func TestRepositoryReplacementConnectionKeepsForeignKeysAndBusyTimeout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "replacement.db")
	if err := Migrate(ctx, path, MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRepository(ctx, path+"?_pragma=foreign_keys(0)&_pragma=busy_timeout(1)", RepositoryOptions{BusyTimeout: 123 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	conn, err := repository.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	_ = conn.Close()
	var foreignKeys, busyTimeout int
	if err := repository.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := repository.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || busyTimeout != 123 {
		t.Fatalf("replacement connection foreign_keys=%d busy_timeout=%d", foreignKeys, busyTimeout)
	}
	if _, err := repository.db.ExecContext(ctx, `INSERT INTO execution_runs(id,current_revision,created_at) VALUES('missing-revision',1,'2000-01-01')`); err == nil {
		t.Fatal("replacement connection accepted a missing foreign-key target")
	}
	if _, err := repository.ListRunProjections(ctx); err != nil {
		t.Fatalf("connection after rejected write: %v", err)
	}
}
