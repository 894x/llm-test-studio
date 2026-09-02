package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/caseimport"
	"github.com/894x/llm-test-studio/internal/domain"
)

// LoadCaseImportState reads import metadata and the current Test Case catalog
// from one SQLite snapshot. Historical imported revisions are verified before
// the state is returned to the application service.
func (repository *Repository) LoadCaseImportState(ctx context.Context, namespace string) (caseimport.State, error) {
	if repository == nil || repository.conn == nil {
		return caseimport.State{}, errors.New("sqlite case import repository is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return caseimport.State{}, err
	}
	if strings.TrimSpace(namespace) == "" || strings.TrimSpace(namespace) != namespace {
		return caseimport.State{}, errors.New("case import namespace is required")
	}
	tx, err := repository.conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return caseimport.State{}, fmt.Errorf("begin case import snapshot: %w", err)
	}
	defer tx.Rollback()

	sources, err := loadCaseImportSources(ctx, tx, namespace)
	if err != nil {
		return caseimport.State{}, err
	}
	cases, err := loadCurrentImportedCases(ctx, tx)
	if err != nil {
		return caseimport.State{}, err
	}
	for _, source := range sources {
		if err := verifyImportedRevision(ctx, tx, source); err != nil {
			return caseimport.State{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return caseimport.State{}, fmt.Errorf("commit case import snapshot: %w", err)
	}
	return caseimport.State{Sources: sources, Cases: cases}, nil
}

// ApplyCaseImportBatch applies entity revisions and import-source metadata in
// one transaction. Both entity and source expectations are compared inside the
// transaction so a concurrent user edit cannot be overwritten or adopted.
func (repository *Repository) ApplyCaseImportBatch(ctx context.Context, batch caseimport.Batch) error {
	if repository == nil || repository.conn == nil {
		return errors.New("sqlite case import repository is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCaseImportBatch(batch); err != nil {
		return err
	}
	tx, err := repository.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin case import batch: %w", err)
	}
	defer tx.Rollback()

	for _, change := range batch.Changes {
		if err := applyCaseImportChange(ctx, tx, change); err != nil {
			return err
		}
	}
	for _, retirement := range batch.Retirements {
		stored, err := loadOneCaseImportSource(ctx, tx, batch.Namespace, retirement.SourceKey)
		if err != nil {
			return err
		}
		if stored.SourceBytesSHA256 != retirement.Expected.SourceBytesSHA256 || stored.ImportedRevision != retirement.Expected.ImportedRevision {
			return fmt.Errorf("%w: retire case import source", ErrConflict)
		}
		if err := verifyImportedRevision(ctx, tx, stored); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE test_case_import_sources SET retired_at = ?
			WHERE namespace = ? AND source_key = ?
			  AND source_bytes_sha256 = ? AND imported_revision = ?
			  AND retired_at IS NULL
		`, formatTime(retirement.RetiredAt), batch.Namespace, retirement.SourceKey,
			retirement.Expected.SourceBytesSHA256, retirement.Expected.ImportedRevision)
		if err != nil {
			return classifyWriteError("retire case import source", err)
		}
		if err := requireOneChangedRow(result, "retire case import source"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return classifyWriteError("commit case import batch", err)
	}
	return nil
}

func loadCaseImportSources(ctx context.Context, tx *sql.Tx, namespace string) ([]caseimport.SourceRecord, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT namespace, source_key, source_path, source_bytes_sha256,
		       semantic_sha256, materialized_sha256, converter_version,
		       entity_id, imported_revision, bundle_manifest_sha256,
		       imported_at, retired_at
		FROM test_case_import_sources
		WHERE namespace = ?
		ORDER BY source_key
	`, namespace)
	if err != nil {
		return nil, fmt.Errorf("load case import sources: %w", err)
	}
	defer rows.Close()
	sources := make([]caseimport.SourceRecord, 0)
	for rows.Next() {
		var source caseimport.SourceRecord
		var converterVersion, importedRevision int64
		var importedAt string
		var retiredAt sql.NullString
		if err := rows.Scan(
			&source.Namespace, &source.SourceKey, &source.SourcePath, &source.SourceBytesSHA256,
			&source.SemanticSHA256, &source.MaterializedSHA256, &converterVersion,
			&source.EntityID, &importedRevision, &source.BundleManifestSHA256,
			&importedAt, &retiredAt,
		); err != nil {
			return nil, fmt.Errorf("%w: scan case import source", ErrCorrupt)
		}
		if converterVersion < 1 || importedRevision < 1 {
			return nil, fmt.Errorf("%w: invalid case import source version", ErrCorrupt)
		}
		source.ConverterVersion = uint64(converterVersion)
		source.ImportedRevision = uint64(importedRevision)
		source.ImportedAt, err = parseCanonicalCaseImportTime(importedAt)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid case import timestamp", ErrCorrupt)
		}
		if retiredAt.Valid {
			value, parseErr := parseCanonicalCaseImportTime(retiredAt.String)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: invalid case import retirement timestamp", ErrCorrupt)
			}
			source.RetiredAt = &value
		}
		if err := validateCaseImportSource(source, namespace); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate case import sources: %w", err)
	}
	return sources, nil
}

