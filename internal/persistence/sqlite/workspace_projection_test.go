package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/894x/llm-test-studio/internal/testspec"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestWorkspaceListDefersFullCaseDefinitionValidationToRunDetail(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	createRunGraph(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(t, path, `DROP TRIGGER trg_execution_run_revisions_no_update`)
	tamper(t, path, `UPDATE execution_run_revisions
		SET snapshot_json = json_set(snapshot_json, '$.entries[0].case_definitions[0].schema_version', 0),
		    document_json = json_set(document_json, '$.plan_snapshot.entries[0].case_definitions[0].schema_version', 0)
		WHERE run_id = ?`, fixture.run.Meta().ID)
	restoreRunRevisionUpdateTrigger(t, path)
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	projections, err := repository.ListRunProjections(context.Background())
	if err != nil || len(projections) != 1 {
		t.Fatalf("unrequested Case definition blocked the list: %v", err)
	}
	encoded, err := json.Marshal(projections)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"case_definitions", "definitions", "base_url", "credential"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("list projection contains detail field %q", forbidden)
		}
	}
	db := openDatabase(t, path)
	defer db.Close()
	readDocument := func() string {
		t.Helper()
		var document string
		if err := db.QueryRowContext(
			context.Background(),
			`SELECT document_json FROM execution_run_revisions WHERE run_id = ?`,
			fixture.run.Meta().ID,
		).Scan(&document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	before := readDocument()
	if _, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("unsupported Case format accepted by GetRun: %v", err)
	}
	if readDocument() != before {
		t.Fatal("rejected detail read changed stored data")
	}
}

