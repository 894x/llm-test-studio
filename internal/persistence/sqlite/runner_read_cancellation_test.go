package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	sqlitedriver "modernc.org/sqlite"
)

func TestRunnerReadViewCanceledQueryReleasesNativeSnapshot(t *testing.T) {
	ctx, finish := context.WithTimeout(t.Context(), 10*time.Second)
	defer finish()
	writer, reader := openReadViewFixture(t)
	run := readViewRunFixture(t)
	if err := writer.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.ExecContext(ctx, `PRAGMA wal_autocheckpoint = 0`); err != nil {
		t.Fatal(err)
	}
	var busy, frames, checkpointed int
	err := writer.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &frames, &checkpointed)
	if err != nil || busy != 0 {
		t.Fatalf("establish checkpoint baseline: busy=%d, err=%v", busy, err)
	}
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	entered, joined := make(chan struct{}), make(chan struct{})
	var nameBytes [16]byte
	if _, err := rand.Read(nameBytes[:]); err != nil {
		t.Fatal(err)
	}
	functionName := "read_view_cancel_" + hex.EncodeToString(nameBytes[:])
	err = sqlitedriver.RegisterDeterministicScalarFunction(
		functionName,
		1,
		func(_ *sqlitedriver.FunctionContext, values []driver.Value) (driver.Value, error) {
			close(entered)
			<-queryCtx.Done()
			// Return after the driver's interrupt races with the active native step.
			time.Sleep(time.Millisecond)
			return values[0], nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Registered functions are installed when modernc opens a connection. The
	// fixture already opened its reader, so replace only that setup connection
	// before the cancellation under test; never discard one after cancellation.
	setupConnection, err := reader.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer setupConnection.Close()
	setupErr := setupConnection.Raw(func(any) error { return driver.ErrBadConn })
	if !errors.Is(setupErr, driver.ErrBadConn) {
		t.Fatalf("replace setup connection before scalar query: %v", setupErr)
	}
	queryErrors := make(chan error, 1)
	// Only the generated hexadecimal identifier is interpolated; run identity
	// remains a bound value. No physical connection lease masks pool replacement.
	query := fmt.Sprintf(`SELECT %s(current_revision) FROM execution_runs WHERE id=?`, functionName)
	go func() {
		defer close(joined)
		rows, queryErr := reader.db.QueryContext(queryCtx, query, run.Meta().ID)
		if rows != nil {
			queryErr = errors.Join(queryErr, rows.Close())
		}
		queryErrors <- queryErr
	}()
	t.Cleanup(func() {
		cancel()
		<-joined
	})
	select {
	case <-entered:
	case queryErr := <-queryErrors:
		t.Fatalf("query returned before entering its scalar gate: %v", queryErr)
	case <-ctx.Done():
		t.Fatal("reader scalar did not enter the native query")
	}
	cancel()
	select {
	case queryErr := <-queryErrors:
		if !errors.Is(queryErr, context.Canceled) {
			t.Fatalf("query did not return context cancellation: %v", queryErr)
		}
	case <-ctx.Done():
		t.Fatal("cancelled query did not return")
	}
	<-joined
	if stats := reader.db.Stats(); stats.InUse != 0 {
		t.Fatalf("cancelled query retained a pooled lease: %+v", stats)
	}
	next, err := run.Transition(domain.RunStarting, run.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.UpdateRun(ctx, run.Meta().Revision, next); err != nil {
		t.Fatalf("execution writer failed after reader cancellation: %v", err)
	}
	loaded, readErr := reader.GetRun(ctx, run.Meta().ID)
	if readErr != nil || loaded.Meta().Revision != next.Meta().Revision {
		t.Errorf("pooled reader revision=%d, expected=%d, err=%v",
			loaded.Meta().Revision,
			next.Meta().Revision,
			readErr,
		)
	}
	err = writer.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &checkpointed)
	t.Logf(
		"joined reader revision=%d; checkpoint busy=%d frames=%d checkpointed=%d err=%v",
		loaded.Meta().Revision,
		busy,
		frames,
		checkpointed,
		err,
	)
	// A replacement pooled connection can read correctly while an abandoned
	// native statement still pins the old WAL snapshot. Check both independently.
	complete := err == nil && busy == 0 && frames > 0 && checkpointed == frames
	if !complete {
		t.Fatal("cancelled native Rows retained a WAL snapshot despite joining and closing returned Rows")
	}
}