func loadCurrentImportedCases(ctx context.Context, tx *sql.Tx) ([]domain.TestCase, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, schema_version, revision, created_at, updated_at, document_json
		FROM test_cases AS candidate
		WHERE revision = (SELECT MAX(revision) FROM test_cases WHERE id = candidate.id)
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, fmt.Errorf("load current test cases for import: %w", err)
	}
	defer rows.Close()
	cases := make([]domain.TestCase, 0)
	for rows.Next() {
		var id, createdAt, updatedAt string
		var schemaVersion, revision int64
		var document []byte
		if err := rows.Scan(&id, &schemaVersion, &revision, &createdAt, &updatedAt, &document); err != nil {
			return nil, fmt.Errorf("%w: scan current test case", ErrCorrupt)
		}
		if err := verifyEntityRow(document, id, schemaVersion, revision, createdAt, updatedAt); err != nil {
			return nil, fmt.Errorf("%w: test case row does not match document", ErrCorrupt)
		}
		var testCase domain.TestCase
		if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
			return nil, fmt.Errorf("%w: test case document", ErrCorrupt)
		}
		cases = append(cases, testCase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate current test cases for import: %w", err)
	}
	return cases, nil
}

func verifyImportedRevision(ctx context.Context, tx *sql.Tx, source caseimport.SourceRecord) error {
	document, err := exactDocument(ctx, tx, "test_cases", source.EntityID, source.ImportedRevision, "imported test case")
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("%w: imported test case revision is missing", ErrCorrupt)
	}
	var testCase domain.TestCase
	if err := decodeCanonical(document, &testCase, func() error { return testCase.Validate() }); err != nil {
		return fmt.Errorf("%w: imported test case document", ErrCorrupt)
	}
	hash, err := caseimport.MaterializedSHA256(testCase)
	if err != nil || hash != source.MaterializedSHA256 {
		return fmt.Errorf("%w: imported test case materialized hash mismatch", ErrCorrupt)
	}
	return nil
}

