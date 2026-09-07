package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestComparisonReferenceErrorsPreserveCancellation(t *testing.T) {
	t.Parallel()

	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	ref := func(index int) domain.EntityRevisionRef {
		return domain.EntityRevisionRef{ID: fmt.Sprintf("30000000-0000-4000-8000-%012d", index), Revision: 1}
	}
	comparison, err := domain.NewComparison(
		meta,
		ref(1),
		ref(2),
		[]domain.ComparisonRunRef{
			{Channel: ref(3), RunID: ref(4).ID},
			{Channel: ref(5), RunID: ref(6).ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	document, err := marshalCanonical(comparison)
	if err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		name  string
		cause error
		want  error
	}{
		{name: "cancelled", cause: context.Canceled, want: context.Canceled},
		{name: "deadline", cause: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "missing run", cause: sql.ErrNoRows, want: ErrCorrupt},
	} {
		t.Run(test.name, func(t *testing.T) {
			referenceQueried := false
			connection := &comparisonQueryConnection{query: func(query string) (driver.Rows, error) {
				switch {
				case strings.Contains(query, "FROM comparisons root"):
					return &comparisonQueryRows{values: [][]driver.Value{{
						meta.ID, int64(1), formatTime(meta.CreatedAt), int64(0),
						int64(1), int64(1), int64(1), int64(meta.SchemaVersion), int64(1),
						formatTime(meta.CreatedAt), formatTime(meta.UpdatedAt),
						ref(1).ID, int64(1), ref(2).ID, int64(1), string(domain.ComparisonRunning), document,
					}}}, nil
				case strings.Contains(query, "FROM comparison_runs"):
					return &comparisonQueryRows{values: [][]driver.Value{
						{int64(0), ref(3).ID, int64(1), ref(4).ID},
						{int64(1), ref(5).ID, int64(1), ref(6).ID},
					}}, nil
				case strings.Contains(query, "FROM execution_runs"):
					referenceQueried = true
					return nil, test.cause
				default:
					return nil, fmt.Errorf("unexpected query: %s", query)
				}
			}}
			db := sql.OpenDB(comparisonQueryConnector{connection: connection})
			defer db.Close()

			_, err := queryCurrentComparison(t.Context(), db, meta.ID)
			if !referenceQueried {
				t.Fatalf("did not reach the run reference query: %v", err)
			}
			if !errors.Is(err, test.want) {
				t.Fatalf("queryCurrentComparison() = %v, want %v", err, test.want)
			}
			if test.want != ErrCorrupt && errors.Is(err, ErrCorrupt) {
				t.Fatalf("context error was misclassified as corrupt: %v", err)
			}
		})
	}
}

// A minimal SQL driver injects failures only after valid comparison rows have
// been scanned, without a live database or timing-dependent cancellation.
type comparisonQueryConnector struct {
	connection *comparisonQueryConnection
}

func (connector comparisonQueryConnector) Connect(context.Context) (driver.Conn, error) {
	return connector.connection, nil
}

func (connector comparisonQueryConnector) Driver() driver.Driver { return connector }

func (connector comparisonQueryConnector) Open(string) (driver.Conn, error) {
	return connector.connection, nil
}

type comparisonQueryConnection struct {
	query func(string) (driver.Rows, error)
}

func (connection *comparisonQueryConnection) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected Prepare")
}

func (connection *comparisonQueryConnection) Close() error { return nil }

func (connection *comparisonQueryConnection) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected Begin")
}

func (connection *comparisonQueryConnection) QueryContext(
	_ context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	return connection.query(query)
}

type comparisonQueryRows struct {
	values [][]driver.Value
}

func (rows *comparisonQueryRows) Columns() []string {
	return make([]string, len(rows.values[0]))
}

func (rows *comparisonQueryRows) Close() error { return nil }

func (rows *comparisonQueryRows) Next(destination []driver.Value) error {
	if len(rows.values) == 0 {
		return io.EOF
	}
	copy(destination, rows.values[0])
	rows.values = rows.values[1:]
	return nil
}
