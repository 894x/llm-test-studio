package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

// Version 4 adds reversible presentation state without rewriting or removing
// immutable execution evidence. Released definitions must remain unchanged.
var historyRecycleObjects = []schemaObject{
	{kind: "table", name: "history_recycle_entries", table: "history_recycle_entries", ddl: `CREATE TABLE history_recycle_entries (
		kind TEXT NOT NULL CHECK(kind IN ('job', 'run', 'report', 'comparison')),
		id TEXT NOT NULL CHECK(length(id) > 0),
		title TEXT NOT NULL,
		removed_at TEXT NOT NULL,
		remote INTEGER NOT NULL CHECK(remote IN (0, 1)),
		PRIMARY KEY(kind, id)
	)`},
	{kind: "table", name: "history_recycle_members", table: "history_recycle_members", ddl: `CREATE TABLE history_recycle_members (
		owner_kind TEXT NOT NULL,
		owner_id TEXT NOT NULL,
		kind TEXT NOT NULL CHECK(kind IN ('job', 'run', 'report', 'comparison')),
		id TEXT NOT NULL CHECK(length(id) > 0),
		PRIMARY KEY(owner_kind, owner_id, kind, id),
		FOREIGN KEY(owner_kind, owner_id) REFERENCES history_recycle_entries(kind, id) ON DELETE CASCADE
	)`},
	{kind: "index", name: "idx_history_recycle_members_identity", table: "history_recycle_members",
		ddl: `CREATE INDEX idx_history_recycle_members_identity ON history_recycle_members(kind, id)`},
	{kind: "index", name: "idx_history_recycle_entries_removed", table: "history_recycle_entries",
		ddl: `CREATE INDEX idx_history_recycle_entries_removed ON history_recycle_entries(removed_at DESC, id DESC)`},
}

var currentSchemaObjects = append(append([]schemaObject{}, schemaV3Objects...), historyRecycleObjects...)

func upgradeHistoryRecycle(
	ctx context.Context,
	conn *sql.Conn,
	path string,
	options MigrateOptions,
	backup bool,
) error {
	if backup {
		options.progress("backing_up", 3)
		if _, err := backupMigrationSource(ctx, path, historicalBaseline{objects: schemaV3Objects}); err != nil {
			return err
		}
	}
	options.progress("migrating", 3)
	for _, object := range historyRecycleObjects {
		if _, err := conn.ExecContext(ctx, object.ddl); err != nil {
			return fmt.Errorf("apply sqlite history recycle object %q: %w", object.name, err)
		}
	}
	// These empty tables and their indexes cannot change historical rows.
	// Migrate validates the target schema before committing the transaction.
	return recordCurrentMigration(ctx, conn, options.AppVersion)
}
