package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	// CatalogExportSchemaVersion is the last schema that still contains the
	// legacy authored catalog and can therefore act as an export source.
	CatalogExportSchemaVersion = 10
	// AuthoredCatalogRetirementSchemaVersion is the operational-only schema.
	AuthoredCatalogRetirementSchemaVersion = 11

	migration0011Name      = "0011_authored_catalog_retirement"
	migration0011Algorithm = "require-export-bound-authored-catalog-digest-complete-v10-v2-snapshots-empty-integrations-keyring-preparation-and-retain-operational-schema-v2"

	authoredCatalogDigestFormat = "llm-test-studio-authored-sqlite-catalog-sha256-v1"
)

var errAuthoredCatalogChangedAfterExport = errors.New("authored sqlite catalog changed after file export; refusing retirement")

// migration0011RetiredTables is ordered from the most dependent authored
// object to the least dependent one. The order is part of the migration's
// durable checksum and must not be changed after release.
var migration0011RetiredTables = []string{
	"integrations",
	"plan_channel_models",
	"plan_cases",
	"plan_channels",
	"plan_models",
	"test_plans",
	"suite_cases",
	"test_case_import_sources",
	"channel_models",
	"channels",
	"credential_refs",
	"test_suites",
	"test_cases",
	"models",
	"catalog_tombstones",
	"case_catalog_cutover",
	"pending_test_case_snapshots",
	"builtin_catalog_seeds",
}

// Deliberately do not use IF EXISTS. A missing authored table means the v10
// schema is not the schema that was reviewed for retirement and must fail the
// transaction instead of silently accepting an incomplete cutover.
var migration0011Statements = []string{
	`DROP TABLE integrations`,
	`DROP TABLE plan_channel_models`,
	`DROP TABLE plan_cases`,
	`DROP TABLE plan_channels`,
	`DROP TABLE plan_models`,
	`DROP TABLE test_plans`,
	`DROP TABLE suite_cases`,
	`DROP TABLE test_case_import_sources`,
	`DROP TABLE channel_models`,
	`DROP TABLE channels`,
	`DROP TABLE credential_refs`,
	`DROP TABLE test_suites`,
	`DROP TABLE test_cases`,
	`DROP TABLE models`,
	`DROP TABLE catalog_tombstones`,
	`DROP TABLE case_catalog_cutover`,
	`DROP TABLE pending_test_case_snapshots`,
	`DROP TABLE builtin_catalog_seeds`,
}

// AuthoredCatalogDigest returns a deterministic SHA-256 digest of every table
// that schema v11 will retire. Operational Run, Result, Report, and immutable
// snapshot tables are intentionally excluded. The database must be a fully
// valid schema v10 database; this function never mutates it.
func AuthoredCatalogDigest(ctx context.Context, path string) (string, error) {
	digest, err := readAuthoredCatalogDigest(ctx, path)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest), nil
}

func readAuthoredCatalogDigest(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("authored sqlite catalog digest path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("authored sqlite catalog digest requires an existing version 10 database")
		}
		return nil, fmt.Errorf("inspect authored sqlite catalog digest path: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("authored sqlite catalog digest path must be a regular file")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open authored sqlite catalog digest database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire authored sqlite catalog digest connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA query_only = ON"); err != nil {
		return nil, fmt.Errorf("enable read-only authored sqlite catalog digest: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return nil, fmt.Errorf("begin authored sqlite catalog digest snapshot: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return nil, fmt.Errorf("validate authored sqlite catalog digest schema: %w", err)
	}
	if version != CatalogExportSchemaVersion {
		return nil, fmt.Errorf("authored sqlite catalog digest requires schema version %d, got version %d", CatalogExportSchemaVersion, version)
	}
	if err := validateMigration0011Eligibility(ctx, conn); err != nil {
		return nil, fmt.Errorf("validate authored sqlite catalog retirement eligibility: %w", err)
	}
	digest, err := calculateAuthoredCatalogDigest(ctx, conn)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("finish authored sqlite catalog digest snapshot: %w", err)
	}
	committed = true
	return digest, nil
}

