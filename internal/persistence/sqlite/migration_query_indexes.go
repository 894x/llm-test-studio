package sqlite

import (
	"context"
	"database/sql"
)

const schemaV2Name = "0002_operational_upgrades"

func schemaV2Checksum() string { return schemaChecksum(schemaV2Name, schemaV2Objects) }

// Version 3 is additive. These expressions and definitions are immutable once
// released; a future query/index change must append another migration.
var queryIndexObjects = []schemaObject{
	{kind: "index", name: "idx_reports_history", table: "reports", ddl: `CREATE INDEX idx_reports_history ON reports((` + queryIndexReportTimeKeyV3 + `) DESC, id DESC)`},
	{kind: "index", name: "idx_runs_history", table: "execution_runs", ddl: `CREATE INDEX idx_runs_history ON execution_runs((` + queryIndexTimeKeyV3("created_at") + `) DESC, id DESC)`},
	{kind: "index", name: "idx_quick_run_revisions", table: "execution_run_revisions", ddl: `CREATE INDEX idx_quick_run_revisions ON execution_run_revisions(run_id, revision) WHERE json_type(snapshot_json,'$.quick_task')='object'`},
	{kind: "index", name: "idx_execution_events_kind_sequence", table: "execution_job_events", ddl: `CREATE INDEX idx_execution_events_kind_sequence ON execution_job_events(job_id, kind, sequence DESC)`},
	{kind: "index", name: "idx_remote_events_kind_sequence", table: "remote_job_events", ddl: `CREATE INDEX idx_remote_events_kind_sequence ON remote_job_events(job_id, kind, sequence DESC)`},
	{kind: "index", name: "idx_comparisons_history", table: "comparisons", ddl: `CREATE INDEX idx_comparisons_history ON comparisons((` + queryIndexTimeKeyV3("created_at") + `) DESC, id DESC)`},
}

var currentSchemaObjects = append(append([]schemaObject{}, schemaV2Objects...), queryIndexObjects...)

func recordV2Migration(ctx context.Context, conn *sql.Conn, appVersion string) error {
	if err := recordMigration(ctx, conn, migrationRecord{
		version: 2, name: schemaV2Name, checksum: schemaV2Checksum(),
	}, appVersion); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, "PRAGMA user_version = 2")
	return err
}

func upgradeQueryIndexes(ctx context.Context, conn *sql.Conn, path string, options MigrateOptions, backup bool) error {
	if err := validateIntegrity(ctx, conn); err != nil {
		return err
	}
	facts, err := migrationFacts(ctx, conn, schemaV2Objects, false)
	if err != nil {
		return err
	}
	if backup {
		options.progress("backing_up", 2)
		if _, err := backupMigrationSource(ctx, path, historicalBaseline{objects: schemaV2Objects}); err != nil {
			return err
		}
	}
	options.progress("migrating", 2)
	for _, object := range queryIndexObjects {
		if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
			return err
		}
	}
	if err := verifyMigrationFacts(ctx, conn, facts); err != nil {
		return err
	}
	return recordCurrentMigration(ctx, conn, options.AppVersion)
}

const queryIndexReportTimeKeyV3 = `
	substr(generated_at, 1, 19) ||
	substr(
		CASE WHEN substr(generated_at, 20, 1) = '.'
			THEN substr(generated_at, 21, length(generated_at) - 21)
			ELSE ''
		END || '000000000',
		1, 9
	)
`

func queryIndexTimeKeyV3(column string) string {
	return "substr(" + column + ",1,19) || '.' || substr(CASE WHEN substr(" + column +
		",20,1)='.' THEN substr(" + column + ",21,length(" + column +
		")-21) ELSE '' END || '000000000',1,9) || 'Z'"
}
