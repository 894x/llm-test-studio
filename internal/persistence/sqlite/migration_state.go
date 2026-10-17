package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type migrationState struct {
	version  int
	baseline historicalBaseline
}

type migrationRecord struct {
	version  int
	name     string
	checksum string
}

func inspectMigrationState(
	ctx context.Context,
	conn *sql.Conn,
	catalog migrationCatalog,
) (migrationState, error) {
	state := migrationState{}
	var userVersion, tables int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return state, fmt.Errorf("read sqlite user version: %w", err)
	}
	query := `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_migrations'`
	if err := conn.QueryRowContext(ctx, query).Scan(&tables); err != nil {
		return state, fmt.Errorf("inspect sqlite migration history: %w", err)
	}
	if tables == 0 {
		if userVersion != 0 {
			return state, schemaResetRequired("user_version is %d without migration history", userVersion)
		}
		return state, validateEmptyDatabase(ctx, conn)
	}
	if userVersion < 1 || userVersion > CurrentSchemaVersion {
		return state, schemaResetRequired("unsupported user_version %d", userVersion)
	}
	rows, err := conn.QueryContext(ctx,
		`SELECT version, name, checksum, applied_at, app_version FROM schema_migrations ORDER BY version LIMIT 3`)
	if err != nil {
		return state, schemaResetRequired("invalid migration history structure")
	}
	defer rows.Close()
	records := []migrationRecord{}
	for rows.Next() {
		var record migrationRecord
		var appliedAt, appVersion string
		if err := rows.Scan(&record.version, &record.name, &record.checksum, &appliedAt, &appVersion); err != nil {
			return state, schemaResetRequired("invalid migration history record")
		}
		timestamp, err := time.Parse(time.RFC3339Nano, appliedAt)
		if err != nil || timestamp.IsZero() || strings.TrimSpace(appVersion) == "" {
			return state, schemaResetRequired("invalid migration history metadata")
		}
		_, offset := timestamp.Zone()
		if offset != 0 || record.version != len(records)+1 {
			return state, schemaResetRequired("non-contiguous migration history")
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return state, fmt.Errorf("read sqlite migration records: %w", err)
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	if len(records) != userVersion {
		return state, schemaResetRequired("migration history and user_version disagree")
	}
	baseline, found := catalog.baseline(records[0].checksum)
	if !found || records[0].name != operationalBaselineName {
		return state, schemaResetRequired("baseline is outside the ten supported operational formats")
	}
	objects := baseline.objects
	if userVersion == CurrentSchemaVersion {
		last := records[len(records)-1]
		if last.name != currentSchemaName || last.checksum != currentSchemaChecksum() {
			return state, schemaResetRequired("upgrade migration identity does not match")
		}
		objects = currentSchemaObjects
	}
	if err := validateSchemaObjects(ctx, conn, objects); err != nil {
		return state, err
	}
	return migrationState{version: userVersion, baseline: baseline}, nil
}

func recordMigration(ctx context.Context, conn *sql.Conn, record migrationRecord, appVersion string) error {
	_, err := conn.ExecContext(
		ctx,
		`INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version) VALUES(?, ?, ?, ?, ?)`,
		record.version,
		record.name,
		record.checksum,
		time.Now().UTC().Format(time.RFC3339Nano),
		appVersion,
	)
	if err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", record.name, err)
	}
	return nil
}

func recordCurrentMigration(ctx context.Context, conn *sql.Conn, appVersion string) error {
	if err := recordMigration(ctx, conn, migrationRecord{
		version: CurrentSchemaVersion, name: currentSchemaName, checksum: currentSchemaChecksum(),
	}, appVersion); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, "PRAGMA user_version = 2")
	return err
}
