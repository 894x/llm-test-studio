package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
)

func historicalJobTable(name string) bool {
	switch name {
	case "execution_jobs", "execution_job_runs", "execution_job_events", "execution_job_commands",
		"execution_performance_observations", "execution_performance_results",
		"remote_job_mirrors", "remote_job_runs", "remote_job_events", "remote_job_report_refs",
		"remote_performance_results", "job_submissions", "job_control_outbox":
		return true
	default:
		return false
	}
}

// archiveHistoricalJobs preserves every cell, including original document bytes,
// before removing pre-origin jobs from executable queues. Source row hashes are
// checked against the archive in the same transaction before commit.
func archiveHistoricalJobs(ctx context.Context, conn *sql.Conn, baseline historicalBaseline) error {
	for _, object := range baseline.objects {
		if object.kind != "table" || !historicalJobTable(object.name) {
			continue
		}
		if err := archiveHistoricalTable(ctx, conn, baseline.Checksum, object.name); err != nil {
			return err
		}
	}
	for _, table := range []string{"execution_jobs", "remote_job_mirrors", "job_submissions"} {
		column := "job_id"
		if table == "execution_jobs" {
			column = "id"
		}
		fields, projection := "job_id, comparison_id, performance_report_id", column+", comparison_id, performance_report_id"
		if table == "execution_jobs" {
			projection = "id, json_extract(snapshot_json, '$.receipt.comparison_id'), " +
				"json_extract(spec_json, '$.spec.performance.report_id')"
		}
		if table == "job_submissions" {
			fields, projection = "job_id", "job_id"
		}
		query := "INSERT OR IGNORE INTO upgrade_history_jobs(" + fields + ") SELECT " + projection + " FROM " + table
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	for _, table := range []string{"execution_job_runs", "remote_job_runs"} {
		query := "INSERT INTO upgrade_history_runs(run_id, job_id, position) " +
			"SELECT run_id, job_id, position FROM " + table
		if _, err := conn.ExecContext(ctx, query); err != nil {
			return err
		}
	}
	for _, object := range baseline.objects {
		if object.kind == "table" && historicalJobTable(object.name) {
			if _, err := conn.ExecContext(ctx, "DELETE FROM "+quoteMigrationName(object.name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func archiveHistoricalTable(ctx context.Context, conn *sql.Conn, source, table string) error {
	columns, err := migrationColumns(ctx, conn, table)
	if err != nil {
		return err
	}
	fact := migrationTableFacts{table: table, columns: columns}
	digest, err := migrationTableDigest(ctx, conn, fact)
	if err != nil {
		return err
	}
	names, err := json.Marshal(columns)
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(
		ctx,
		`INSERT INTO upgrade_history_tables(table_name, source_checksum, columns_json, row_count, sha256)
		VALUES(?, ?, ?, 0, ?)`,
		table,
		source,
		string(names),
		digest,
	); err != nil {
		return err
	}
	projection := make([]string, len(columns))
	for i, column := range columns {
		projection[i] = quoteMigrationName(column)
	}
	selectColumns := strings.Join(projection, ", ")
	query := "SELECT " + selectColumns + " FROM " + quoteMigrationName(table) + " ORDER BY " + selectColumns
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	var position int64
	for rows.Next() {
		values, pointers := migrationScanValues(len(columns))
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		encoded, err := encodeHistoryValues(values)
		if err != nil {
			return err
		}
		position++
		if _, err := conn.ExecContext(
			ctx,
			`INSERT INTO upgrade_history_rows(table_name, position, values_json) VALUES(?, ?, ?)`,
			table,
			position,
			string(encoded),
		); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = conn.ExecContext(
		ctx,
		"UPDATE upgrade_history_tables SET row_count = ? WHERE table_name = ?",
		position,
		table,
	)
	return err
}

type historyValue struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

func migrationScanValues(count int) ([]any, []any) {
	values, pointers := make([]any, count), make([]any, count)
	for i := range values {
		pointers[i] = &values[i]
	}
	return values, pointers
}

func encodeHistoryValues(values []any) ([]byte, error) {
	encoded := make([]historyValue, len(values))
	for i, value := range values {
		switch typed := value.(type) {
		case nil:
			encoded[i] = historyValue{Kind: "null"}
		case string:
			encoded[i] = historyValue{Kind: "text", Value: typed}
		case []byte:
			encoded[i] = historyValue{Kind: "blob", Value: base64.StdEncoding.EncodeToString(typed)}
		case int64:
			encoded[i] = historyValue{Kind: "integer", Value: strconv.FormatInt(typed, 10)}
		case float64:
			encoded[i] = historyValue{Kind: "real", Value: strconv.FormatUint(math.Float64bits(typed), 16)}
		default:
			return nil, ErrCorrupt
		}
	}
	return json.Marshal(encoded)
}

func decodeHistoryValues(document []byte) ([]any, error) {
	var encoded []historyValue
	if err := decodeCanonical(document, &encoded, nil); err != nil {
		return nil, ErrCorrupt
	}
	values := make([]any, len(encoded))
	for i, value := range encoded {
		var err error
		switch value.Kind {
		case "null":
			if value.Value != "" {
				return nil, ErrCorrupt
			}
		case "text":
			values[i] = value.Value
		case "blob":
			values[i], err = base64.StdEncoding.DecodeString(value.Value)
		case "integer":
			values[i], err = strconv.ParseInt(value.Value, 10, 64)
		case "real":
			var bits uint64
			bits, err = strconv.ParseUint(value.Value, 16, 64)
			values[i] = math.Float64frombits(bits)
		default:
			return nil, ErrCorrupt
		}
		if err != nil {
			return nil, ErrCorrupt
		}
	}
	return values, nil
}

func historyTableDigest(ctx context.Context, queryer relationQueryer, table string, width int) (string, int64, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT position, values_json FROM upgrade_history_rows
		WHERE table_name = ? ORDER BY position`, table)
	if err != nil {
		return "", 0, err
	}
	defer rows.Close()
	digest := sha256.New()
	var count int64
	for rows.Next() {
		var position int64
		var document []byte
		if err := rows.Scan(&position, &document); err != nil {
			return "", 0, err
		}
		values, err := decodeHistoryValues(document)
		if err != nil || len(values) != width || position != count+1 {
			return "", 0, ErrCorrupt
		}
		writeRunnerDigest(digest, 'R', nil)
		for _, value := range values {
			if err := hashRunnerValue(digest, value); err != nil {
				return "", 0, err
			}
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(digest.Sum(nil)), count, nil
}

func validateUpgradeHistory(ctx context.Context, queryer relationQueryer) error {
	catalog, err := loadMigrationCatalog()
	if err != nil {
		return err
	}
	rows, err := queryer.QueryContext(ctx, `SELECT table_name, source_checksum, columns_json, row_count, sha256
		FROM upgrade_history_tables ORDER BY table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var archiveSource string
	var manifestCount int
	for rows.Next() {
		var table, source, columnsJSON, expected string
		var count int64
		if err := rows.Scan(&table, &source, &columnsJSON, &count, &expected); err != nil {
			return err
		}
		baseline, found := catalog.baseline(source)
		if archiveSource != "" && archiveSource != source {
			return ErrCorrupt
		}
		archiveSource = source
		manifestCount++
		var columns []string
		validSource := found && baseline.lacksExecutionOrigin() && historicalJobTable(table)
		validColumns := json.Unmarshal([]byte(columnsJSON), &columns) == nil && len(columns) != 0
		if !validSource || !validColumns {
			return ErrCorrupt
		}
		expectedColumns, err := migrationColumns(ctx, queryer, table)
		if err != nil {
			return err
		}
		if !slices.Equal(columns, expectedColumns) {
			return ErrCorrupt
		}
		actual, actualCount, err := historyTableDigest(ctx, queryer, table, len(columns))
		if err != nil {
			return err
		}
		if actual != expected || actualCount != count {
			return ErrCorrupt
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var ledgerSource string
	ledgerRow := queryer.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version = 1")
	if err := ledgerRow.Scan(&ledgerSource); err != nil {
		return err
	}
	baseline, found := catalog.baseline(ledgerSource)
	if !found {
		return ErrCorrupt
	}
	var expectedCount int
	if baseline.lacksExecutionOrigin() {
		for _, object := range baseline.objects {
			if object.kind == "table" && historicalJobTable(object.name) {
				expectedCount++
			}
		}
	}
	if expectedCount != manifestCount || (manifestCount != 0 && ledgerSource != archiveSource) {
		return ErrCorrupt
	}
	var duplicate bool
	if err := queryer.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs h
		WHERE EXISTS(SELECT 1 FROM execution_jobs j WHERE j.id = h.job_id)
		OR EXISTS(SELECT 1 FROM remote_job_mirrors j WHERE j.job_id = h.job_id)
		OR EXISTS(SELECT 1 FROM job_submissions j WHERE j.job_id = h.job_id))
		OR EXISTS(SELECT 1 FROM upgrade_history_runs h WHERE
		EXISTS(SELECT 1 FROM execution_job_runs r WHERE r.run_id = h.run_id)
		OR EXISTS(SELECT 1 FROM remote_job_runs r WHERE r.run_id = h.run_id))`).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return errors.New("historical job archive conflicts with current execution ownership")
	}
	return validateArchiveProjections(ctx, queryer)
}

func validateArchiveProjections(ctx context.Context, queryer relationQueryer) error {
	var links int64
	tables := []string{"execution_jobs", "remote_job_mirrors", "job_submissions", "execution_job_runs", "remote_job_runs"}
	for _, table := range tables {
		columns, err := migrationColumns(ctx, queryer, table)
		if err != nil {
			return err
		}
		rows, err := queryer.QueryContext(ctx, `SELECT values_json FROM upgrade_history_rows
			WHERE table_name = ? ORDER BY position`, table)
		if err != nil {
			return err
		}
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var document []byte
				if err := rows.Scan(&document); err != nil {
					return err
				}
				values, err := decodeHistoryValues(document)
				if err != nil || len(values) != len(columns) {
					return ErrCorrupt
				}
				record := runnerBackupRecord{}
				for i, value := range values {
					record[columns[i]] = value
				}
				jobID := record.text("job_id")
				if table == "execution_jobs" {
					jobID = record.text("id")
				}
				if !domain.IsUUID(jobID) {
					return ErrCorrupt
				}
				var present bool
				jobRow := queryer.QueryRowContext(ctx,
					`SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs WHERE job_id = ?)`, jobID)
				if err := jobRow.Scan(&present); err != nil {
					return err
				}
				if !present {
					return ErrCorrupt
				}
				if table == "execution_jobs" || table == "remote_job_mirrors" {
					comparison, performance := record.text("comparison_id"), record.text("performance_report_id")
					if table == "execution_jobs" {
						var spec struct {
							Spec struct {
								Performance *struct {
									ReportID string `json:"report_id"`
								} `json:"performance"`
								Comparison *struct {
									Meta struct {
										ID string `json:"id"`
									} `json:"meta"`
								} `json:"comparison"`
							} `json:"spec"`
						}
						if json.Unmarshal([]byte(record.text("spec_json")), &spec) != nil {
							return ErrCorrupt
						}
						if spec.Spec.Performance != nil {
							performance = spec.Spec.Performance.ReportID
						}
						if spec.Spec.Comparison != nil {
							comparison = spec.Spec.Comparison.Meta.ID
						}
					}
					var comparisonID, performanceID any
					if comparison != "" {
						comparisonID = comparison
					}
					if performance != "" {
						performanceID = performance
					}
					resourceRow := queryer.QueryRowContext(
						ctx,
						`SELECT EXISTS(SELECT 1 FROM upgrade_history_jobs
						WHERE job_id = ? AND comparison_id IS ? AND performance_report_id IS ?)`,
						jobID,
						comparisonID,
						performanceID,
					)
					if err := resourceRow.Scan(&present); err != nil {
						return err
					}
					if !present {
						return ErrCorrupt
					}
				}
				if table == "execution_job_runs" || table == "remote_job_runs" {
					runRow := queryer.QueryRowContext(
						ctx,
						`SELECT EXISTS(SELECT 1 FROM upgrade_history_runs
						WHERE run_id = ? AND job_id = ? AND position = ?)`,
						record.text("run_id"),
						jobID,
						record.integer("position"),
					)
					if err := runRow.Scan(&present); err != nil {
						return err
					}
					if !present {
						return ErrCorrupt
					}
					links++
				}
			}
			return rows.Err()
		}()
		if err != nil {
			return err
		}
	}
	var actualLinks int64
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM upgrade_history_runs`).Scan(&actualLinks); err != nil {
		return err
	}
	if links != actualLinks {
		return ErrCorrupt
	}
	var actualJobs, expectedJobs int64
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM upgrade_history_jobs`).Scan(&actualJobs); err != nil {
		return err
	}
	// The frozen column order is checked against the current unchanged tables
	// above: execution_jobs.id is column 1; mirror/submission.job_id is column 0.
	if err := queryer.QueryRowContext(ctx, `SELECT COUNT(*) FROM (
		SELECT json_extract(values_json, '$[1].value') FROM upgrade_history_rows WHERE table_name = 'execution_jobs'
		UNION SELECT json_extract(values_json, '$[0].value') FROM upgrade_history_rows
		WHERE table_name IN ('remote_job_mirrors', 'job_submissions')
	)`).Scan(&expectedJobs); err != nil {
		return err
	}
	if actualJobs != expectedJobs {
		return ErrCorrupt
	}
	return nil
}