func decodeAuthoredCatalogDigest(value string) ([]byte, error) {
	if value == "" {
		return nil, errors.New("expected authored sqlite catalog digest is required for retirement")
	}
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return nil, errors.New("expected authored sqlite catalog digest must be 64 lowercase hexadecimal characters")
	}
	digest, err := hex.DecodeString(value)
	if err != nil || len(digest) != sha256.Size {
		return nil, errors.New("expected authored sqlite catalog digest must be 64 lowercase hexadecimal characters")
	}
	return digest, nil
}

func authoredCatalogDigestsEqual(left, right []byte) bool {
	return len(left) == sha256.Size && len(right) == sha256.Size && subtle.ConstantTimeCompare(left, right) == 1
}

type authoredCatalogColumn struct {
	position     int64
	name         string
	declaredType string
	notNull      int64
	defaultValue any
	primaryKey   int64
	hidden       int64
}

const (
	digestFieldFormat byte = iota + 1
	digestFieldTable
	digestFieldColumnCount
	digestFieldColumn
	digestFieldRowCount
	digestFieldRow
	digestFieldColumnPosition
	digestFieldColumnName
	digestFieldColumnDeclaredType
	digestFieldColumnNotNull
	digestFieldColumnDefault
	digestFieldColumnPrimaryKey
	digestFieldColumnHidden
	digestValueNull
	digestValueInteger
	digestValueFloat
	digestValueText
	digestValueBlob
)

func calculateAuthoredCatalogDigest(ctx context.Context, conn *sql.Conn) ([]byte, error) {
	var payload []byte
	appendDigestField(&payload, digestFieldFormat, []byte(authoredCatalogDigestFormat))
	for _, table := range migration0011RetiredTables {
		columns, err := authoredCatalogDigestColumns(ctx, conn, table)
		if err != nil {
			return nil, err
		}
		appendDigestField(&payload, digestFieldTable, []byte(table))
		appendDigestUint64(&payload, digestFieldColumnCount, uint64(len(columns)))
		for _, column := range columns {
			var definition []byte
			appendDigestInt64(&definition, digestFieldColumnPosition, column.position)
			appendDigestField(&definition, digestFieldColumnName, []byte(column.name))
			appendDigestField(&definition, digestFieldColumnDeclaredType, []byte(column.declaredType))
			appendDigestInt64(&definition, digestFieldColumnNotNull, column.notNull)
			if err := appendDigestSQLValue(&definition, digestFieldColumnDefault, column.defaultValue); err != nil {
				return nil, fmt.Errorf("encode authored sqlite catalog table %q column %q default: %w", table, column.name, err)
			}
			appendDigestInt64(&definition, digestFieldColumnPrimaryKey, column.primaryKey)
			appendDigestInt64(&definition, digestFieldColumnHidden, column.hidden)
			appendDigestField(&payload, digestFieldColumn, definition)
		}

		encodedRows, err := authoredCatalogDigestRows(ctx, conn, table, columns)
		if err != nil {
			return nil, err
		}
		sort.Slice(encodedRows, func(left, right int) bool {
			return bytes.Compare(encodedRows[left], encodedRows[right]) < 0
		})
		appendDigestUint64(&payload, digestFieldRowCount, uint64(len(encodedRows)))
		for _, row := range encodedRows {
			appendDigestField(&payload, digestFieldRow, row)
		}
	}
	sum := sha256.Sum256(payload)
	return append([]byte(nil), sum[:]...), nil
}