func TestWorkspaceRequestCountsSelectObservationsAcrossAllEntries(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	snapshot := fixture.run.Snapshot()
	second := snapshot.Entries[0]
	second.EntryID = "10000000-0000-4000-8000-000000000091"
	snapshot.Entries = append(snapshot.Entries, second)
	secondPlanEntry := snapshot.PlanDocument.Entries[0]
	secondPlanEntry.EntryID = second.EntryID
	snapshot.PlanDocument.Entries = append(snapshot.PlanDocument.Entries, secondPlanEntry)
	var err error
	fixture.run, err = domain.NewRun(fixture.run.Meta(), fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	createRunGraph(t, repository, fixture)
	transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	request := fixture.result
	request.RequestID, request.EvidenceIDs = "measured-request", []string{}
	warmup := request
	warmup.ID, warmup.RequestID = "10000000-0000-4000-8000-000000000093", "warmup-request"
	warmup.Dimensions = map[string]string{"phase": "warmup"}
	if err := repository.AppendResults(context.Background(), warmup); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResults(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	summary := request
	summary.ID = "10000000-0000-4000-8000-000000000092"
	summary.EntryID, summary.RequestID = second.EntryID, ""
	if err := repository.AppendResults(context.Background(), summary); err != nil {
		t.Fatal(err)
	}
	projections, err := repository.ListRunProjections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 1 || projections[0].Completed != 1 || len(projections[0].EntryResults) != 2 {
		t.Fatalf("case summary was counted again as a request: %+v", projections)
	}
	for _, entry := range projections[0].EntryResults {
		if entry.ObservedCases != 1 {
			t.Fatalf("entry Case progress = %+v", entry)
		}
	}
}

func TestWorkspaceRunProjectionsAggregateManyRunsWithoutLoadingDetails(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)
	createRunGraph(t, repository, fixture)

	firstRun := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence(first) error = %v", err)
	}
	if err := repository.AppendResults(ctx, fixture.result); err != nil {
		t.Fatalf("AppendResults(first) error = %v", err)
	}
	firstRun = transitionRun(t, repository, firstRun, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = domain.RunCompleted
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport(first) error = %v", err)
	}

	secondSnapshot := fixture.run.Snapshot()
	secondLoad := secondSnapshot.Entries[0].Load
	secondLoad.Mode, secondLoad.RequestCount = domain.LoadFixedConcurrency, 2
	secondSnapshot.Entries[0].Load = secondLoad
	secondSnapshot.PlanDocument.Entries[0].Load = secondLoad
	secondRun, err := domain.NewRun(
		entityMeta("10000000-0000-4000-8000-000000000041", 1),
		fixture.plan.ID,
		secondSnapshot,
	)
	if err != nil {
		t.Fatalf("NewRun(second) error = %v", err)
	}
	if err := repository.CreateRun(ctx, secondRun); err != nil {
		t.Fatalf("CreateRun(second) error = %v", err)
	}
	secondRun = transitionRun(t, repository, secondRun, domain.RunStarting, domain.RunRunning)

	secondEvidence := fixture.evidence
	secondEvidence.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000042", 1)
	secondEvidence.RunID = secondRun.Meta().ID
	secondEvidence.RelativePath = "evidence/second.json"
	if err := repository.CreateEvidence(ctx, secondEvidence); err != nil {
		t.Fatalf("CreateEvidence(second) error = %v", err)
	}
	passing := fixture.result
	passing.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000043", 1)
	passing.RunID = secondRun.Meta().ID
	passing.RequestID = "request-pass"
	passing.EvidenceIDs = []string{secondEvidence.ID}
	if err := repository.AppendResults(ctx, passing); err != nil {
		t.Fatalf("AppendResults(second pass) error = %v", err)
	}
	failing := domain.Result{
		EntityMeta: entityMeta("10000000-0000-4000-8000-000000000044", 1),
		RunID:      secondRun.Meta().ID, EntryID: fixture.result.EntryID,
		CaseID: fixture.testCase.ID, RequestID: "request-fail",
		ExecutionStatus: domain.ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictFailed, Assertions: []testspec.AssertionResult{}},
		Failure: domain.FailureSemantic, ErrorCode: domain.ErrorCode("semantic_mismatch"),
	}
	if err := repository.AppendResults(ctx, failing); err != nil {
		t.Fatalf("AppendResults(second fail) error = %v", err)
	}
	// Case summaries and request observations are separate views of the same
	// execution. Counting both doubles progress and failure totals.
	summary := failing
	summary.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000045", 1)
	summary.RequestID = ""
	if err := repository.AppendResults(ctx, summary); err != nil {
		t.Fatalf("AppendResults(second summary) error = %v", err)
	}

	projections, err := repository.ListRunProjections(ctx)
	if err != nil {
		t.Fatalf("ListRunProjections() error = %v", err)
	}
	if len(projections) != 2 {
		t.Fatalf("ListRunProjections() count = %d, want 2", len(projections))
	}
	byID := make(map[string]workspace.RunProjection, len(projections))
	for _, projection := range projections {
		byID[projection.Meta.ID] = projection
		if projection.PlanName != fixture.plan.Name || projection.Plan.Revision != fixture.plan.Revision {
			t.Fatalf("pinned plan = %#v, want original revision and label", projection.Plan)
		}
	}
	first := byID[firstRun.Meta().ID]
	if first.Completed != 1 || first.Passed != 1 || first.Failed != 0 || first.ArtifactCount != 2 || first.Conclusion != workspace.ConclusionPassed {
		t.Fatalf("first projection = %#v", first)
	}
	second := byID[secondRun.Meta().ID]
	if len(second.EntryResults) != 1 || second.EntryResults[0].ObservedCases != 1 || second.EntryResults[0].EntryID != fixture.result.EntryID {
		t.Fatalf("request observations and summary must count as one Case: %#v", second.EntryResults)
	}
	if second.Completed != 2 || second.Passed != 1 || second.Failed != 1 || second.ArtifactCount != 1 || second.Conclusion != workspace.ConclusionNone {
		t.Fatalf("second projection = %#v", second)
	}
	if !reflect.DeepEqual(first.Meta, firstRun.Meta()) || !reflect.DeepEqual(second.Meta, secondRun.Meta()) {
		t.Fatalf("projected runs do not match authoritative current runs")
	}
}

