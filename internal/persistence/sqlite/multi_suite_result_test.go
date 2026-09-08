package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	reportapp "github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRepositoryStoresSameCaseSummaryForDifferentSuiteEntries(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	base := runWithCompleteSnapshot(t)
	snapshot := base.Snapshot()
	caseRef := snapshot.Suites[0].Cases[0]
	definition := snapshot.Suites[0].CaseDefinitions[0]
	load, sla := snapshot.Suites[0].Load, snapshot.Suites[0].SLA
	suite := domain.Suite{
		EntityMeta: entityMeta("86000000-0000-4000-8000-000000000001", 1),
		Key:        "repeated", Name: "Repeated", Protocol: snapshot.Model.Protocol,
		ModelTarget: snapshot.Channel.UpstreamModelName, Cases: []domain.CaseRevisionRef{caseRef},
	}
	entries := []domain.PlanSuiteEntry{
		{EntryID: "86000000-0000-4000-8000-000000000002", SuiteID: suite.ID, SuiteRevision: suite.Revision, Cases: suite.Cases, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
		{EntryID: "86000000-0000-4000-8000-000000000003", SuiteID: suite.ID, SuiteRevision: suite.Revision, Cases: suite.Cases, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
	}
	plan := *snapshot.PlanDocument
	plan.Suites = entries
	snapshot.PlanDocument = &plan
	snapshot.Suites = []domain.RunSuiteSnapshot{
		{EntryID: entries[0].EntryID, Suite: suite, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{definition}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
		{EntryID: entries[1].EntryID, Suite: suite, Cases: []domain.CaseRevisionRef{caseRef}, CaseDefinitions: []domain.TestCase{definition}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
	}
	run, err := domain.NewRun(base.Meta(), base.PlanID(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repository.CreateRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	starting, err := run.Transition(domain.RunStarting, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(ctx, run.Meta().Revision, starting); err != nil {
		t.Fatal(err)
	}
	running, err := starting.Transition(domain.RunRunning, repositoryEpoch.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(ctx, starting.Meta().Revision, running); err != nil {
		t.Fatal(err)
	}
	for index, entry := range entries {
		result := domain.Result{
			EntityMeta: entityMeta([]string{"86000000-0000-4000-8000-000000000004", "86000000-0000-4000-8000-000000000005"}[index], 1),
			RunID:      running.Meta().ID, SuiteEntryID: entry.EntryID, CaseID: caseRef.CaseID,
			Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		}
		if err := repository.AppendResult(ctx, result); err != nil {
			t.Fatalf("AppendResult(entry %d) error = %v", index, err)
		}
	}
	results, err := repository.ListResults(ctx, running.Meta().ID)
	if err != nil || len(results) != 2 || results[0].SuiteEntryID == results[1].SuiteEntryID {
		t.Fatalf("ListResults() = %#v, %v", results, err)
	}
	marker := domain.Result{
		EntityMeta: entityMeta("86000000-0000-4000-8000-000000000006", 1),
		RunID:      running.Meta().ID, SuiteEntryID: entries[0].EntryID,
		SuiteStatus: domain.SuiteExecutionCompleted,
	}
	if err := repository.AppendResult(ctx, marker); err != nil {
		t.Fatalf("AppendResult(marker) error = %v", err)
	}
	marker.EntityMeta = entityMeta("86000000-0000-4000-8000-000000000007", 1)
	if err := repository.AppendResult(ctx, marker); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(duplicate marker) error = %v, want ErrConflict", err)
	}
	marker.EntityMeta = entityMeta("86000000-0000-4000-8000-000000000008", 1)
	marker.SuiteEntryID = entries[1].EntryID
	if err := repository.AppendResult(ctx, marker); err != nil {
		t.Fatalf("AppendResult(second marker) error = %v", err)
	}
	completed, err := running.Transition(domain.RunCompleted, repositoryEpoch.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateRun(ctx, running.Meta().Revision, completed); err != nil {
		t.Fatal(err)
	}
	generator, err := reportapp.NewGenerator(reportapp.GeneratorDependencies{
		Repository: repository,
		Clock:      multiSuiteReportClock{now: repositoryEpoch.Add(4 * time.Minute)},
		IDFactory: func(time.Time) (string, error) {
			return "86000000-0000-4000-8000-000000000009", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(ctx, completed.Meta().ID); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	report, err := repository.GetReport(ctx, "86000000-0000-4000-8000-000000000009")
	if err != nil || len(report.SuiteReports) != 2 || report.SuiteReports[0].SuiteEntryID != entries[0].EntryID {
		t.Fatalf("GetReport() = %#v, %v", report, err)
	}
	projections, err := repository.ListReportProjections(ctx)
	if err != nil || len(projections) != 1 || projections[0].CaseCount != 2 {
		t.Fatalf("ListReportProjections() = %#v, %v", projections, err)
	}
	runProjections, err := repository.ListRunProjections(ctx)
	if err != nil || len(runProjections) != 1 || runProjections[0].Completed != 2 {
		t.Fatalf("ListRunProjections() = %#v, %v", runProjections, err)
	}
}

type multiSuiteReportClock struct{ now time.Time }

func (clock multiSuiteReportClock) Now() time.Time { return clock.now }
