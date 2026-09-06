package sqlite

var schemaV1Objects = []schemaObject{
	{kind: "table", name: "schema_migrations", table: "schema_migrations", ddl: `CREATE TABLE schema_migrations (
		version INTEGER PRIMARY KEY,
		name TEXT NOT NULL UNIQUE,
		checksum TEXT NOT NULL,
		applied_at TEXT NOT NULL,
		app_version TEXT NOT NULL
	)`},
	{kind: "table", name: "runs", table: "runs", ddl: `CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT, url TEXT, model TEXT, requests INTEGER,
		duration INTEGER, max_tokens INTEGER, input_tokens INTEGER,
		stream INTEGER, random INTEGER,
		success INTEGER, total INTEGER, ttft_p50 REAL, ttft_p90 REAL,
		tpot_p50 REAL, tpot_p90 REAL, e2e_p50 REAL, e2e_p90 REAL,
		cached INTEGER, prompt_total INTEGER, config TEXT,
		concurrency INTEGER, timeout INTEGER, failures INTEGER,
		elapsed REAL, qps REAL, total_tpm REAL, peak_in_flight INTEGER,
		request_template TEXT, capture_policy TEXT,
		capture_sample_limit INTEGER
	)`},
	{kind: "table", name: "run_results", table: "run_results", ddl: `CREATE TABLE run_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_id INTEGER NOT NULL,
		request_id INTEGER NOT NULL,
		status INTEGER, e2e_ms REAL, ttft_ms REAL, tpot_ms REAL,
		queue_ms REAL, started_at_s REAL, completed_at_s REAL,
		prompt_tokens INTEGER, completion_tokens INTEGER, cached_tokens INTEGER,
		chunks INTEGER, error TEXT, request_body TEXT, response_body TEXT,
		response_text TEXT,
		FOREIGN KEY(run_id) REFERENCES runs(id)
	)`},
	{kind: "index", name: "idx_run_results_run_id", table: "run_results", ddl: `CREATE INDEX idx_run_results_run_id ON run_results(run_id)`},
	{kind: "table", name: "audit_runs", table: "audit_runs", ddl: `CREATE TABLE audit_runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		ts TEXT NOT NULL, suite TEXT NOT NULL, base_url TEXT NOT NULL,
		model TEXT NOT NULL, total INTEGER NOT NULL, overall TEXT NOT NULL,
		verdict TEXT NOT NULL, summary_json TEXT NOT NULL, config_json TEXT NOT NULL,
		report_dir TEXT NOT NULL, report_json TEXT NOT NULL, report_html TEXT NOT NULL
	)`},
	{kind: "table", name: "audit_case_results", table: "audit_case_results", ddl: `CREATE TABLE audit_case_results (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		audit_run_id INTEGER NOT NULL,
		sequence INTEGER NOT NULL,
		case_id TEXT NOT NULL, name TEXT NOT NULL, dimension TEXT NOT NULL,
		protocol TEXT NOT NULL, model TEXT NOT NULL, status TEXT NOT NULL,
		severity TEXT NOT NULL, elapsed_ms INTEGER NOT NULL,
		evidence TEXT NOT NULL, http_status INTEGER NOT NULL,
		metrics_json TEXT NOT NULL, result_json TEXT NOT NULL,
		artifact_dir TEXT NOT NULL,
		FOREIGN KEY(audit_run_id) REFERENCES audit_runs(id) ON DELETE CASCADE,
		UNIQUE(audit_run_id, sequence)
	)`},
	{kind: "index", name: "idx_audit_runs_ts", table: "audit_runs", ddl: `CREATE INDEX idx_audit_runs_ts ON audit_runs(ts DESC)`},
	{kind: "index", name: "idx_audit_case_results_run_id", table: "audit_case_results", ddl: `CREATE INDEX idx_audit_case_results_run_id ON audit_case_results(audit_run_id)`},
	{kind: "table", name: "execution_runs", table: "execution_runs", ddl: `CREATE TABLE execution_runs (
		id TEXT PRIMARY KEY,
		current_revision INTEGER NOT NULL CHECK(current_revision > 0),
		created_at TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1)),
		FOREIGN KEY(id, current_revision)
			REFERENCES execution_run_revisions(run_id, revision)
			DEFERRABLE INITIALLY DEFERRED
	)`},
	{kind: "table", name: "evidence", table: "evidence", ddl: `CREATE TABLE evidence (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "case_results", table: "case_results", ddl: `CREATE TABLE case_results (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		run_id TEXT NOT NULL,
		case_id TEXT,
		request_id TEXT,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, request_id)
	)`},
	{kind: "table", name: "reports", table: "reports", ddl: `CREATE TABLE reports (
		id TEXT PRIMARY KEY,
		schema_version INTEGER NOT NULL,
		run_id TEXT NOT NULL UNIQUE,
		generated_at TEXT NOT NULL,
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "artifacts", table: "artifacts", ddl: `CREATE TABLE artifacts (
		id TEXT PRIMARY KEY,
		run_id TEXT NOT NULL,
		name TEXT NOT NULL,
		relative_path TEXT NOT NULL,
		sha256 TEXT NOT NULL,
		media_type TEXT NOT NULL,
		redacted INTEGER NOT NULL CHECK(redacted IN (0, 1)),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE,
		UNIQUE(run_id, relative_path)
	)`},
	{kind: "table", name: "report_attachments", table: "report_attachments", ddl: `CREATE TABLE report_attachments (
		report_id TEXT NOT NULL,
		artifact_id TEXT NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		PRIMARY KEY(report_id, artifact_id),
		UNIQUE(report_id, position),
		FOREIGN KEY(report_id) REFERENCES reports(id) ON DELETE CASCADE,
		FOREIGN KEY(artifact_id) REFERENCES artifacts(id)
	)`},
	{kind: "index", name: "idx_case_results_run", table: "case_results", ddl: `CREATE INDEX idx_case_results_run ON case_results(run_id, created_at, id)`},
	{kind: "index", name: "idx_case_results_case_summary", table: "case_results", ddl: `CREATE UNIQUE INDEX idx_case_results_case_summary ON case_results(run_id, case_id) WHERE request_id IS NULL AND case_id IS NOT NULL`},
	{kind: "index", name: "idx_evidence_run", table: "evidence", ddl: `CREATE INDEX idx_evidence_run ON evidence(run_id, created_at, id)`},
	{kind: "index", name: "idx_reports_run", table: "reports", ddl: `CREATE INDEX idx_reports_run ON reports(run_id)`},
	{kind: "table", name: "comparisons", table: "comparisons", ddl: `CREATE TABLE comparisons (
		id TEXT PRIMARY KEY,
		current_revision INTEGER NOT NULL CHECK(current_revision > 0),
		created_at TEXT NOT NULL,
		sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN (0, 1)),
		FOREIGN KEY(id, current_revision)
			REFERENCES comparison_revisions(comparison_id, revision)
			DEFERRABLE INITIALLY DEFERRED
	)`},
	{kind: "table", name: "quick_performance_reports", table: "quick_performance_reports", ddl: `CREATE TABLE quick_performance_reports (
		id TEXT PRIMARY KEY,
		generated_at TEXT NOT NULL,
		generated_at_unix_nano INTEGER NOT NULL,
		success INTEGER NOT NULL CHECK(success IN (0, 1)),
		model_id TEXT NOT NULL,
		base_url TEXT NOT NULL,
		phase TEXT NOT NULL CHECK(phase IN ('completed', 'cancelled')),
		completed INTEGER NOT NULL CHECK(completed > 0 AND completed <= 10000),
		failed INTEGER NOT NULL CHECK(failed >= 0 AND failed <= completed),
		document_json TEXT NOT NULL CHECK(json_valid(document_json))
	)`},
	{kind: "index", name: "idx_quick_performance_reports_generated", table: "quick_performance_reports", ddl: `CREATE INDEX idx_quick_performance_reports_generated ON quick_performance_reports(generated_at_unix_nano DESC, id DESC)`},
	{kind: "trigger", name: "quick_performance_reports_no_update", table: "quick_performance_reports", ddl: `CREATE TRIGGER quick_performance_reports_no_update
		BEFORE UPDATE ON quick_performance_reports
		BEGIN
			SELECT RAISE(ABORT, 'quick performance reports are immutable');
		END`},
	{kind: "trigger", name: "quick_performance_reports_no_delete", table: "quick_performance_reports", ddl: `CREATE TRIGGER quick_performance_reports_no_delete
		BEFORE DELETE ON quick_performance_reports
		BEGIN
			SELECT RAISE(ABORT, 'quick performance reports are immutable');
		END`},
	{kind: "table", name: "execution_run_revisions", table: "execution_run_revisions", ddl: `CREATE TABLE execution_run_revisions (
		run_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		status TEXT NOT NULL,
		snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(run_id, revision),
		FOREIGN KEY(run_id) REFERENCES execution_runs(id) ON DELETE CASCADE
	)`},
	{kind: "index", name: "idx_execution_run_revisions_updated", table: "execution_run_revisions", ddl: `CREATE INDEX idx_execution_run_revisions_updated ON execution_run_revisions(updated_at DESC, run_id)`},
	{kind: "trigger", name: "trg_execution_run_revisions_no_update", table: "execution_run_revisions", ddl: `CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END`},
	{kind: "trigger", name: "trg_execution_run_revisions_no_delete", table: "execution_run_revisions", ddl: `CREATE TRIGGER trg_execution_run_revisions_no_delete
		BEFORE DELETE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END`},
	{kind: "trigger", name: "trg_execution_run_revisions_contiguous_insert", table: "execution_run_revisions", ddl: `CREATE TRIGGER trg_execution_run_revisions_contiguous_insert
		BEFORE INSERT ON execution_run_revisions
		WHEN NEW.revision != COALESCE(
			(SELECT MAX(revision) + 1 FROM execution_run_revisions WHERE run_id = NEW.run_id),
			1
		)
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions must be contiguous');
		END`},
	{kind: "table", name: "comparison_revisions", table: "comparison_revisions", ddl: `CREATE TABLE comparison_revisions (
		comparison_id TEXT NOT NULL,
		schema_version INTEGER NOT NULL,
		revision INTEGER NOT NULL CHECK(revision > 0),
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		plan_id TEXT NOT NULL,
		plan_revision INTEGER NOT NULL,
		model_id TEXT NOT NULL,
		model_revision INTEGER NOT NULL,
		status TEXT NOT NULL CHECK(status IN ('running', 'completed', 'failed', 'cancelled')),
		document_json TEXT NOT NULL CHECK(json_valid(document_json)),
		PRIMARY KEY(comparison_id, revision),
		FOREIGN KEY(comparison_id) REFERENCES comparisons(id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "comparison_runs", table: "comparison_runs", ddl: `CREATE TABLE comparison_runs (
		comparison_id TEXT NOT NULL,
		comparison_revision INTEGER NOT NULL,
		position INTEGER NOT NULL CHECK(position >= 0),
		channel_id TEXT NOT NULL,
		channel_revision INTEGER NOT NULL,
		run_id TEXT NOT NULL,
		PRIMARY KEY(comparison_id, comparison_revision, position),
		UNIQUE(comparison_id, comparison_revision, channel_id),
		UNIQUE(comparison_id, comparison_revision, run_id),
		FOREIGN KEY(comparison_id, comparison_revision)
			REFERENCES comparison_revisions(comparison_id, revision) ON DELETE CASCADE,
		FOREIGN KEY(run_id) REFERENCES execution_runs(id)
	)`},
	{kind: "index", name: "idx_comparison_revisions_updated", table: "comparison_revisions", ddl: `CREATE INDEX idx_comparison_revisions_updated ON comparison_revisions(updated_at DESC, comparison_id)`},
	{kind: "trigger", name: "comparison_revisions_no_update", table: "comparison_revisions", ddl: `CREATE TRIGGER comparison_revisions_no_update
		BEFORE UPDATE ON comparison_revisions
		BEGIN
			SELECT RAISE(ABORT, 'comparison revisions are immutable');
		END`},
	{kind: "trigger", name: "comparison_revisions_no_delete", table: "comparison_revisions", ddl: `CREATE TRIGGER comparison_revisions_no_delete
		BEFORE DELETE ON comparison_revisions
		BEGIN
			SELECT RAISE(ABORT, 'comparison revisions are immutable');
		END`},
}