func applyCaseImportChange(ctx context.Context, tx *sql.Tx, change caseimport.Change) error {
	if change.ExpectedRecord != nil {
		stored, err := loadOneCaseImportSource(ctx, tx, change.Source.Namespace, change.Source.SourceKey)
		if err != nil {
			return err
		}
		if stored.EntityID != change.Source.EntityID ||
			stored.SourceBytesSHA256 != change.ExpectedRecord.SourceBytesSHA256 ||
			stored.ImportedRevision != change.ExpectedRecord.ImportedRevision {
			return fmt.Errorf("%w: update case import source", ErrConflict)
		}
		if err := verifyImportedRevision(ctx, tx, stored); err != nil {
			return err
		}
	}
	if change.WriteEntity {
		var expected *uint64
		if change.ExpectedRecord != nil {
			expected = &change.ExpectedCurrentRevision
		}
		if err := checkVersionedWrite(ctx, tx, "test_cases", change.TestCase.EntityMeta, expected); err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: imported test case", ErrConflict)
			}
			return err
		}
		document, err := marshalCanonical(change.TestCase)
		if err != nil {
			return fmt.Errorf("encode imported test case: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO test_cases(id, schema_version, revision, created_at, updated_at, document_json)
			VALUES(?, ?, ?, ?, ?, ?)
		`, change.TestCase.ID, change.TestCase.SchemaVersion, change.TestCase.Revision,
			formatTime(change.TestCase.CreatedAt), formatTime(change.TestCase.UpdatedAt), document); err != nil {
			return classifyWriteError("write imported test case", err)
		}
	} else {
		currentRevision, err := latestRevision(ctx, tx, "test_cases", change.TestCase.ID, "imported test case")
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return fmt.Errorf("%w: imported test case", ErrConflict)
			}
			return err
		}
		if currentRevision != change.ExpectedCurrentRevision {
			return fmt.Errorf("%w: imported test case", ErrConflict)
		}
		if err := verifyImportedRevision(ctx, tx, change.Source); err != nil {
			return err
		}
	}

	if change.ExpectedRecord == nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO test_case_import_sources(
				namespace, source_key, source_path, source_bytes_sha256,
				semantic_sha256, materialized_sha256, converter_version,
				entity_id, imported_revision, bundle_manifest_sha256,
				imported_at, retired_at
			) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
		`, sourceValues(change.Source)...); err != nil {
			return classifyWriteError("create case import source", err)
		}
		return nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE test_case_import_sources SET
			source_path = ?, source_bytes_sha256 = ?, semantic_sha256 = ?,
			materialized_sha256 = ?, converter_version = ?, entity_id = ?,
			imported_revision = ?, bundle_manifest_sha256 = ?, imported_at = ?, retired_at = NULL
		WHERE namespace = ? AND source_key = ? AND entity_id = ?
		  AND source_bytes_sha256 = ? AND imported_revision = ?
	`, change.Source.SourcePath, change.Source.SourceBytesSHA256, change.Source.SemanticSHA256,
		change.Source.MaterializedSHA256, change.Source.ConverterVersion, change.Source.EntityID,
		change.Source.ImportedRevision, change.Source.BundleManifestSHA256, formatTime(change.Source.ImportedAt),
		change.Source.Namespace, change.Source.SourceKey, change.Source.EntityID,
		change.ExpectedRecord.SourceBytesSHA256, change.ExpectedRecord.ImportedRevision)
	if err != nil {
		return classifyWriteError("update case import source", err)
	}
	return requireOneChangedRow(result, "update case import source")
}

func loadOneCaseImportSource(ctx context.Context, tx *sql.Tx, namespace, sourceKey string) (caseimport.SourceRecord, error) {
	var source caseimport.SourceRecord
	var converterVersion, importedRevision int64
	var importedAt string
	var retiredAt sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT namespace, source_key, source_path, source_bytes_sha256,
		       semantic_sha256, materialized_sha256, converter_version,
		       entity_id, imported_revision, bundle_manifest_sha256,
		       imported_at, retired_at
		FROM test_case_import_sources
		WHERE namespace = ? AND source_key = ?
	`, namespace, sourceKey).Scan(
		&source.Namespace, &source.SourceKey, &source.SourcePath, &source.SourceBytesSHA256,
		&source.SemanticSHA256, &source.MaterializedSHA256, &converterVersion,
		&source.EntityID, &importedRevision, &source.BundleManifestSHA256,
		&importedAt, &retiredAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return caseimport.SourceRecord{}, fmt.Errorf("%w: case import source", ErrConflict)
	}
	if err != nil {
		return caseimport.SourceRecord{}, fmt.Errorf("load case import source: %w", err)
	}
	if converterVersion < 1 || importedRevision < 1 {
		return caseimport.SourceRecord{}, fmt.Errorf("%w: invalid case import source version", ErrCorrupt)
	}
	source.ConverterVersion = uint64(converterVersion)
	source.ImportedRevision = uint64(importedRevision)
	source.ImportedAt, err = parseCanonicalCaseImportTime(importedAt)
	if err != nil {
		return caseimport.SourceRecord{}, fmt.Errorf("%w: invalid case import timestamp", ErrCorrupt)
	}
	if retiredAt.Valid {
		value, parseErr := parseCanonicalCaseImportTime(retiredAt.String)
		if parseErr != nil {
			return caseimport.SourceRecord{}, fmt.Errorf("%w: invalid case import retirement timestamp", ErrCorrupt)
		}
		source.RetiredAt = &value
	}
	if err := validateCaseImportSource(source, namespace); err != nil {
		return caseimport.SourceRecord{}, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	return source, nil
}

