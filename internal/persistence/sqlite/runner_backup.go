package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	sqlitedriver "modernc.org/sqlite"
)

// RunnerBackup contains only safe verification facts, suitable for an upgrade
// journal. DatabaseSHA256 identifies the file bytes; BusinessSHA256 identifies
// every typed SQLite value in the consistent snapshot, including WAL content.
type RunnerBackup struct {
	SchemaVersion  int              `json:"schema_version"`
	SchemaName     string           `json:"schema_name"`
	SchemaSHA256   string           `json:"schema_sha256"`
	DatabaseSHA256 string           `json:"database_sha256"`
	DatabaseBytes  int64            `json:"database_bytes"`
	BusinessSHA256 string           `json:"business_sha256"`
	Rows           RunnerBackupRows `json:"rows"`
}

// RunnerBackupRows counts current business records. TotalRows also includes
// relation tables, the schema marker, and SQLite's AUTOINCREMENT high water marks.
type RunnerBackupRows struct {
	TotalRows          int64 `json:"total_rows"`
	Jobs               int64 `json:"jobs"`
	JobEvents          int64 `json:"job_events"`
	Runs               int64 `json:"runs"`
	RunRevisions       int64 `json:"run_revisions"`
	CaseResults        int64 `json:"case_results"`
	Evidence           int64 `json:"evidence"`
	Reports            int64 `json:"reports"`
	Comparisons        int64 `json:"comparisons"`
	PerformanceReports int64 `json:"performance_reports"`
	Submissions        int64 `json:"submissions"`
}

// BackupRunnerDatabase explicitly snapshots a current database. The caller
// stops admission and the service before invoking it; this helper never does so.
// It never migrates, checkpoints, or writes the source. An existing destination
// or SQLite sidecar is a conflict. The caller must own a private destination
// directory so another process cannot replace files during SQLite's path open.
func BackupRunnerDatabase(ctx context.Context, sourcePath, destinationPath string) (RunnerBackup, error) {
	source, err := openRunnerBackupSource(ctx, sourcePath)
	if err != nil {
		return RunnerBackup{}, err
	}
	defer source.close()
	before, err := runnerSnapshotFacts(ctx, source.conn)
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "validate source", err)
	}
	destination, err := filepath.Abs(destinationPath)
	if err != nil || strings.TrimSpace(destinationPath) == "" {
		return RunnerBackup{}, errors.New("runner backup destination path is required")
	}
	if destination == source.path {
		return RunnerBackup{}, ErrConflict
	}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(destination + suffix); err == nil {
			return RunnerBackup{}, ErrConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return RunnerBackup{}, runnerBackupError(ctx, "inspect destination namespace", err)
		}
	}
	file, err := os.OpenFile(destination, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return RunnerBackup{}, ErrConflict
	}
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "create exclusive destination", err)
	}
	created, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return RunnerBackup{}, runnerBackupError(ctx, "inspect destination", err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			// Only remove the new file we exclusively created, never a replacement.
			if current, statErr := os.Lstat(destination); statErr == nil && os.SameFile(created, current) {
				_ = os.Remove(destination)
			}
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "set destination permissions", err)
	}
	if err := copyRunnerSnapshot(ctx, source.conn, destination); err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "copy snapshot", err)
	}
	if err := file.Sync(); err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "sync destination", err)
	}
	current, err := os.Lstat(destination)
	validDestination := err == nil && current.Mode().IsRegular() && os.SameFile(created, current)
	if !validDestination {
		return RunnerBackup{}, errors.New("runner backup destination identity changed")
	}
	after, err := runnerSnapshotFacts(ctx, source.conn)
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "revalidate source snapshot", err)
	}
	if !sameRunnerBusiness(before, after) {
		return RunnerBackup{}, fmt.Errorf("%w: runner source snapshot changed", ErrConflict)
	}
	backup, err := VerifyRunnerDatabase(ctx, destination)
	if err != nil {
		return RunnerBackup{}, err
	}
	if !sameRunnerBusiness(before, backup) {
		return RunnerBackup{}, fmt.Errorf("%w: runner backup business facts differ", ErrCorrupt)
	}
	if err := source.verifyIdentity(); err != nil {
		return RunnerBackup{}, err
	}
	complete = true
	return backup, nil
}

