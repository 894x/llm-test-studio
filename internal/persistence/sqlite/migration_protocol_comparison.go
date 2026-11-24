package sqlite

import (
	"context"
	"database/sql"
	"slices"
)

var protocolComparisonRuns = schemaObject{
	kind: "table", name: "comparison_runs", table: "comparison_runs",
	ddl: `CREATE TABLE comparison_runs (
		comparison_id TEXT NOT NULL,
		comparison_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		channel_id TEXT NOT NULL,
		channel_revision INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		protocol TEXT NOT NULL DEFAULT '' CHECK(protocol IN ('', 'openai-chat', 'openai-responses', 'anthropic-messages')),
		PRIMARY KEY(comparison_id, comparison_revision, position),
		UNIQUE(comparison_id, comparison_revision, protocol, channel_id),
		UNIQUE(comparison_id, comparison_revision, run_id),
		FOREIGN KEY(comparison_id, comparison_revision)
			REFERENCES comparison_revisions(comparison_id, revision) ON DELETE CASCADE,
		FOREIGN KEY(run_id) REFERENCES execution_runs(id)
	)`,
}

var protocolComparisonIndexes = []schemaObject{
	{kind: "index", name: "idx_comparison_runs_protocol", table: "comparison_runs",
		ddl: `CREATE UNIQUE INDEX idx_comparison_runs_protocol
			ON comparison_runs(comparison_id, comparison_revision, protocol) WHERE protocol <> ''`},
	{kind: "index", name: "idx_comparison_runs_protocol_child", table: "comparison_runs",
		ddl: `CREATE INDEX idx_comparison_runs_protocol_child ON comparison_runs(run_id) WHERE protocol <> ''`},
}

var currentSchemaObjects = protocolComparisonSchema()

func protocolComparisonSchema() []schemaObject {
	objects := slices.Clone(schemaV4Objects)
	for index, object := range objects {
		if object.name == protocolComparisonRuns.name {
			objects[index] = protocolComparisonRuns
			break
		}
	}
	return append(objects, protocolComparisonIndexes...)
}

func upgradeProtocolComparison(ctx context.Context, conn *sql.Conn, path string, options MigrateOptions, backup bool) error {
	if backup {
		options.progress("backing_up", 4)
		if _, err := backupMigrationSource(ctx, path, historicalBaseline{objects: schemaV4Objects}); err != nil {
			return err
		}
	}
	options.progress("migrating", 4)
	columns, err := migrationColumns(ctx, conn, protocolComparisonRuns.name)
	if err != nil {
		return err
	}
	previous := migrationTableFacts{table: protocolComparisonRuns.name, columns: columns}
	previous.digest, err = migrationTableDigest(ctx, conn, previous)
	if err != nil {
		return err
	}
	// The default value preserves the channel axis while copying all original columns.
	if err := rebuildOperationalTable(ctx, conn, protocolComparisonRuns); err != nil {
		return err
	}
	for _, object := range protocolComparisonIndexes {
		if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
			return err
		}
	}
	if err := verifyMigrationFacts(ctx, conn, []migrationTableFacts{previous}); err != nil {
		return err
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return err
	}
	return recordCurrentMigration(ctx, conn, options.AppVersion)
}