func sourceValues(source caseimport.SourceRecord) []any {
	return []any{
		source.Namespace, source.SourceKey, source.SourcePath, source.SourceBytesSHA256,
		source.SemanticSHA256, source.MaterializedSHA256, source.ConverterVersion,
		source.EntityID, source.ImportedRevision, source.BundleManifestSHA256,
		formatTime(source.ImportedAt),
	}
}

func requireOneChangedRow(result sql.Result, operation string) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read %s row count: %w", operation, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s", ErrConflict, operation)
	}
	return nil
}

func validateCaseImportBatch(batch caseimport.Batch) error {
	if strings.TrimSpace(batch.Namespace) == "" || strings.TrimSpace(batch.Namespace) != batch.Namespace {
		return errors.New("case import batch namespace is required")
	}
	keys := make(map[string]struct{}, len(batch.Changes)+len(batch.Retirements))
	for _, change := range batch.Changes {
		if _, duplicate := keys[change.Source.SourceKey]; duplicate {
			return errors.New("case import batch contains duplicate source keys")
		}
		keys[change.Source.SourceKey] = struct{}{}
		if err := validateCaseImportSource(change.Source, batch.Namespace); err != nil {
			return err
		}
		if change.Source.RetiredAt != nil {
			return errors.New("case import change cannot remain retired")
		}
		if err := change.TestCase.Validate(); err != nil {
			return fmt.Errorf("validate imported test case: %w", err)
		}
		if change.Source.EntityID != change.TestCase.ID || change.ExpectedCurrentRevision > math.MaxInt64 {
			return errors.New("case import change entity identity is invalid")
		}
		if change.ExpectedRecord == nil {
			if !change.WriteEntity || change.ExpectedCurrentRevision != 0 || change.Source.ImportedRevision != change.TestCase.Revision {
				return errors.New("new case import change is invalid")
			}
		} else {
			if change.ExpectedCurrentRevision == 0 || !validCaseImportSHA(change.ExpectedRecord.SourceBytesSHA256) || change.ExpectedRecord.ImportedRevision == 0 {
				return errors.New("case import change expectation is invalid")
			}
			if change.WriteEntity && change.Source.ImportedRevision != change.TestCase.Revision {
				return errors.New("updated case import revision is invalid")
			}
		}
		if change.WriteEntity {
			hash, err := caseimport.MaterializedSHA256(change.TestCase)
			if err != nil || hash != change.Source.MaterializedSHA256 {
				return errors.New("imported test case materialized hash does not match its source")
			}
		}
	}
	for _, retirement := range batch.Retirements {
		if strings.TrimSpace(retirement.SourceKey) == "" || strings.TrimSpace(retirement.SourceKey) != retirement.SourceKey ||
			!validCaseImportSHA(retirement.Expected.SourceBytesSHA256) || retirement.Expected.ImportedRevision == 0 ||
			!canonicalCaseImportTime(retirement.RetiredAt) {
			return errors.New("case import retirement is invalid")
		}
		if _, duplicate := keys[retirement.SourceKey]; duplicate {
			return errors.New("case import batch contains duplicate source keys")
		}
		keys[retirement.SourceKey] = struct{}{}
	}
	return nil
}

func validateCaseImportSource(source caseimport.SourceRecord, namespace string) error {
	if source.Namespace != namespace || strings.TrimSpace(source.SourceKey) == "" || strings.TrimSpace(source.SourceKey) != source.SourceKey ||
		strings.TrimSpace(source.SourcePath) == "" || strings.TrimSpace(source.SourcePath) != source.SourcePath ||
		!validCaseImportSHA(source.SourceBytesSHA256) || !validCaseImportSHA(source.SemanticSHA256) ||
		!validCaseImportSHA(source.MaterializedSHA256) || !validCaseImportSHA(source.BundleManifestSHA256) ||
		source.ConverterVersion == 0 || source.ConverterVersion > math.MaxInt64 || !domain.IsUUID(source.EntityID) ||
		source.ImportedRevision == 0 || source.ImportedRevision > math.MaxInt64 || !canonicalCaseImportTime(source.ImportedAt) ||
		(source.RetiredAt != nil && !canonicalCaseImportTime(*source.RetiredAt)) {
		return errors.New("case import source metadata is invalid")
	}
	return nil
}

func validCaseImportSHA(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func canonicalCaseImportTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func parseCanonicalCaseImportTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || formatTime(parsed) != value {
		return time.Time{}, errors.New("timestamp is not canonical UTC")
	}
	return parsed, nil
}