// VerifyRunnerDatabase validates the single current schema and all stored
// runner facts through a read-only transaction. No schema is created or upgraded.
// For an arbitrary WAL database, DatabaseSHA256 is the main file's hash; only
// BusinessSHA256 includes the transaction's WAL-visible values.
func VerifyRunnerDatabase(ctx context.Context, path string) (RunnerBackup, error) {
	source, err := openRunnerBackupSource(ctx, path)
	if err != nil {
		return RunnerBackup{}, err
	}
	defer source.close()
	fileSHA, fileBytes, err := runnerBackupFileHash(ctx, source.file)
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "hash database", err)
	}
	facts, err := runnerSnapshotFacts(ctx, source.conn)
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "verify database", err)
	}
	afterSHA, afterBytes, err := runnerBackupFileHash(ctx, source.file)
	if err != nil {
		return RunnerBackup{}, runnerBackupError(ctx, "recheck database hash", err)
	}
	if fileSHA != afterSHA || fileBytes != afterBytes {
		return RunnerBackup{}, fmt.Errorf("%w: runner database file changed during verification", ErrConflict)
	}
	if err := source.verifyIdentity(); err != nil {
		return RunnerBackup{}, err
	}
	facts.DatabaseSHA256, facts.DatabaseBytes = fileSHA, fileBytes
	return facts, nil
}

type runnerBackupSource struct {
	db   *sql.DB
	conn *sql.Conn
	file *os.File
	info os.FileInfo
	path string
}

func openRunnerBackupSource(ctx context.Context, path string) (*runnerBackupSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("runner database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, runnerBackupError(ctx, "resolve source path", err)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, runnerBackupError(ctx, "inspect source", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("runner database must be a regular file without a symbolic link")
	}
	file, err := os.Open(abs)
	if err != nil {
		return nil, runnerBackupError(ctx, "open source file", err)
	}
	source := &runnerBackupSource{file: file, info: info, path: abs}
	if err := source.verifyIdentity(); err != nil {
		source.close()
		return nil, err
	}
	db, err := sql.Open("sqlite", runnerBackupURI(abs, "ro"))
	if err != nil {
		source.close()
		return nil, runnerBackupError(ctx, "open read-only source", err)
	}
	source.db = db
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		source.close()
		return nil, runnerBackupError(ctx, "acquire read-only source", err)
	}
	source.conn = conn
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		source.close()
		return nil, runnerBackupError(ctx, "enable query-only source", err)
	}
	var queryOnly int
	if err := conn.QueryRowContext(ctx, "PRAGMA query_only").Scan(&queryOnly); err != nil || queryOnly != 1 {
		source.close()
		return nil, errors.New("runner source query-only setting could not be verified")
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		source.close()
		return nil, runnerBackupError(ctx, "begin source snapshot", err)
	}
	return source, nil
}

func (source *runnerBackupSource) close() {
	if source.conn != nil {
		_, _ = source.conn.ExecContext(context.Background(), "ROLLBACK")
		_ = source.conn.Close()
	}
	if source.db != nil {
		_ = source.db.Close()
	}
	if source.file != nil {
		_ = source.file.Close()
	}
}

func (source *runnerBackupSource) verifyIdentity() error {
	opened, err := source.file.Stat()
	if err != nil {
		return errors.New("runner source file identity could not be verified")
	}
	current, err := os.Lstat(source.path)
	sameFile := err == nil && current.Mode().IsRegular() && os.SameFile(source.info, current)
	if !sameFile || !os.SameFile(source.info, opened) {
		return fmt.Errorf("%w: runner source file identity changed", ErrConflict)
	}
	return nil
}

func runnerBackupURI(path, mode string) string {
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	value := &url.URL{Scheme: "file", Path: uriPath}
	query := url.Values{"mode": []string{mode}}
	if mode == "ro" {
		query["_pragma"] = []string{"query_only(1)", "busy_timeout(5000)"}
	}
	value.RawQuery = query.Encode()
	return value.String()
}

