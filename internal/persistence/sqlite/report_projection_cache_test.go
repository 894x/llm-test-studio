package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestCachedReportProjectionsRevalidateChangedDurableInputs(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{name: "report", statement: `UPDATE reports SET schema_version = 0`},
		{name: "historical revision", statement: `UPDATE execution_run_revisions SET status = 'unknown' WHERE revision = 1`},
		{name: "summary result", statement: `UPDATE case_results SET document_json = '{}'`},
		{name: "evidence", statement: `UPDATE evidence SET run_id = 'ffffffff-ffff-4fff-8fff-ffffffffffff'`},
		{name: "artifact", statement: `UPDATE artifacts SET name = 'changed'`},
		{name: "attachment", statement: `UPDATE report_attachments SET position = 42`},
		{name: "seal", statement: `UPDATE execution_runs SET sealed = 0`},
		{name: "superseded format", statement: `UPDATE reports SET document_json = json_remove(document_json, '$.entry_reports')`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			defer repository.Close()
			storeFixtureReport(t, repository, fixture)
			first, err := repository.ListReportProjections(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			second, err := repository.ListReportProjections(context.Background())
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("cache changed projection: %v", err)
			}
			second[0].PlanName = "caller mutation"
			third, err := repository.ListReportProjections(context.Background())
			if err != nil || third[0].PlanName != first[0].PlanName {
				t.Fatal("cache retained caller mutation")
			}
			// tamper uses another connection, so this covers external writes too.
			if test.name == "historical revision" {
				tamper(t, path, `DROP TRIGGER trg_execution_run_revisions_no_update`)
			}
			tamper(t, path, test.statement)
			if _, err := repository.ListReportProjections(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("changed input reused cached validation: %v", err)
			}
		})
	}
}