func authoredCatalogDigestColumns(ctx context.Context, conn *sql.Conn, table string) ([]authoredCatalogColumn, error) {
	rows, err := conn.QueryContext(ctx, "PRAGMA table_xinfo("+digestQuoteIdentifier(table)+")")
	if err != nil {
		return nil, fmt.Errorf("inspect authored sqlite catalog table %q columns: %w", table, err)
	}
	defer rows.Close()
	var columns []authoredCatalogColumn
	for rows.Next() {
		var column authoredCatalogColumn
		if err := rows.Scan(
			&column.position,
			&column.name,
			&column.declaredType,
			&column.notNull,
			&column.defaultValue,
			&column.primaryKey,
			&column.hidden,
		); err != nil {
			return nil, fmt.Errorf("read authored sqlite catalog table %q columns: %w", table, err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authored sqlite catalog table %q columns: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close authored sqlite catalog table %q columns: %w", table, err)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("authored sqlite catalog table %q has no columns", table)
	}
	sort.Slice(columns, func(left, right int) bool {
		if columns[left].position != columns[right].position {
			return columns[left].position < columns[right].position
		}
		return columns[left].name < columns[right].name
	})
	return columns, nil
}

func authoredCatalogDigestRows(
	ctx context.Context,
	conn *sql.Conn,
	table string,
	columns []authoredCatalogColumn,
) ([][]byte, error) {
	quotedColumns := make([]string, len(columns))
	for index, column := range columns {
		quotedColumns[index] = digestQuoteIdentifier(column.name)
	}
	query := "SELECT " + strings.Join(quotedColumns, ", ") + " FROM " + digestQuoteIdentifier(table)
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read authored sqlite catalog table %q rows: %w", table, err)
	}
	defer rows.Close()
	var encodedRows [][]byte
	for rows.Next() {
		values := make([]any, len(columns))
		destinations := make([]any, len(columns))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return nil, fmt.Errorf("scan authored sqlite catalog table %q row: %w", table, err)
		}
		var encoded []byte
		for _, value := range values {
			if err := appendDigestSQLValue(&encoded, 0, value); err != nil {
				return nil, fmt.Errorf("encode authored sqlite catalog table %q row: %w", table, err)
			}
		}
		encodedRows = append(encodedRows, encoded)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authored sqlite catalog table %q rows: %w", table, err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close authored sqlite catalog table %q rows: %w", table, err)
	}
	return encodedRows, nil
}

func appendDigestSQLValue(destination *[]byte, wrapper byte, value any) error {
	var encoded []byte
	var valueType byte
	switch typed := value.(type) {
	case nil:
		valueType = digestValueNull
	case int64:
		valueType = digestValueInteger
		encoded = digestInt64Bytes(typed)
	case float64:
		valueType = digestValueFloat
		encoded = digestUint64Bytes(math.Float64bits(typed))
	case string:
		valueType = digestValueText
		encoded = []byte(typed)
	case []byte:
		valueType = digestValueBlob
		encoded = typed
	default:
		return fmt.Errorf("unsupported SQLite value type %T", value)
	}
	var typedValue []byte
	appendDigestField(&typedValue, valueType, encoded)
	if wrapper == 0 {
		*destination = append(*destination, typedValue...)
	} else {
		appendDigestField(destination, wrapper, typedValue)
	}
	return nil
}

func appendDigestField(destination *[]byte, fieldType byte, value []byte) {
	*destination = append(*destination, fieldType)
	*destination = append(*destination, digestUint64Bytes(uint64(len(value)))...)
	*destination = append(*destination, value...)
}

func appendDigestInt64(destination *[]byte, fieldType byte, value int64) {
	appendDigestField(destination, fieldType, digestInt64Bytes(value))
}

func appendDigestUint64(destination *[]byte, fieldType byte, value uint64) {
	appendDigestField(destination, fieldType, digestUint64Bytes(value))
}

func digestInt64Bytes(value int64) []byte {
	return digestUint64Bytes(uint64(value))
}

func digestUint64Bytes(value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	return encoded[:]
}

func digestQuoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func migration0011Checksum() string {
	definition := strings.Join([]string{
		migration0011Name,
		migration0011Algorithm,
		strings.Join(migration0011Statements, "\n-- statement --\n"),
	}, "\n-- statement --\n")
	sum := sha256.Sum256([]byte(definition))
	return hex.EncodeToString(sum[:])
}