func copyRunnerSnapshot(ctx context.Context, conn *sql.Conn, destination string) error {
	return conn.Raw(func(raw any) error {
		creator, ok := raw.(interface {
			NewBackup(string) (*sqlitedriver.Backup, error)
		})
		if !ok {
			return errors.New("SQLite online backup is unavailable")
		}
		backup, err := creator.NewBackup(runnerBackupURI(destination, "rw"))
		if err != nil {
			return err
		}
		finished := false
		defer func() {
			if !finished {
				_ = backup.Finish()
			}
		}()
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			more, err := backup.Step(128)
			if err != nil {
				return err
			}
			if !more {
				break
			}
		}
		finished = true
		return backup.Finish()
	})
}

func runnerSnapshotFacts(ctx context.Context, conn *sql.Conn) (RunnerBackup, error) {
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return RunnerBackup{}, err
	}
	if version != CurrentSchemaVersion {
		return RunnerBackup{}, ErrSchemaResetRequired
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return RunnerBackup{}, err
	}
	facts := RunnerBackup{}
	facts.SchemaVersion, facts.SchemaName, facts.SchemaSHA256 = CurrentSchemaIdentity()
	digest := sha256.New()
	for _, object := range currentSchemaObjects {
		if object.kind != "table" {
			continue
		}
		count, err := hashRunnerTable(ctx, conn, object.name, digest)
		if err != nil {
			return RunnerBackup{}, err
		}
		facts.Rows.add(object.name, count)
	}
	count, err := hashRunnerTable(ctx, conn, "sqlite_sequence", digest)
	if err != nil {
		return RunnerBackup{}, err
	}
	facts.Rows.TotalRows += count
	if err := validateRunnerBackupRelations(ctx, conn); err != nil {
		return RunnerBackup{}, err
	}
	facts.BusinessSHA256 = hex.EncodeToString(digest.Sum(nil))
	return facts, nil
}

func (counts *RunnerBackupRows) add(table string, count int64) {
	counts.TotalRows += count
	switch table {
	case "execution_jobs":
		counts.Jobs = count
	case "execution_job_events":
		counts.JobEvents = count
	case "execution_runs":
		counts.Runs = count
	case "execution_run_revisions":
		counts.RunRevisions = count
	case "case_results":
		counts.CaseResults = count
	case "evidence":
		counts.Evidence = count
	case "reports":
		counts.Reports = count
	case "comparisons":
		counts.Comparisons = count
	case "quick_performance_reports":
		counts.PerformanceReports = count
	case "job_submissions":
		counts.Submissions = count
	}
}

// Table and column identifiers originate only in the already matched current
// schema. Values are length framed and tagged so NULL, text, blob and numeric
// values cannot collide, including embedded separators or zero bytes.
func hashRunnerTable(ctx context.Context, conn *sql.Conn, table string, digest hash.Hash) (int64, error) {
	metadata, err := conn.QueryContext(ctx, `SELECT * FROM "`+table+`" LIMIT 0`)
	if err != nil {
		return 0, err
	}
	columns, err := metadata.Columns()
	closeErr := metadata.Close()
	if err != nil {
		return 0, err
	}
	if closeErr != nil {
		return 0, closeErr
	}
	order := make([]string, len(columns))
	for index := range columns {
		order[index] = strconv.Itoa(index + 1)
	}
	rows, err := conn.QueryContext(ctx, `SELECT * FROM "`+table+`" ORDER BY `+strings.Join(order, ","))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	writeRunnerDigest(digest, 'T', []byte(table))
	for _, column := range columns {
		writeRunnerDigest(digest, 'C', []byte(column))
	}
	var count int64
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return 0, err
		}
		record := make(map[string]any, len(columns))
		writeRunnerDigest(digest, 'R', nil)
		for index, value := range values {
			record[columns[index]] = value
			if err := hashRunnerValue(digest, value); err != nil {
				return 0, err
			}
		}
		if err := validateRunnerBackupDocument(table, record); err != nil {
			return 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	writeRunnerDigest(digest, 'E', []byte(strconv.FormatInt(count, 10)))
	return count, nil
}

func writeRunnerDigest(digest hash.Hash, kind byte, value []byte) {
	var prefix [9]byte
	prefix[0] = kind
	binary.BigEndian.PutUint64(prefix[1:], uint64(len(value)))
	_, _ = digest.Write(prefix[:])
	_, _ = digest.Write(value)
}

