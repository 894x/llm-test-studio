package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type migrationTableFacts struct {
	table       string
	columns     []string
	stripTarget bool
	stripOrigin bool
	archived    bool
	digest      string
}

// The writer already holds BEGIN IMMEDIATE without having changed any pages.
// A separate read connection supplies the online backup: using the writer as
// the backup source would make sqlite3_backup_step return SQLITE_BUSY forever.
func backupMigrationSource(ctx context.Context, path string, baseline historicalBaseline) (string, error) {
	source, err := openRunnerBackupSource(ctx, path)
	if err != nil {
		return "", runnerBackupError(ctx, "open migration backup source", err)
	}
	defer source.close()
	before, err := migrationFacts(ctx, source.conn, baseline.objects, false)
	if err != nil {
		return "", runnerBackupError(ctx, "read migration backup source", err)
	}
	directory, err := os.MkdirTemp(filepath.Dir(source.path), filepath.Base(source.path)+".upgrade-backup-")
	if err != nil {
		return "", fmt.Errorf("create sqlite upgrade backup directory: %w", err)
	}
	// Retain even incomplete backups for diagnosis; they are never auto-restored.
	destination := filepath.Join(directory, "database.sqlite")
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return "", fmt.Errorf("create sqlite upgrade backup: %w", err)
	}
	defer file.Close()
	created, err := file.Stat()
	if err != nil {
		return "", err
	}
	if err := copyRunnerSnapshot(ctx, source.conn, destination); err != nil {
		return "", runnerBackupError(ctx, "copy migration backup", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("sync sqlite upgrade backup: %w", err)
	}
	current, err := os.Lstat(destination)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(created, current) {
		return "", errors.New("sqlite upgrade backup identity changed")
	}
	backup, err := openRunnerBackupSource(ctx, destination)
	if err != nil {
		return "", runnerBackupError(ctx, "open migration backup verification", err)
	}
	defer backup.close()
	if err := validateSchemaObjects(ctx, backup.conn, baseline.objects); err != nil {
		return "", err
	}
	if err := validateIntegrity(ctx, backup.conn); err != nil {
		return "", runnerBackupError(ctx, "verify migration backup integrity", err)
	}
	if err := verifyMigrationFacts(ctx, backup.conn, before); err != nil {
		return "", err
	}
	if err := source.verifyIdentity(); err != nil {
		return "", err
	}
	backup.close()
	if err := file.Close(); err != nil {
		return "", err
	}
	published, err := publishMigrationBackup(directory)
	if err != nil {
		return "", fmt.Errorf("publish verified sqlite upgrade backup: %w", err)
	}
	return filepath.Join(published, "database.sqlite"), nil
}

func migrationFacts(
	ctx context.Context,
	conn *sql.Conn,
	objects []schemaObject,
	upgrading bool,
) ([]migrationTableFacts, error) {
	names := []string{"sqlite_sequence"}
	for _, object := range objects {
		if object.kind == "table" && (!upgrading || object.name != "schema_migrations") {
			names = append(names, object.name)
		}
	}
	slices.Sort(names)
	facts := make([]migrationTableFacts, 0, len(names))
	for _, name := range names {
		columns, err := migrationColumns(ctx, conn, name)
		if err != nil {
			return nil, err
		}
		fact := migrationTableFacts{
			table: name, columns: columns, stripTarget: upgrading && name == "job_control_outbox",
			stripOrigin: upgrading && (name == "reports" || name == "quick_performance_reports"),
		}
		fact.digest, err = migrationTableDigest(ctx, conn, fact)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

func migrationColumns(ctx context.Context, conn relationQueryer, table string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	return columns, rows.Err()
}

func verifyMigrationFacts(ctx context.Context, conn *sql.Conn, facts []migrationTableFacts) error {
	for _, fact := range facts {
		digest, err := migrationTableDigest(ctx, conn, fact)
		if fact.archived {
			var count int
			if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quoteMigrationName(fact.table)).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return errors.New("historical execution queue was not archived")
			}
			digest, _, err = historyTableDigest(ctx, conn, fact.table, len(fact.columns))
		}
		if err != nil {
			return err
		}
		if digest != fact.digest {
			return fmt.Errorf("sqlite upgrade did not preserve table %q", fact.table)
		}
	}
	return nil
}

func migrationTableDigest(ctx context.Context, conn *sql.Conn, fact migrationTableFacts) (string, error) {
	columns := make([]string, 0, len(fact.columns))
	for _, column := range fact.columns {
		columns = append(columns, quoteMigrationName(column))
	}
	if len(columns) == 0 {
		return "", errors.New("sqlite migration table has no columns")
	}
	projection := strings.Join(columns, ", ")
	// Names come only from a schema already matched against a frozen baseline.
	query := "SELECT " + projection + " FROM " + quoteMigrationName(fact.table) + " ORDER BY " + projection
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return "", runnerBackupError(ctx, "read migration preservation facts", err)
	}
	defer rows.Close()
	digest := sha256.New()
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return "", runnerBackupError(ctx, "scan migration preservation facts", err)
		}
		writeRunnerDigest(digest, 'R', nil)
		for index, value := range values {
			if (fact.stripTarget || fact.stripOrigin) && fact.columns[index] == "document_json" {
				var document []byte
				switch typed := value.(type) {
				case string:
					document = []byte(typed)
				case []byte:
					document = typed
				default:
					return "", errors.New("invalid migration document storage type")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(document, &fields); err != nil {
					return "", errors.New("invalid control document")
				}
				if fact.stripTarget {
					delete(fields, "target")
				}
				if fact.stripOrigin && string(fields["execution_origin"]) == "null" {
					delete(fields, "execution_origin")
				}
				normalized, err := json.Marshal(fields)
				if err != nil {
					return "", err
				}
				value = string(normalized)
			}
			if err := hashRunnerValue(digest, value); err != nil {
				return "", err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", runnerBackupError(ctx, "iterate migration preservation facts", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func quoteMigrationName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
