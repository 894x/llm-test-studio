package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/application/jobs"
)

func upgradeOperationalDatabase(
	ctx context.Context,
	conn *sql.Conn,
	path string,
	baseline historicalBaseline,
	options MigrateOptions,
) error {
	if err := validateIntegrity(ctx, conn); err != nil {
		return runnerBackupError(ctx, "validate upgrade source integrity", err)
	}
	facts, err := migrationFacts(ctx, conn, baseline.objects, true)
	if err != nil {
		return err
	}
	if baseline.lacksExecutionOrigin() {
		for i := range facts {
			if historicalJobTable(facts[i].table) {
				facts[i].stripTarget = false
				facts[i].digest, err = migrationTableDigest(ctx, conn, facts[i])
				if err != nil {
					return err
				}
				facts[i].archived = true
			}
		}
	}
	backup, err := backupMigrationSource(ctx, path, baseline)
	if err != nil {
		return err
	}
	if err := applyOperationalUpgrade(ctx, conn, baseline); err != nil {
		return fmt.Errorf("sqlite upgrade stopped; original backup at %s: %w", backup, err)
	}
	if err := verifyMigrationFacts(ctx, conn, facts); err != nil {
		return err
	}
	if err := recordCurrentMigration(ctx, conn, options.AppVersion); err != nil {
		return err
	}
	// A structurally upgraded database must still be readable by current typed
	// repositories. Unsupported JSON fails the transaction instead of committing
	// an apparently successful upgrade that hides the user's history.
	if _, err := runnerSnapshotFacts(ctx, conn); err != nil {
		return fmt.Errorf("sqlite upgrade cannot read stored documents; original backup at %s: %w",
			backup, runnerBackupError(ctx, "validate upgraded documents", err))
	}
	return nil
}

func applyOperationalUpgrade(ctx context.Context, conn *sql.Conn, baseline historicalBaseline) error {
	previous := make(map[string]schemaObject, len(baseline.objects))
	for _, object := range baseline.objects {
		previous[object.name] = object
	}
	rebuilt := map[string]bool{}
	for _, object := range currentSchemaObjects {
		if object.kind == "index" && rebuilt[object.table] {
			continue
		}
		old, exists := previous[object.name]
		if exists && normalizeDDL(old.ddl) == normalizeDDL(object.ddl) {
			continue
		}
		if !exists {
			if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("add migration object %q: %w", object.name, err)
			}
			continue
		}
		// These are the only table replacements in the ten frozen formats.
		switch object.name {
		case "job_control_outbox", "execution_job_runs":
			if err := rebuildOperationalTable(ctx, conn, object); err != nil {
				return err
			}
			rebuilt[object.name] = true
		default:
			return fmt.Errorf("missing explicit migration for object %q", object.name)
		}
	}
	if baseline.lacksExecutionOrigin() {
		if err := archiveHistoricalJobs(ctx, conn, baseline); err != nil {
			return err
		}
	}
	if _, hadTargets := previous["job_control_targets"]; !hadTargets {
		if _, hadControls := previous["job_control_outbox"]; hadControls {
			if err := upgradeControlTargets(ctx, conn); err != nil {
				return err
			}
		}
	}
	if baseline.lacksExecutionOrigin() {
		if err := upgradeReportOrigins(ctx, conn); err != nil {
			return err
		}
	}
	if _, hadContent := previous["execution_job_content"]; !hadContent {
		if _, err := conn.ExecContext(ctx, `INSERT INTO execution_job_content(job_id, inventory_revision)
			SELECT id, 1 FROM execution_jobs`); err != nil {
			return err
		}
	}
	return validateAppliedSchema(ctx, conn)
}

func rebuildOperationalTable(ctx context.Context, conn *sql.Conn, object schemaObject) error {
	columns, err := migrationColumns(ctx, conn, object.name)
	if err != nil {
		return err
	}
	projection := make([]string, 0, len(columns))
	for _, column := range columns {
		projection = append(projection, quoteMigrationName(column))
	}
	names := strings.Join(projection, ", ")
	staging := "_upgrade_" + object.name
	stagingDDL := strings.Replace(object.ddl, "CREATE TABLE "+object.name+" (", "CREATE TABLE "+staging+" (", 1)
	statements := []string{
		stagingDDL,
		"INSERT INTO " + quoteMigrationName(staging) + " (" + names + ") SELECT " + names + " FROM " + quoteMigrationName(object.name),
		"DROP TABLE " + quoteMigrationName(object.name),
		object.ddl,
		"INSERT INTO " + quoteMigrationName(object.name) + " (" + names + ") SELECT " + names + " FROM " + quoteMigrationName(staging),
		"DROP TABLE " + quoteMigrationName(staging),
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("rebuild migration table %q: %w", object.name, err)
		}
	}
	// Dropping a table drops its indexes too, even when their SQL did not change.
	for _, index := range currentSchemaObjects {
		if index.kind == "index" && index.table == object.name {
			if _, err := conn.ExecContext(ctx, index.ddl); err != nil {
				return fmt.Errorf("restore migration index %q: %w", index.name, err)
			}
		}
	}
	return nil
}

func upgradeControlTargets(ctx context.Context, conn *sql.Conn) error {
	var after string
	for {
		var commandID, jobID string
		var raw []byte
		err := conn.QueryRowContext(
			ctx,
			`SELECT command_id, job_id,
			CASE WHEN length(CAST(document_json AS BLOB)) <= ? THEN document_json ELSE NULL END
			FROM job_control_outbox WHERE command_id > ? ORDER BY command_id LIMIT 1`,
			jobs.MaxControlRecordBytes,
			after,
		).Scan(&commandID, &jobID, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return runnerBackupError(ctx, "read historical control", err)
		}
		if err := upgradeControlTarget(ctx, conn, commandID, jobID, raw); err != nil {
			return err
		}
		after = commandID
	}
}

func upgradeControlTarget(ctx context.Context, conn *sql.Conn, commandID, jobID string, raw []byte) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) != 8 || fields["target"] != nil {
		return errors.New("unsupported historical control fields")
	}
	submission, err := querySubmission(ctx, conn, jobID)
	if err != nil {
		return runnerBackupError(ctx, "resolve historical control owner", err)
	}
	read := jobs.JobRead{
		Spec: submission.Prepared.Spec, Submission: &submission,
		Location: jobs.Location{
			Kind: submission.Prepared.Spec.Target.Kind, ConfigurationID: submission.Prepared.ConfigurationID,
		},
	}
	target, err := read.ControlTarget()
	if err != nil {
		return errors.New("historical control destination cannot be preserved")
	}
	targetJSON, err := marshalCanonical(target)
	if err != nil {
		return err
	}
	fields["target"] = targetJSON
	updated, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	record, err := decodeControlRecord(updated)
	if err != nil || record.Command.CommandID != commandID || record.Command.JobID != jobID {
		return errors.New("historical control identity cannot be preserved")
	}
	if err := record.ValidateFor(read); err != nil {
		return errors.New("historical control acceptance cannot be preserved")
	}
	if _, err := conn.ExecContext(
		ctx,
		`INSERT INTO job_control_targets(command_id, job_id, submission_job_id, mirror_job_id, document_json)
		VALUES(?, ?, ?, NULL, ?)`,
		commandID,
		jobID,
		jobID,
		targetJSON,
	); err != nil {
		return fmt.Errorf("record migrated control destination: %w", err)
	}
	_, err = conn.ExecContext(ctx, `UPDATE job_control_outbox SET document_json = ? WHERE command_id = ?`,
		string(updated), commandID)
	return err
}
