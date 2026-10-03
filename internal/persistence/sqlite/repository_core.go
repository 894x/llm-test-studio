// Package sqlite implements the durable operational repositories for the Go Core.
package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrNotFound = errors.New("sqlite repository record not found")
	ErrConflict = errors.New("sqlite repository revision conflict")
	ErrCorrupt  = errors.New("sqlite repository data is corrupt")
)

type RepositoryOptions struct {
	BusyTimeout time.Duration
}

// Repository uses a one-connection pool. database/sql reserves that connection
// for each complete transaction and queues other operations with their context.
type Repository struct {
	db                 *sql.DB
	reportProjectionMu sync.Mutex
	reportProjections  map[string]cachedReportProjection
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func OpenRepository(ctx context.Context, path string, options RepositoryOptions) (*Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite repository path is required")
	}
	busyTimeout, err := normalizeBusyTimeout(options.BusyTimeout)
	if err != nil {
		return nil, err
	}
	dsn, err := repositoryPoolDSN(path, busyTimeout)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite repository: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("acquire sqlite repository connection: %w", err)
	}
	cleanup := func() {
		_ = conn.Close()
		_ = db.Close()
	}
	if err := configureConnection(ctx, conn, busyTimeout); err != nil {
		cleanup()
		return nil, err
	}
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: validate operational repository schema: %w", ErrCorrupt, err)
	}
	if version != CurrentSchemaVersion {
		cleanup()
		return nil, fmt.Errorf("%w: operational repository requires schema version %d", ErrCorrupt, CurrentSchemaVersion)
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: validate operational repository integrity: %v", ErrCorrupt, err)
	}
	// Return the configured connection to the pool; keeping a shared *sql.Conn
	// bypasses transaction reservation and permits overlapping BEGIN statements.
	if err := conn.Close(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("release sqlite repository connection: %w", err)
	}
	return &Repository{db: db}, nil
}

func repositoryPoolDSN(value string, busyTimeout time.Duration) (string, error) {
	dsn, err := repositoryDSN(value)
	if err != nil {
		return "", err
	}
	base, rawQuery, _ := strings.Cut(dsn, "?")
	query, _ := url.ParseQuery(rawQuery)
	pragmas := make([]string, 0, len(query["_pragma"])+2)
	for _, pragma := range query["_pragma"] {
		name := strings.ToLower(strings.TrimSpace(pragma))
		if strings.HasPrefix(name, "foreign_keys") || strings.HasPrefix(name, "busy_timeout") {
			continue
		}
		pragmas = append(pragmas, pragma)
	}
	milliseconds := max(int64(1), busyTimeout.Milliseconds())
	// Connection-local settings must also apply after cancellation or a driver
	// error makes database/sql replace the physical connection.
	query["_pragma"] = append(pragmas, "foreign_keys(1)", fmt.Sprintf("busy_timeout(%d)", milliseconds))
	return base + "?" + query.Encode(), nil
}

func repositoryDSN(value string) (string, error) {
	base, rawQuery, _ := strings.Cut(value, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", fmt.Errorf("parse sqlite repository DSN: %w", err)
	}
	query.Set("_txlock", "immediate")
	return base + "?" + query.Encode(), nil
}

func (repository *Repository) Close() error {
	if repository == nil {
		return nil
	}
	if repository.db != nil {
		return repository.db.Close()
	}
	return nil
}

func validateNextRevision(expected uint64, meta domain.EntityMeta) error {
	if expected == math.MaxUint64 || meta.Revision != expected+1 {
		return fmt.Errorf("%w: entity revision must be expected revision plus one", ErrConflict)
	}
	if expected > math.MaxInt64 || meta.Revision > math.MaxInt64 {
		return errors.New("entity revision exceeds SQLite integer range")
	}
	return nil
}

func marshalCanonical(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func decodeCanonical(document []byte, destination any, validate func() error) error {
	if err := json.Unmarshal(document, destination); err != nil {
		return err
	}
	if err := validate(); err != nil {
		return err
	}
	canonical, err := marshalCanonical(destination)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, document) {
		return errors.New("stored document is not canonical JSON")
	}
	return nil
}

func verifyDocumentIdentity(document []byte, id string, revision uint64) error {
	var identity struct {
		ID       string `json:"id"`
		Revision uint64 `json:"revision"`
	}
	if err := json.Unmarshal(document, &identity); err != nil {
		return err
	}
	if identity.ID != id || identity.Revision != revision {
		return errors.New("document identity does not match its row")
	}
	return nil
}

func verifyEntityRow(document []byte, id string, schemaVersion, revision int64, createdAt, updatedAt string) error {
	var meta domain.EntityMeta
	if err := json.Unmarshal(document, &meta); err != nil {
		return err
	}
	if meta.ID != id || int64(meta.SchemaVersion) != schemaVersion || int64(meta.Revision) != revision ||
		formatTime(meta.CreatedAt) != createdAt || formatTime(meta.UpdatedAt) != updatedAt {
		return errors.New("document metadata does not match its row")
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func classifyWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "constraint failed") || strings.Contains(lower, "unique constraint") || strings.Contains(lower, "primary key") {
		return fmt.Errorf("%w: %s", ErrConflict, operation)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