// applyMigration0011 is the transaction-local form used by focused migration
// tests. Capturing the digest after BEGIN IMMEDIATE is safe because no external
// file export can occur between this digest and the retirement transaction.
func applyMigration0011(ctx context.Context, conn *sql.Conn, appVersion string) error {
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return fmt.Errorf("retire authored sqlite catalog only after a valid version 10 migration: %w", err)
	}
	if version != CatalogExportSchemaVersion {
		return fmt.Errorf("retire authored sqlite catalog requires completed version %d, got version %d", CatalogExportSchemaVersion, version)
	}
	digest, err := calculateAuthoredCatalogDigest(ctx, conn)
	if err != nil {
		return err
	}
	return applyMigration0011WithExpectedDigest(ctx, conn, appVersion, digest)
}

// applyMigration0011WithExpectedDigest physically retires authored
// configuration only after the v10 history, the externally exported catalog
// digest, and complete v2 runtime snapshots have all been revalidated inside
// the coordinator's BEGIN IMMEDIATE transaction.
func applyMigration0011WithExpectedDigest(ctx context.Context, conn *sql.Conn, appVersion string, expectedDigest []byte) error {
	return applyMigration0011WithExpectedDigestAndPreparation(
		ctx, conn, appVersion, expectedDigest, func(context.Context) error { return nil },
	)
}

func applyMigration0011WithExpectedDigestAndPreparation(
	ctx context.Context,
	conn *sql.Conn,
	appVersion string,
	expectedDigest []byte,
	prepare func(context.Context) error,
) error {
	if strings.TrimSpace(appVersion) == "" {
		return errors.New("sqlite migration 0011 app version is required")
	}
	if err := preflightMigration0011(ctx, conn, expectedDigest); err != nil {
		return err
	}
	if prepare != nil {
		if err := prepare(ctx); err != nil {
			return fmt.Errorf("prepare authored sqlite catalog retirement: %w", err)
		}
	} else {
		var credentialRefs int
		if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM credential_refs`).Scan(&credentialRefs); err != nil {
			return fmt.Errorf("inspect credential references before authored sqlite catalog retirement: %w", err)
		}
		if credentialRefs != 0 {
			return errors.New("authored sqlite catalog retirement with credential references requires keyring preparation")
		}
	}
	for _, statement := range migration0011Statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("apply sqlite migration %s: %w", migration0011Name, err)
		}
	}
	if _, err := conn.ExecContext(ctx, `
		INSERT INTO schema_migrations(version, name, checksum, applied_at, app_version)
		VALUES(11, ?, ?, ?, ?)
	`, migration0011Name, migration0011Checksum(), time.Now().UTC().Format(time.RFC3339Nano), appVersion); err != nil {
		return fmt.Errorf("record sqlite migration %s: %w", migration0011Name, err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA user_version = 11"); err != nil {
		return fmt.Errorf("set sqlite user version: %w", err)
	}
	if err := validateAppliedSchema0011(ctx, conn); err != nil {
		return fmt.Errorf("validate sqlite migration %s: %w", migration0011Name, err)
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return fmt.Errorf("validate sqlite migration %s integrity: %w", migration0011Name, err)
	}
	return nil
}

func preflightMigration0011(ctx context.Context, conn *sql.Conn, expectedDigest []byte) error {
	if len(expectedDigest) != sha256.Size {
		return errors.New("expected authored sqlite catalog digest is invalid for retirement")
	}
	version, err := appliedMigrationVersion(ctx, conn)
	if err != nil {
		return fmt.Errorf("retire authored sqlite catalog only after a valid version 10 migration: %w", err)
	}
	if version != CatalogExportSchemaVersion {
		return fmt.Errorf("retire authored sqlite catalog requires completed version %d, got version %d", CatalogExportSchemaVersion, version)
	}

	if err := validateMigration0011Eligibility(ctx, conn); err != nil {
		return err
	}
	currentDigest, err := calculateAuthoredCatalogDigest(ctx, conn)
	if err != nil {
		return err
	}
	if !authoredCatalogDigestsEqual(currentDigest, expectedDigest) {
		return errAuthoredCatalogChangedAfterExport
	}
	return nil
}

func validateMigration0011Eligibility(ctx context.Context, conn *sql.Conn) error {
	var integrations int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM integrations`).Scan(&integrations); err != nil {
		return fmt.Errorf("inspect integrations before authored sqlite catalog retirement: %w", err)
	}
	if integrations != 0 {
		return fmt.Errorf("authored sqlite catalog retirement is blocked: integrations contains %d row(s)", integrations)
	}
	if err := validateMigration0011V2Documents(ctx, conn); err != nil {
		return err
	}
	if err := validateIntegrity(ctx, conn); err != nil {
		return fmt.Errorf("validate sqlite before authored catalog retirement: %w", err)
	}
	return nil
}

