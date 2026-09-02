// Package sqlite implements the durable local repositories for the Go Core.
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
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrNotFound            = errors.New("sqlite repository record not found")
	ErrConflict            = errors.New("sqlite repository revision conflict")
	ErrCorrupt             = errors.New("sqlite repository data is corrupt")
	ErrAmbiguousPlanTarget = errors.New("sqlite plan must contain exactly one model and one channel")
)

type RepositoryOptions struct {
	BusyTimeout time.Duration
}

// Repository owns one configured SQLite connection. Holding a single
// connection keeps PRAGMA foreign_keys effective for every repository call and
// gives local writes a deterministic transaction boundary.
type Repository struct {
	db   *sql.DB
	conn *sql.Conn
}

func OpenRepository(ctx context.Context, path string, options RepositoryOptions) (*Repository, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("sqlite repository path is required")
	}
	busyTimeout := options.BusyTimeout
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTime
	}
	if busyTimeout < 0 {
		return nil, errors.New("sqlite repository busy timeout cannot be negative")
	}
	dsn, err := repositoryDSN(path)
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
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		cleanup()
		return nil, fmt.Errorf("enable sqlite repository foreign keys: %w", err)
	}
	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		cleanup()
		return nil, fmt.Errorf("verify sqlite repository foreign keys: %w", err)
	}
	if foreignKeys != 1 {
		cleanup()
		return nil, errors.New("sqlite repository foreign keys could not be enabled")
	}
	busyMilliseconds := busyTimeout.Milliseconds()
	if busyTimeout > 0 && busyMilliseconds == 0 {
		busyMilliseconds = 1
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", busyMilliseconds)); err != nil {
		cleanup()
		return nil, fmt.Errorf("set sqlite repository busy timeout: %w", err)
	}
	var configuredBusyMilliseconds int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&configuredBusyMilliseconds); err != nil {
		cleanup()
		return nil, fmt.Errorf("verify sqlite repository busy timeout: %w", err)
	}
	if configuredBusyMilliseconds != busyMilliseconds {
		cleanup()
		return nil, fmt.Errorf("sqlite repository busy timeout is %dms, want %dms", configuredBusyMilliseconds, busyMilliseconds)
	}
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: validate repository schema: %v", ErrCorrupt, err)
	}
	if version != CurrentSchemaVersion {
		cleanup()
		return nil, fmt.Errorf("%w: repository requires schema version %d", ErrCorrupt, CurrentSchemaVersion)
	}
	return &Repository{db: db, conn: conn}, nil
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
	var result error
	if repository.conn != nil {
		result = repository.conn.Close()
		repository.conn = nil
	}
	if repository.db != nil {
		if err := repository.db.Close(); result == nil {
			result = err
		}
		repository.db = nil
	}
	return result
}

func (repository *Repository) CreateModel(ctx context.Context, model domain.Model) error {
	if err := model.Validate(); err != nil {
		return fmt.Errorf("validate model before create: %w", err)
	}
	if model.Revision != 1 {
		return errors.New("new model revision must be 1")
	}
	document, err := marshalCanonical(model)
	if err != nil {
		return fmt.Errorf("encode model: %w", err)
	}
	_, err = repository.conn.ExecContext(ctx, `
		INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, model.ID, model.SchemaVersion, model.Revision, formatTime(model.CreatedAt), formatTime(model.UpdatedAt), document)
	if err != nil {
		return classifyWriteError("create model", err)
	}
	return nil
}

func (repository *Repository) GetModel(ctx context.Context, id string) (domain.Model, error) {
	document, err := repository.getLatestDocument(ctx, "models", id, "model")
	if err != nil {
		return domain.Model{}, err
	}
	model, err := decodeModel(document)
	if err != nil {
		return domain.Model{}, fmt.Errorf("%w: model document", ErrCorrupt)
	}
	return model, nil
}

func (repository *Repository) UpdateModel(ctx context.Context, expectedRevision uint64, model domain.Model) error {
	if err := model.Validate(); err != nil {
		return fmt.Errorf("validate model before update: %w", err)
	}
	if err := validateNextRevision(expectedRevision, model.EntityMeta); err != nil {
		return err
	}
	document, err := marshalCanonical(model)
	if err != nil {
		return fmt.Errorf("encode model: %w", err)
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin model update: %w", err)
	}
	defer tx.Rollback()
	if err := checkVersionedWrite(ctx, tx, "models", model.EntityMeta, &expectedRevision); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO models(id, schema_version, revision, created_at, updated_at, document_json)
		VALUES(?, ?, ?, ?, ?, ?)
	`, model.ID, model.SchemaVersion, model.Revision, formatTime(model.CreatedAt), formatTime(model.UpdatedAt), document)
	if err != nil {
		return classifyWriteError("update model", err)
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit model update", err)
	}
	return nil
}

func (repository *Repository) DeleteModel(ctx context.Context, id string, expectedRevision uint64) error {
	return repository.deleteVersionedEntity(ctx, "models", "model", id, expectedRevision)
}

func (repository *Repository) ListModels(ctx context.Context) ([]domain.Model, error) {
	documents, err := repository.listLatestDocuments(ctx, "models", "models")
	if err != nil {
		return nil, err
	}
	models := make([]domain.Model, 0, len(documents))
	for _, document := range documents {
		model, err := decodeModel(document)
		if err != nil {
			return nil, fmt.Errorf("%w: model document", ErrCorrupt)
		}
		models = append(models, model)
	}
	return models, nil
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

func decodeModel(document []byte) (domain.Model, error) {
	var model domain.Model
	if err := json.Unmarshal(document, &model); err != nil {
		return domain.Model{}, err
	}
	if err := model.Validate(); err != nil {
		return domain.Model{}, err
	}
	canonical, err := marshalCanonical(model)
	if err != nil || !bytes.Equal(canonical, document) {
		return domain.Model{}, errors.New("model document is not canonical")
	}
	return model, nil
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
