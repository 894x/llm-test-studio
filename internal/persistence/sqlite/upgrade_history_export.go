package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// ExportUpgradeHistory writes a lossless JSONL archive from a consistent,
// read-only snapshot. It never migrates the source or overwrites an existing
// destination. A complete export always ends with a complete record.
func ExportUpgradeHistory(ctx context.Context, path, destination string) (result error) {
	source, err := openRunnerBackupSource(ctx, path)
	if err != nil {
		return err
	}
	defer source.close()
	version, err := appliedMigrationVersion(ctx, source.conn)
	if err != nil {
		return err
	}
	if version != CurrentSchemaVersion {
		return errors.New("open the application to upgrade this database before exporting history")
	}
	if err := validateIntegrity(ctx, source.conn); err != nil {
		return err
	}
	if err := validateUpgradeHistory(ctx, source.conn); err != nil {
		return err
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	created, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return err
	}
	defer func() {
		if err := file.Close(); result == nil {
			result = err
		}
		if result != nil {
			if current, err := os.Lstat(destination); err == nil && os.SameFile(created, current) {
				_ = os.Remove(destination)
			}
		}
	}()
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(map[string]any{"type": "upgrade_history", "schema_version": 1}); err != nil {
		return err
	}
	rows, err := source.conn.QueryContext(ctx, `SELECT table_name, source_checksum, columns_json, row_count, sha256
		FROM upgrade_history_tables ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var name, checksum, columns, digest string
		var count int64
		if err := rows.Scan(&name, &checksum, &columns, &count, &digest); err != nil {
			return err
		}
		if err := encoder.Encode(map[string]any{"type": "table", "table": name, "source_checksum": checksum,
			"columns": json.RawMessage(columns), "row_count": count, "sha256": digest}); err != nil {
			return err
		}
		if err := exportHistoryRows(ctx, source, encoder, name); err != nil {
			return err
		}
		total += count
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := source.verifyIdentity(); err != nil {
		return err
	}
	if err := encoder.Encode(map[string]any{"type": "complete", "rows": total}); err != nil {
		return err
	}
	return file.Sync()
}

func exportHistoryRows(ctx context.Context, source *runnerBackupSource, encoder *json.Encoder, table string) error {
	rows, err := source.conn.QueryContext(ctx, `SELECT position, values_json FROM upgrade_history_rows
		WHERE table_name = ? ORDER BY position`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var position int64
		var document string
		if err := rows.Scan(&position, &document); err != nil {
			return err
		}
		record := map[string]any{
			"type": "row", "table": table, "position": position, "values": json.RawMessage(document),
		}
		if err := encoder.Encode(record); err != nil {
			return err
		}
	}
	return rows.Err()
}
