package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// These seven frozen producers predate executor provenance in report documents.
// Unknown historical provenance stays null; preparation metadata is not evidence
// of which machine actually executed a request.
func (baseline historicalBaseline) lacksExecutionOrigin() bool {
	switch baseline.Commit[:8] {
	case "8190caae", "b4e38868", "90acda6c", "78d4c02c", "cb96ed63", "44b77fb0", "0dc44b1b":
		return true
	default:
		return false
	}
}

func upgradeReportOrigins(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, "DROP TRIGGER quick_performance_reports_no_update"); err != nil {
		return err
	}
	for _, table := range []string{"reports", "quick_performance_reports"} {
		var after string
		for {
			var id, document string
			err := conn.QueryRowContext(ctx, "SELECT id, document_json FROM "+quoteMigrationName(table)+
				" WHERE id > ? ORDER BY id LIMIT 1", after).Scan(&id, &document)
			if errors.Is(err, sql.ErrNoRows) {
				break
			}
			if err != nil {
				return err
			}
			var fields map[string]json.RawMessage
			validJSON := json.Unmarshal([]byte(document), &fields) == nil
			if !validJSON || fields["schema_version"] == nil || fields["execution_origin"] != nil {
				return errors.New("unsupported historical report fields")
			}
			fields["execution_origin"] = json.RawMessage("null")
			updated, err := json.Marshal(fields)
			if err != nil {
				return err
			}
			if _, err := conn.ExecContext(ctx, "UPDATE "+quoteMigrationName(table)+" SET document_json = ? WHERE id = ?",
				string(updated), id); err != nil {
				return err
			}
			after = id
		}
	}
	for _, object := range schemaV2Objects {
		if object.name == "quick_performance_reports_no_update" {
			_, err := conn.ExecContext(ctx, object.ddl)
			return err
		}
	}
	return errors.New("missing report immutability trigger")
}