func hashRunnerValue(digest hash.Hash, value any) error {
	var encoded [8]byte
	switch typed := value.(type) {
	case nil:
		writeRunnerDigest(digest, 'N', nil)
	case string:
		writeRunnerDigest(digest, 'S', []byte(typed))
	case []byte:
		writeRunnerDigest(digest, 'B', typed)
	case int64:
		binary.BigEndian.PutUint64(encoded[:], uint64(typed))
		writeRunnerDigest(digest, 'I', encoded[:])
	case float64:
		binary.BigEndian.PutUint64(encoded[:], math.Float64bits(typed))
		writeRunnerDigest(digest, 'F', encoded[:])
	default:
		return ErrCorrupt
	}
	return nil
}

type runnerBackupRecord map[string]any

func (row runnerBackupRecord) text(name string) string {
	switch value := row[name].(type) {
	case string:
		return value
	case []byte:
		return string(value)
	default:
		return ""
	}
}

func (row runnerBackupRecord) integer(name string) int64 {
	value, _ := row[name].(int64)
	return value
}

func (row runnerBackupRecord) nullable(name string) sql.NullString {
	value, valid := row[name].(string)
	return sql.NullString{String: value, Valid: valid}
}

func validateRunnerBackupDocument(table string, row runnerBackupRecord) error {
	document := []byte(row.text("document_json"))
	switch table {
	case "local_executor_identity":
		key, ok := row["commitment_key"].([]byte)
		if !ok || len(key) != 32 || !domain.IsUUID(row.text("node_id")) || !domain.IsUUID(row.text("installation_id")) {
			return ErrCorrupt
		}
	case "execution_run_revisions":
		run, err := decodeRunDocument(document)
		if err != nil {
			return err
		}
		meta := run.Meta()
		validMeta := verifyEntityRow(
			document,
			row.text("run_id"),
			row.integer("schema_version"),
			row.integer("revision"),
			row.text("created_at"),
			row.text("updated_at"),
		) == nil
		validPlan := run.PlanID() == row.text("plan_id") &&
			int64(run.Snapshot().Plan.Revision) == row.integer("plan_revision")
		snapshot, err := marshalCanonical(run.Snapshot())
		validSnapshot := err == nil && string(snapshot) == row.text("snapshot_json")
		validIdentity := string(run.Status()) == row.text("status") && meta.ID == row.text("run_id")
		validColumns := validMeta && validPlan && validSnapshot && validIdentity
		if !validColumns {
			return ErrCorrupt
		}
	case "case_results":
		stored := storedResultRow{
			id: row.text("id"), schemaVersion: row.integer("schema_version"), revision: row.integer("revision"),
			createdAt: row.text("created_at"), updatedAt: row.text("updated_at"), runID: row.text("run_id"),
			entryID: row.nullable("entry_id"), caseID: row.nullable("case_id"), requestID: row.nullable("request_id"),
			document: document,
		}
		_, err := stored.decode("")
		return err
	case "evidence":
		stored := storedEvidenceRow{
			id: row.text("id"), schemaVersion: row.integer("schema_version"), revision: row.integer("revision"),
			createdAt: row.text("created_at"), updatedAt: row.text("updated_at"), runID: row.text("run_id"),
			document: document,
		}
		_, err := stored.decode("")
		return err
	case "reports":
		stored := storedReportRow{
			id: row.text("id"), schemaVersion: row.integer("schema_version"), runID: row.text("run_id"),
			generatedAt: row.text("generated_at"), document: document,
		}
		_, err := stored.decode()
		return err
	case "artifacts":
		var attachment domain.ReportAttachment
		validate := func() error { return attachment.Validate(row.text("run_id")) }
		if err := decodeCanonical(document, &attachment, validate); err != nil {
			return err
		}
		validIdentity := attachment.ArtifactID == row.text("id") && attachment.Name == row.text("name")
		validLocation := attachment.RelativePath == row.text("relative_path") && attachment.SHA256 == row.text("sha256")
		validType := attachment.MediaType == row.text("media_type") &&
			boolInt(attachment.Redacted) == int(row.integer("redacted"))
		validColumns := validIdentity && validLocation && validType
		if !validColumns {
			return ErrCorrupt
		}
	case "comparison_revisions":
		var comparison domain.Comparison
		if err := decodeCanonical(document, &comparison, nil); err != nil {
			return err
		}
		if err := verifyEntityRow(
			document,
			row.text("comparison_id"),
			row.integer("schema_version"),
			row.integer("revision"),
			row.text("created_at"),
			row.text("updated_at"),
		); err != nil {
			return err
		}
		plan, model := comparison.Plan(), comparison.Model()
		validPlan := plan.ID == row.text("plan_id") && int64(plan.Revision) == row.integer("plan_revision")
		validModel := model.ID == row.text("model_id") && int64(model.Revision) == row.integer("model_revision")
		validColumns := validPlan && validModel && string(comparison.Status()) == row.text("status")
		if !validColumns {
			return ErrCorrupt
		}
	case "quick_performance_reports":
		report, at, err := decodeQuickPerformanceReport(document)
		if err != nil {
			return err
		}
		validTime := report.GeneratedAt == row.text("generated_at") && at.UnixNano() == row.integer("generated_at_unix_nano")
		validSummary := report.ReportID == row.text("id") && report.ModelID == row.text("model_id") &&
			report.BaseURL == row.text("base_url") && string(report.Progress.Phase) == row.text("phase")
		validCounts := int64(report.Metrics.Completed) == row.integer("completed") &&
			int64(report.Metrics.Failed) == row.integer("failed")
		validColumns := validTime && validSummary && validCounts && report.Success == (row.integer("success") == 1)
		if !validColumns {
			return ErrCorrupt
		}
	}
	return nil
}

