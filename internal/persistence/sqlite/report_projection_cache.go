package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"hash"

	"github.com/894x/llm-test-studio/internal/application/reporting"
)

type cachedReportProjection struct {
	digest     [sha256.Size]byte
	projection reporting.ReportProjection
	expected   reportRunExpectation
}

// Hash the exact durable inputs, including historical revisions and relation
// columns. A sealed report may reuse its validation only while all inputs are
// byte-identical; direct writes and writes from other connections invalidate it.
func reportProjectionDigest(ctx context.Context, tx *sql.Tx, row storedReportProjection) ([sha256.Size]byte, error) {
	digest := sha256.New()
	queries := []struct {
		query string
		id    string
	}{
		{query: `SELECT id, schema_version, run_id, generated_at, document_json FROM reports WHERE id = ?`, id: row.id},
		{query: `SELECT id, current_revision, created_at, sealed FROM execution_runs WHERE id = ?`, id: row.runID},
		{query: `SELECT id FROM reports WHERE run_id = ? ORDER BY id`, id: row.runID},
		{query: `SELECT run_id, schema_version, revision, created_at, updated_at, plan_id, plan_revision, status, snapshot_json, document_json
			FROM execution_run_revisions WHERE run_id = ? ORDER BY revision`, id: row.runID},
		{query: `SELECT id, schema_version, revision, created_at, updated_at, run_id, entry_id, case_id, request_id, document_json
			FROM case_results WHERE run_id = ? AND request_id IS NULL AND case_id IS NOT NULL ORDER BY id`, id: row.runID},
		{query: `SELECT id, schema_version, revision, created_at, updated_at, run_id, document_json
			FROM evidence WHERE run_id = ? ORDER BY id`, id: row.runID},
		{query: `SELECT id, run_id, name, relative_path, sha256, media_type, redacted, document_json
			FROM artifacts WHERE run_id = ? ORDER BY id`, id: row.runID},
		{query: `SELECT link.report_id, link.artifact_id, link.position,
			artifact.id, artifact.run_id, artifact.name, artifact.relative_path, artifact.sha256,
			artifact.media_type, artifact.redacted, artifact.document_json
			FROM report_attachments AS link LEFT JOIN artifacts AS artifact ON artifact.id = link.artifact_id
			WHERE link.report_id = ? ORDER BY link.position`, id: row.id},
	}
	for _, query := range queries {
		if err := hashReportRows(ctx, tx, digest, query.query, query.id); err != nil {
			return [sha256.Size]byte{}, err
		}
	}
	var result [sha256.Size]byte
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func hashReportRows(ctx context.Context, tx *sql.Tx, digest hash.Hash, query, id string) error {
	rows, err := tx.QueryContext(ctx, query, id)
	if err != nil {
		return reportProjectionReadError(ctx, "query report validation inputs", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	values := make([]sql.RawBytes, len(columns))
	destinations := make([]any, len(columns))
	for index := range values {
		destinations[index] = &values[index]
	}
	_, _ = digest.Write([]byte(query))
	var size [8]byte
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := rows.Scan(destinations...); err != nil {
			return err
		}
		_, _ = digest.Write([]byte{1})
		for _, value := range values {
			// Zero identifies SQL NULL; empty bytes have length marker one.
			var length uint64
			if value != nil {
				length = uint64(len(value)) + 1
			}
			binary.LittleEndian.PutUint64(size[:], length)
			_, _ = digest.Write(size[:])
			_, _ = digest.Write(value)
		}
	}
	if err := rows.Err(); err != nil {
		return reportProjectionReadError(ctx, "read report validation inputs", err)
	}
	_, _ = digest.Write([]byte{0})
	return rows.Close()
}
