package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestReportDetailsRevalidateChangedDurableInputs(t *testing.T) {
	for _, test := range []struct{ name, statement string }{
		{name: "report", statement: `UPDATE reports SET schema_version = 0`},
		{name: "historical revision", statement: `UPDATE execution_run_revisions SET status = 'unknown' WHERE revision = 1`},
		{name: "historical unknown field", statement: `UPDATE execution_run_revisions SET document_json = json_set(document_json, '$.extra', 1) WHERE revision = 2`},
		{name: "historical changed snapshot", statement: `UPDATE execution_run_revisions SET document_json = json_set(document_json, '$.plan_snapshot.environment.app_version', 'changed') WHERE revision = 2`},
		{name: "historical unexpected failure", statement: `UPDATE execution_run_revisions SET document_json = json_set(document_json, '$.failure', json('{}')) WHERE revision = 2`},
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
			if _, _, err := repository.GetReportWithResults(context.Background(), fixture.report.ID); err != nil {
				t.Fatal(err)
			}
			first, err := repository.ListReportProjections(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			second, err := repository.ListReportProjections(context.Background())
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("repeat read changed projection: %v", err)
			}
			second[0].PlanName = "caller mutation"
			third, err := repository.ListReportProjections(context.Background())
			if err != nil || third[0].PlanName != first[0].PlanName {
				t.Fatal("summary read retained caller mutation")
			}
			// tamper uses another connection, so this covers external writes too.
			if strings.HasPrefix(test.name, "historical") {
				tamper(t, path, `DROP TRIGGER trg_execution_run_revisions_no_update`)
			}
			tamper(t, path, test.statement)
			if _, err := repository.GetReport(context.Background(), fixture.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("detail read accepted changed durable input: %v", err)
			}
			if _, _, err := repository.GetReportWithResults(context.Background(), fixture.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("combined detail read accepted changed durable input: %v", err)
			}
		})
	}
}

func TestReportWithResultsPreservesRequestsAndValidatesExecutionMarkers(t *testing.T) {
	ctx := context.Background()
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	run := createRunningOutputs(t, repository, fixture)
	request := fixture.result
	request.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000035", 1)
	request.RequestID = "request-1"
	marker := domain.Result{
		EntityMeta: entityMeta("10000000-0000-4000-8000-000000000036", 1),
		RunID:      run.Meta().ID, EntryID: request.EntryID, EntryStatus: domain.EntryExecutionCompleted,
	}
	if err := repository.AppendResults(ctx, request, marker); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	got, requests, err := repository.GetReportWithResults(ctx, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(t, "report", report, got)
	assertRoundTrip(t, "requests", []domain.Result{request}, requests)
	// No retained mutable document can hide a later durable read or a change.
	requests[0].Metrics["caller_only"] = 1
	_, again, err := repository.GetReportWithResults(ctx, report.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(t, "repeat requests", []domain.Result{request}, again)
	tamper(t, path, `UPDATE case_results SET document_json = '{}' WHERE id = ?`, marker.ID)
	if _, _, err := repository.GetReportWithResults(ctx, report.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("combined read accepted corrupt execution marker: %v", err)
	}
}