func validateRunnerBackupRelations(ctx context.Context, conn *sql.Conn) error {
	checks := []struct {
		table, column string
		visit         func(string) error
	}{
		{table: "execution_runs", column: "id", visit: func(id string) error {
			_, err := queryCurrentRun(ctx, conn, id)
			return err
		}},
		{table: "comparisons", column: "id", visit: func(id string) error {
			_, err := queryCurrentComparison(ctx, conn, id)
			return err
		}},
		{table: "job_submissions", column: "job_id", visit: func(id string) error {
			_, err := querySubmission(ctx, conn, id)
			return err
		}},
		{table: "execution_job_commands", column: "id", visit: func(id string) error {
			_, err := queryJobCommand(ctx, conn, id)
			return err
		}},
		{table: "execution_jobs", column: "id", visit: func(id string) error {
			return validateRunnerBackupJob(ctx, conn, id)
		}},
		{table: "reports", column: "id", visit: func(id string) error {
			var row storedReportRow
			if err := row.scan(conn.QueryRowContext(ctx, `SELECT id, schema_version, run_id, generated_at, document_json
				FROM reports WHERE id = ?`, id)); err != nil {
				return err
			}
			report, err := row.decode()
			if err != nil {
				return err
			}
			return validateReportStorage(ctx, conn, report)
		}},
		{table: "execution_performance_observations", column: "job_id", visit: func(id string) error {
			spec, snapshot, err := queryPerformanceOwner(ctx, conn, id)
			if err != nil {
				return err
			}
			_, err = queryPerformanceObservation(
				ctx,
				conn,
				spec,
				snapshot.Receipt.AcceptedAt,
			)
			return err
		}},
		{table: "execution_performance_results", column: "job_id", visit: func(id string) error {
			spec, snapshot, err := queryPerformanceOwner(ctx, conn, id)
			if err != nil {
				return err
			}
			_, err = queryPerformanceResult(
				ctx,
				conn,
				spec,
				snapshot,
			)
			return err
		}},
	}
	for _, check := range checks {
		if err := runnerBackupEachID(
			ctx,
			conn,
			check.table,
			check.column,
			check.visit,
		); err != nil {
			return err
		}
	}
	return validateRunnerBackupCaseResults(ctx, conn)
}

