package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// This driver injects Commit outcomes to test the repository's classification.
// The actual database/sql auto-rollback race is a separate real-driver probe.
type healthProbeConnector struct {
	commit func() error
}

func (connector healthProbeConnector) Connect(context.Context) (driver.Conn, error) {
	return healthProbeConnection{commit: connector.commit}, nil
}

func (connector healthProbeConnector) Driver() driver.Driver { return connector }

func (connector healthProbeConnector) Open(string) (driver.Conn, error) {
	return connector.Connect(context.Background())
}

type healthProbeConnection struct {
	commit func() error
}

func (connection healthProbeConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("health fixture does not prepare statements")
}

func (healthProbeConnection) Close() error { return nil }

func (connection healthProbeConnection) Begin() (driver.Tx, error) {
	return healthProbeTransaction{commit: connection.commit}, nil
}

func (connection healthProbeConnection) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return connection.Begin()
}

func (healthProbeConnection) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (healthProbeConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	if strings.HasPrefix(query, "SELECT EXISTS(") {
		return &healthProbeRows{columns: []string{"unsupported"}, values: [][]driver.Value{{false}}}, nil
	}
	return nil, errors.New("unexpected health content query")
}

type healthProbeRows struct {
	columns []string
	values  [][]driver.Value
}

func (rows *healthProbeRows) Columns() []string { return rows.columns }
func (rows *healthProbeRows) Close() error      { return nil }
func (rows *healthProbeRows) Next(values []driver.Value) error {
	if len(rows.values) == 0 {
		return io.EOF
	}
	copy(values, rows.values[0])
	rows.values = rows.values[1:]
	return nil
}

type healthProbeTransaction struct {
	commit func() error
}

func (transaction healthProbeTransaction) Commit() error { return transaction.commit() }
func (healthProbeTransaction) Rollback() error           { return nil }

func TestRunnerWriteProbeClassifiesOnlyCancelledCommitTxDone(t *testing.T) {
	for _, test := range []struct {
		name      string
		context   string
		commitErr error
		wantCause error
	}{
		{name: "own_deadline", context: "deadline", commitErr: sql.ErrTxDone, wantCause: context.DeadlineExceeded},
		{name: "owner_shutdown", context: "cancelled", commitErr: sql.ErrTxDone, wantCause: context.Canceled},
		{name: "tx_done_without_cancellation", commitErr: sql.ErrTxDone},
		{name: "deadline_and_io_failure", context: "deadline", commitErr: io.ErrClosedPipe},
		{name: "shutdown_and_io_failure", context: "cancelled", commitErr: io.ErrClosedPipe},
		{name: "deadline_and_combined_failure", context: "deadline", commitErr: errors.Join(sql.ErrTxDone, io.ErrClosedPipe)},
		{name: "deadline_after_success", context: "deadline"},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				if test.context == "deadline" {
					cancel()
					ctx, cancel = context.WithTimeout(t.Context(), time.Second)
				}
				defer cancel()
				db := sql.OpenDB(healthProbeConnector{commit: func() error {
					switch test.context {
					case "deadline":
						<-ctx.Done()
					case "cancelled":
						cancel()
					}
					return test.commitErr
				}})
				db.SetMaxOpenConns(1)
				defer db.Close()
				repository := &Repository{db: db}
				err := repository.CheckWritable(ctx)
				if test.commitErr == nil {
					if err != nil {
						t.Fatalf("successful commit changed into failure: %v", err)
					}
					return
				}
				if !errors.Is(err, test.commitErr) {
					t.Fatalf("probe lost the original commit error: %v", err)
				}
				for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
					if errors.Is(err, cause) != (test.wantCause == cause) {
						t.Fatalf("probe context classification for %v: %v", cause, err)
					}
				}
			})
		})
	}
}