func validateAppliedSchema0011(ctx context.Context, conn *sql.Conn) error {
	if err := validateMigration0011RetiredTablesAbsent(ctx, conn); err != nil {
		return err
	}
	if err := validateMigration0011OperationalSchema(ctx, conn); err != nil {
		return err
	}
	return validateMigration0011V2Documents(ctx, conn)
}

func validateMigration0011RetiredTablesAbsent(ctx context.Context, conn *sql.Conn) error {
	for _, table := range migration0011RetiredTables {
		var count int
		if err := conn.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?
		`, table).Scan(&count); err != nil {
			return fmt.Errorf("inspect retired authored table %q: %w", table, err)
		}
		if count != 0 {
			return fmt.Errorf("sqlite schema is unknown: retired authored table %q exists", table)
		}
	}
	return nil
}

func validateMigration0011OperationalSchema(ctx context.Context, conn *sql.Conn) error {
	expectedTables := map[string]bool{
		"schema_migrations":  true,
		"runs":               true,
		"run_results":        true,
		"audit_runs":         true,
		"audit_case_results": true,
	}
	expectedIndexes := make(map[string]string, len(legacyIndexes))
	for name, definition := range legacyIndexes {
		expectedIndexes[name] = definition.table
	}
	expectedTriggers := make(map[string]string)
	expectedDDL := make(map[string]string)

	retired := make(map[string]bool, len(migration0011RetiredTables))
	for _, table := range migration0011RetiredTables {
		retired[table] = true
	}
	groups := [][]string{
		migration0002Statements,
		migration0003Statements,
		migration0004Statements,
		migration0005Statements,
		migration0006Statements,
		migration0007Statements,
		migration0008Statements,
		migration0009Statements,
	}
	for _, statements := range groups {
		for _, statement := range statements {
			match := migration0002ObjectPattern.FindStringSubmatch(strings.TrimSpace(statement))
			if len(match) != 3 {
				return errors.New("sqlite migration contains an unrecognized retained-schema statement")
			}
			kind := strings.ToLower(match[1])
			name := strings.ToLower(match[2])
			if kind == "table" {
				if retired[name] {
					continue
				}
				expectedTables[name] = true
				expectedDDL[kind+":"+name] = normalizeDDL(statement)
				continue
			}

			tableMatch := migrationTriggerTablePattern.FindStringSubmatch(statement)
			if len(tableMatch) != 2 {
				return fmt.Errorf("sqlite migration retained %s %q has no target table", kind, name)
			}
			table := strings.ToLower(tableMatch[1])
			if retired[table] {
				continue
			}
			expectedDDL[kind+":"+name] = normalizeDDL(statement)
			if kind == "index" {
				expectedIndexes[name] = table
			} else if kind == "trigger" {
				expectedTriggers[name] = table
			} else {
				return fmt.Errorf("sqlite migration retained object %q has unsupported kind %q", name, kind)
			}
		}
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT type, name, tbl_name, sql FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		return fmt.Errorf("inspect migrated sqlite v11 objects: %w", err)
	}
	seenTables := make(map[string]bool)
	seenIndexes := make(map[string]bool)
	seenTriggers := make(map[string]bool)
	for rows.Next() {
		var objectType, name, table string
		var definition sql.NullString
		if err := rows.Scan(&objectType, &name, &table, &definition); err != nil {
			rows.Close()
			return fmt.Errorf("read migrated sqlite v11 object: %w", err)
		}
		switch objectType {
		case "table":
			if retired[name] {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: retired authored table %q exists", name)
			}
			if !expectedTables[name] {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated v11 database has table %q", name)
			}
			seenTables[name] = true
		case "index":
			expectedTable, ok := expectedIndexes[name]
			if !ok || expectedTable != table {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated v11 database has index %q on table %q", name, table)
			}
			seenIndexes[name] = true
		case "trigger":
			expectedTable, ok := expectedTriggers[name]
			if !ok || expectedTable != table {
				rows.Close()
				return fmt.Errorf("sqlite schema is unknown: migrated v11 database has trigger %q on table %q", name, table)
			}
			seenTriggers[name] = true
		default:
			rows.Close()
			return fmt.Errorf("sqlite schema is unknown: migrated v11 database has %s %q", objectType, name)
		}
		if expected, ok := expectedDDL[objectType+":"+name]; ok {
			if !definition.Valid || normalizeDDL(definition.String) != expected {
				rows.Close()
				return fmt.Errorf("sqlite schema drift detected for %s %q", objectType, name)
			}
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate migrated sqlite v11 objects: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close migrated sqlite v11 objects: %w", err)
	}
	for table := range expectedTables {
		if !seenTables[table] {
			return fmt.Errorf("sqlite schema is unknown: migrated v11 database is missing table %q", table)
		}
	}
	for index := range expectedIndexes {
		if !seenIndexes[index] {
			return fmt.Errorf("sqlite schema is unknown: migrated v11 database is missing index %q", index)
		}
	}
	for trigger := range expectedTriggers {
		if !seenTriggers[trigger] {
			return fmt.Errorf("sqlite schema is unknown: migrated v11 database is missing trigger %q", trigger)
		}
	}
	for name, expected := range legacyIndexes {
		if err := validateLegacyIndex(ctx, conn, name, expected); err != nil {
			return err
		}
	}
	for table := range legacyTableColumns {
		if err := validateLegacyTable(ctx, conn, table, true); err != nil {
			return err
		}
	}
	return validateMigrationTable(ctx, conn)
}

func validateMigration0011V2Documents(ctx context.Context, conn *sql.Conn) error {
	checks := []struct {
		name  string
		query string
	}{
		{
			name: "run snapshot",
			query: `SELECT COUNT(*) FROM execution_run_revisions
				WHERE json_type(snapshot_json, '$.schema_version') IS NOT 'integer'
				   OR json_extract(snapshot_json, '$.schema_version') IS NOT 2`,
		},
		{
			name: "run document",
			query: `SELECT COUNT(*) FROM execution_run_revisions
				WHERE json_type(document_json, '$.plan_snapshot.schema_version') IS NOT 'integer'
				   OR json_extract(document_json, '$.plan_snapshot.schema_version') IS NOT 2`,
		},
		{
			name: "report snapshot",
			query: `SELECT COUNT(*) FROM reports
				WHERE json_type(document_json, '$.plan_snapshot.schema_version') IS NOT 'integer'
				   OR json_extract(document_json, '$.plan_snapshot.schema_version') IS NOT 2`,
		},
	}
	for _, check := range checks {
		var invalid int
		if err := conn.QueryRowContext(ctx, check.query).Scan(&invalid); err != nil {
			return fmt.Errorf("validate sqlite migration %s %s: %w", migration0011Name, check.name, err)
		}
		if invalid != 0 {
			return fmt.Errorf("sqlite migration %s %s must contain complete integer version 2 snapshots", migration0011Name, check.name)
		}
	}

	runUpdates, runStates, err := prepareMigration0010Runs(ctx, conn)
	if err != nil {
		return fmt.Errorf("validate sqlite migration %s run documents: %w", migration0011Name, err)
	}
	if len(runUpdates) != 0 {
		return fmt.Errorf("sqlite migration %s found a run that still requires v10 backfill", migration0011Name)
	}
	reportUpdates, err := prepareMigration0010Reports(ctx, conn, runStates)
	if err != nil {
		return fmt.Errorf("validate sqlite migration %s report documents: %w", migration0011Name, err)
	}
	if len(reportUpdates) != 0 {
		return fmt.Errorf("sqlite migration %s found a report that still requires v10 backfill", migration0011Name)
	}
	return nil
}