func validateRunnerBackupCaseResults(ctx context.Context, queryer relationQueryer) error {
	type resultIdentity struct{ runID, id string }

	var afterRunID, afterID string
	var currentRunID string
	var snapshot domain.RunSnapshot
	started := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, err := queryer.QueryContext(
			ctx,
			`SELECT run_id, id FROM case_results
			WHERE (? = 0 OR run_id > ? OR (run_id = ? AND id > ?))
			ORDER BY run_id, id LIMIT 128`,
			started,
			afterRunID,
			afterRunID,
			afterID,
		)
		if err != nil {
			return err
		}
		page := []resultIdentity{}
		for rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			var runID, id string
			if err := rows.Scan(&runID, &id); err != nil {
				_ = rows.Close()
				return err
			}
			page = append(page, resultIdentity{runID: runID, id: id})
		}
		iterationErr := rows.Err()
		closeErr := rows.Close()
		if iterationErr != nil {
			return iterationErr
		}
		if closeErr != nil {
			return closeErr
		}
		// The transaction freezes each owner. Keep only its snapshot across pages;
		// evidence reuse ends at each bounded page or owner change.
		evidenceCache := make(map[string]domain.Evidence)
		for _, identity := range page {
			if err := ctx.Err(); err != nil {
				return err
			}
			var row storedResultRow
			if err := row.scan(queryer.QueryRowContext(ctx, `
				SELECT id, schema_version, revision, created_at, updated_at,
				       run_id, entry_id, case_id, request_id, document_json FROM case_results WHERE id = ?
			`, identity.id)); err != nil {
				return err
			}
			if row.id != identity.id || row.runID != identity.runID {
				return fmt.Errorf("%w: result page identity", ErrCorrupt)
			}
			result, err := row.decode("")
			if err != nil {
				return err
			}
			if result.RunID != currentRunID {
				run, err := queryCurrentRun(ctx, queryer, result.RunID)
				if err != nil {
					if contextErr := ctx.Err(); contextErr != nil {
						return contextErr
					}
					return fmt.Errorf("%w: result owner", ErrCorrupt)
				}
				snapshot = run.Snapshot()
				currentRunID = result.RunID
				evidenceCache = make(map[string]domain.Evidence)
			}
			if err := validateStoredResultAgainstSnapshot(
				ctx,
				queryer,
				result,
				snapshot,
				evidenceCache,
			); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			afterRunID, afterID = row.runID, row.id
		}
		started = true
		if len(page) < 128 {
			return ctx.Err()
		}
	}
}

func runnerBackupEachID(
	ctx context.Context,
	conn *sql.Conn,
	table, column string,
	visit func(string) error,
) error {
	var after string
	started := false
	for {
		rows, err := conn.QueryContext(
			ctx,
			`SELECT "`+column+`" FROM "`+table+`"
			WHERE (? = 0 OR "`+column+`" > ?) ORDER BY "`+column+`" LIMIT 128`,
			started,
			after,
		)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		iterationErr := rows.Err()
		closeErr := rows.Close()
		if iterationErr != nil {
			return iterationErr
		}
		if closeErr != nil {
			return closeErr
		}
		for _, id := range ids {
			if !domain.IsUUID(id) {
				return ErrCorrupt
			}
			if err := visit(id); err != nil {
				return err
			}
			after = id
		}
		started = true
		if len(ids) < 128 {
			return nil
		}
	}
}