func TestWorkspaceRunProjectionsStillValidateUncountedSummaries(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	createRunningOutputs(t, repository, fixture)
	observation := fixture.result
	observation.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000046", 1)
	observation.RequestID = "request-observation"
	if err := repository.AppendResults(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	closeForTamper(t, repository)
	tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.verification.status', 'unknown') WHERE id = ?`, fixture.result.ID)
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	if _, err := repository.ListRunProjections(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("corrupt summary was ignored: %v", err)
	}
}

func TestWorkspaceRunProjectionsUseV2SnapshotAfterCatalogPlanRowIsDeleted(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	fixture = withCompleteRunSnapshot(t, fixture)
	createRunGraph(t, repository, fixture)
	closeForTamper(t, repository)
	repository = retireAndReopenOperationalRepository(t, path)
	defer repository.Close()

	projections, err := repository.ListRunProjections(context.Background())
	if err != nil {
		t.Fatalf("ListRunProjections() after catalog plan deletion error = %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("ListRunProjections() count = %d, want 1", len(projections))
	}
	if projections[0].PlanName != fixture.plan.Name || projections[0].Plan.ID != fixture.plan.ID || projections[0].Plan.Revision != fixture.plan.Revision {
		t.Fatalf("PinnedPlan = %#v, want snapshot plan %#v", projections[0].Plan, fixture.plan)
	}
}

func TestWorkspaceRunProjectionsUseCurrentSnapshotForQuickTask(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	snapshot := fixture.run.Snapshot()
	fixture.plan.ID = fixture.run.Meta().ID
	snapshot.Plan.ID = fixture.plan.ID
	snapshot.PlanDocument = &fixture.plan
	snapshot.QuickTask = &domain.QuickTaskSnapshot{}
	run, err := domain.NewRun(fixture.run.Meta(), fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(context.Background(), run); err != nil {
		t.Fatal(err)
	}
	projections, err := repository.ListRunProjections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projections) != 1 || projections[0].PlanName != fixture.plan.Name || projections[0].Plan.ID != fixture.plan.ID || projections[0].Plan.Revision != fixture.plan.Revision {
		t.Fatalf("quick projection %#v", projections)
	}
}

func TestWorkspaceRunProjectionsRejectSummaryCriticalCorruption(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, repositoryFixture)
	}{
		{
			name: "result owner document",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.run_id', ?) WHERE id = ?`,
					"10000000-0000-4000-8000-000000000099", fixture.result.ID)
			},
		},
		{
			name: "result success dimension type",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.verification.status', 'unknown') WHERE id = ?`, fixture.result.ID)
			},
		},
		{
			name: "result case outside frozen entry",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				const foreignCaseID = "10000000-0000-4000-8000-000000000099"
				tamper(t, path, `
					UPDATE case_results SET case_id = ?,
					  document_json = json_set(document_json, '$.case_id', ?) WHERE id = ?
				`, foreignCaseID, foreignCaseID, fixture.result.ID)
			},
		},
		{
			name: "result entry outside frozen run",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				const foreignEntryID = "10000000-0000-4000-8000-000000000099"
				tamper(t, path, `
					UPDATE case_results SET entry_id = ?,
					  document_json = json_set(document_json, '$.entry_id', ?) WHERE id = ?
				`, foreignEntryID, foreignEntryID, fixture.result.ID)
			},
		},
		{
			name: "entry marker with request identity",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `
					UPDATE case_results
					SET document_json = json_set(document_json, '$.entry_status', 'completed') WHERE id = ?
				`, fixture.result.ID)
			},
		},
		{
			name: "evidence owner document",
			mutate: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `UPDATE evidence SET document_json = json_set(document_json, '$.run_id', ?) WHERE id = ?`,
					"10000000-0000-4000-8000-000000000099", fixture.evidence.ID)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			createRunningOutputs(t, repository, fixture)
			closeForTamper(t, repository)
			test.mutate(t, path, fixture)
			repository = reopenHardeningRepository(t, path)
			defer repository.Close()

			if test.name == "evidence owner document" {
				if _, err := repository.ListRunProjections(context.Background()); err != nil {
					t.Fatalf("unrequested evidence blocked run summaries: %v", err)
				}
				if _, err := repository.ListEvidence(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("evidence read error = %v, want ErrCorrupt", err)
				}
				return
			}
			if _, err := repository.ListRunProjections(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("ListRunProjections() error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestWorkspaceRunProjectionsRejectCorruptSealedReportConclusion(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	run := createRunningOutputs(t, repository, fixture)
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = domain.RunCompleted
	if err := repository.CreateReport(context.Background(), report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}
	closeForTamper(t, repository)
	tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.conclusion.passed', 'yes') WHERE id = ?`, report.ID)
	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListRunProjections(context.Background())
		return err
	})
}

func TestWorkspaceRunProjectionsUseFailedSealedReportConclusion(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	run := createRunningOutputs(t, repository, fixture)
	run = transitionRun(t, repository, run, domain.RunFailed)
	report := fixture.report
	report.RunStatus = domain.RunFailed
	report.Conclusion = domain.ReportConclusion{Passed: false, Verdict: "failed", Issues: []string{"run failed"}}
	if err := repository.CreateReport(context.Background(), report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}

	projections, err := repository.ListRunProjections(context.Background())
	if err != nil {
		t.Fatalf("ListRunProjections() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Conclusion != workspace.ConclusionFailed {
		t.Fatalf("failed report projection = %#v", projections)
	}
}

func TestWorkspaceRunProjectionsPreserveContextCancellation(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListRunProjections(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRunProjections(cancelled) error = %v, want context.Canceled", err)
	}
}

func withCompleteRunSnapshot(t *testing.T, fixture repositoryFixture) repositoryFixture {
	t.Helper()
	snapshot := fixture.run.Snapshot()
	snapshot.SchemaVersion = domain.CurrentRunSnapshotSchemaVersion
	plan := fixture.plan
	mapping := fixture.mapping
	snapshot.PlanDocument = &plan
	snapshot.Mapping = &mapping
	run, err := domain.NewRun(fixture.run.Meta(), fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatalf("NewRun(v2 workspace fixture) error = %v", err)
	}
	fixture.run = run
	return fixture
}
