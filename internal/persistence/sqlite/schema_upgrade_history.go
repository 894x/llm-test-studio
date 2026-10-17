package sqlite

// Upgrade archives retain original SQL value types and document bytes. They are
// read-only history, never inputs to the current admission or recovery engine.
var upgradeHistorySchemaObjects = []schemaObject{
	{kind: "table", name: "upgrade_history_tables", table: "upgrade_history_tables", ddl: `CREATE TABLE upgrade_history_tables (
		table_name TEXT PRIMARY KEY NOT NULL,
		source_checksum TEXT NOT NULL,
		columns_json TEXT NOT NULL CHECK(json_valid(columns_json)),
		row_count INTEGER NOT NULL CHECK(row_count >= 0),
		sha256 TEXT NOT NULL
	)`},
	{kind: "table", name: "upgrade_history_rows", table: "upgrade_history_rows", ddl: `CREATE TABLE upgrade_history_rows (
		table_name TEXT NOT NULL REFERENCES upgrade_history_tables(table_name),
		position INTEGER NOT NULL CHECK(position > 0),
		values_json TEXT NOT NULL CHECK(json_valid(values_json)),
		PRIMARY KEY(table_name, position)
	)`},
	{kind: "table", name: "upgrade_history_jobs", table: "upgrade_history_jobs", ddl: `CREATE TABLE upgrade_history_jobs (
		job_id TEXT PRIMARY KEY NOT NULL,
		comparison_id TEXT,
		performance_report_id TEXT
	)`},
	{kind: "table", name: "upgrade_history_runs", table: "upgrade_history_runs", ddl: `CREATE TABLE upgrade_history_runs (
		run_id TEXT PRIMARY KEY NOT NULL REFERENCES execution_runs(id),
		job_id TEXT NOT NULL REFERENCES upgrade_history_jobs(job_id),
		position INTEGER NOT NULL CHECK(position >= 0),
		UNIQUE(job_id, position)
	)`},
}