func validateRunnerBackupJob(ctx context.Context, conn *sql.Conn, id string) error {
	stored, err := queryJobRow(ctx, conn, id)
	if err != nil {
		return err
	}
	snapshot, err := stored.decode()
	if err != nil {
		return err
	}
	spec, err := queryJobSpec(ctx, conn, id)
	if err != nil {
		return err
	}
	if err := snapshot.Validate(spec); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx, `SELECT sequence, kind, created_at, document_json
		FROM execution_job_events WHERE job_id = ? ORDER BY sequence`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	var sequence uint64
	reports := []jobcontract.ReportReference{}
	results := []jobcontract.PerformanceResultReference{}
	for rows.Next() {
		var index int64
		var kind, at string
		var document []byte
		if err := rows.Scan(&index, &kind, &at, &document); err != nil {
			return err
		}
		if len(document) > jobcontract.MaxEventBytes {
			return ErrCorrupt
		}
		event, err := decodeStoredJobEvent(document)
		if err != nil {
			return err
		}
		validSequence := index > 0 && event.Sequence == sequence+1 && event.Sequence == uint64(index)
		validIdentity := event.JobID == id && string(event.Kind) == kind && formatTime(event.CreatedAt) == at
		if !validSequence || !validIdentity || event.Sequence > snapshot.LatestSequence {
			return ErrCorrupt
		}
		if err := validateEventExecutionOrigin(event, snapshot.Receipt.Origin); err != nil {
			return err
		}
		switch event.Kind {
		case jobcontract.EventReport:
			var reference jobcontract.ReportReference
			if err := json.Unmarshal(event.Payload, &reference); err != nil {
				return ErrCorrupt
			}
			reports = append(reports, reference)
		case jobcontract.EventPerformanceResult:
			var reference jobcontract.PerformanceResultReference
			if err := json.Unmarshal(event.Payload, &reference); err != nil {
				return ErrCorrupt
			}
			results = append(results, reference)
		}
		sequence = event.Sequence
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if sequence != snapshot.LatestSequence {
		return ErrCorrupt
	}
	if err := rows.Close(); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, reference := range reports {
		if seen[reference.ID] {
			return ErrCorrupt
		}
		seen[reference.ID] = true
		var document []byte
		var runID string
		if reference.Kind == jobcontract.KindPerformance {
			if spec.Spec.Performance == nil || reference.ID != spec.Spec.Performance.ReportID {
				return ErrCorrupt
			}
			err = conn.QueryRowContext(
				ctx, `SELECT document_json FROM quick_performance_reports WHERE id = ?`, reference.ID,
			).Scan(&document)
		} else {
			err = conn.QueryRowContext(
				ctx,
				`SELECT report.run_id, report.document_json FROM reports report
				JOIN execution_job_runs owned ON owned.run_id = report.run_id
				WHERE owned.job_id = ? AND report.id = ?`,
				id,
				reference.ID,
			).Scan(&runID, &document)
		}
		if err != nil || reportReference(reference.ID, reference.Kind, runID, document) != reference {
			return ErrCorrupt
		}
	}
	if len(results) > 1 {
		return ErrCorrupt
	}
	for _, reference := range results {
		if spec.Kind != jobcontract.KindPerformance {
			return ErrCorrupt
		}
		sealed, err := queryPerformanceResult(
			ctx,
			conn,
			spec,
			snapshot,
		)
		if err != nil || sealed.Reference != reference {
			return ErrCorrupt
		}
	}
	owned, err := conn.QueryContext(ctx, `SELECT run_id, position FROM execution_job_runs
		WHERE job_id = ? ORDER BY position`, id)
	if err != nil {
		return err
	}
	defer owned.Close()
	position := 0
	for owned.Next() {
		var runID string
		var ordinal int
		if err := owned.Scan(&runID, &ordinal); err != nil {
			return err
		}
		if position >= len(snapshot.Receipt.RunIDs) || ordinal != position || runID != snapshot.Receipt.RunIDs[position] {
			return ErrCorrupt
		}
		position++
	}
	if err := owned.Err(); err != nil {
		return err
	}
	if position != len(snapshot.Receipt.RunIDs) {
		return ErrCorrupt
	}
	return owned.Close()
}

func sameRunnerBusiness(left, right RunnerBackup) bool {
	sameSchema := left.SchemaVersion == right.SchemaVersion && left.SchemaName == right.SchemaName &&
		left.SchemaSHA256 == right.SchemaSHA256
	sameRows := left.BusinessSHA256 == right.BusinessSHA256 && left.Rows == right.Rows
	return sameSchema && sameRows
}

func runnerBackupFileHash(ctx context.Context, file *os.File) (string, int64, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}
	digest := sha256.New()
	buffer := make([]byte, 64*1024)
	var size int64
	for {
		if err := ctx.Err(); err != nil {
			return "", 0, err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			_, _ = digest.Write(buffer[:n])
			size += int64(n)
		}
		if errors.Is(err, io.EOF) {
			return hex.EncodeToString(digest.Sum(nil)), size, nil
		}
		if err != nil {
			return "", 0, err
		}
	}
}

// Stored documents and SQLite errors may contain request material. Export only
// a fixed stage and classification, never an underlying database error body.
func runnerBackupError(ctx context.Context, stage string, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if errors.Is(cause, ErrSchemaResetRequired) {
		return fmt.Errorf("%w: runner database %s", ErrSchemaResetRequired, stage)
	}
	if errors.Is(cause, os.ErrNotExist) {
		return fmt.Errorf("runner database %s: %w", stage, os.ErrNotExist)
	}
	return fmt.Errorf(
		"%w: runner database %s; preserve this database and explicitly repair or upgrade it", ErrCorrupt, stage,
	)
}
