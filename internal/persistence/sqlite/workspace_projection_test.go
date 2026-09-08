package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

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
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatalf("AppendResult(first) error = %v", err)
	}
	firstRun = transitionRun(t, repository, firstRun, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = domain.RunCompleted
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport(first) error = %v", err)
	}

	secondRun, err := domain.NewRun(
		entityMeta("10000000-0000-4000-8000-000000000041", 1),
		fixture.plan.ID,
		fixture.run.Snapshot(),
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
	if err := repository.AppendResult(ctx, passing); err != nil {
		t.Fatalf("AppendResult(second pass) error = %v", err)
	}
	failing := domain.Result{
		EntityMeta: entityMeta("10000000-0000-4000-8000-000000000044", 1),
		RunID:      secondRun.Meta().ID, SuiteEntryID: fixture.result.SuiteEntryID,
		CaseID: fixture.testCase.ID, RequestID: "request-fail",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true},
		Failure: domain.FailureSemantic, ErrorCode: domain.ErrorCode("semantic_mismatch"),
	}
	if err := repository.AppendResult(ctx, failing); err != nil {
		t.Fatalf("AppendResult(second fail) error = %v", err)
	}
	// Case summaries and request observations are separate views of the same
	// execution. Counting both doubles progress and failure totals.
	summary := failing
	summary.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000045", 1)
	summary.RequestID = ""
	if err := repository.AppendResult(ctx, summary); err != nil {
		t.Fatalf("AppendResult(second summary) error = %v", err)
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
		byID[projection.Run.Meta().ID] = projection
		if projection.PinnedPlan.Name != fixture.plan.Name || projection.PinnedPlan.Revision != fixture.plan.Revision {
			t.Fatalf("pinned plan = %#v, want original revision and label", projection.PinnedPlan)
		}
	}
	first := byID[firstRun.Meta().ID]
	if first.Completed != 1 || first.Passed != 1 || first.Failed != 0 || first.ArtifactCount != 2 || first.Conclusion != workspace.ConclusionPassed {
		t.Fatalf("first projection = %#v", first)
	}
	second := byID[secondRun.Meta().ID]
	if len(second.SuiteResults) != 1 || second.SuiteResults[0].ObservedCases != 1 || second.SuiteResults[0].EntryID != fixture.result.SuiteEntryID {
		t.Fatalf("request observations and summary must count as one Case: %#v", second.SuiteResults)
	}
	if second.Completed != 2 || second.Passed != 1 || second.Failed != 1 || second.ArtifactCount != 1 || second.Conclusion != workspace.ConclusionNone {
		t.Fatalf("second projection = %#v", second)
	}
	if !reflect.DeepEqual(first.Run, firstRun) || !reflect.DeepEqual(second.Run, secondRun) {
		t.Fatalf("projected runs do not match authoritative current runs")
	}
}

func TestWorkspaceRunProjectionsStillValidateUncountedSummaries(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	createRunningOutputs(t, repository, fixture)
	observation := fixture.result
	observation.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000046", 1)
	observation.RequestID = "request-observation"
	if err := repository.AppendResult(context.Background(), observation); err != nil {
		t.Fatal(err)
	}
	closeForTamper(t, repository)
	tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.success.semantic', 'yes') WHERE id = ?`, fixture.result.ID)
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
	if !reflect.DeepEqual(projections[0].PinnedPlan, fixture.plan) {
		t.Fatalf("PinnedPlan = %#v, want snapshot plan %#v", projections[0].PinnedPlan, fixture.plan)
	}
}

func TestWorkspaceRunProjectionsAcceptFlatV2QuickTask(t *testing.T) {
	t.Parallel()
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	const quickRunID = "10000000-0000-4000-8000-000000000047"
	runMeta := entityMeta(quickRunID, 1)
	snapshot := fixture.run.Snapshot()
	authoredSuite := snapshot.Suites[0]
	load := domain.LoadProfile{
		Mode: domain.LoadFixedConcurrency, Concurrency: 1,
		RequestCount: 1, RequestTimeoutMS: authoredSuite.Load.RequestTimeoutMS,
	}
	suite := fixture.suite
	suite.QuickTest = &domain.SuiteQuickTest{
		Description: "Quick", TimeoutMS: load.RequestTimeoutMS,
		Inputs: []domain.SuiteInput{},
	}
	snapshot.SchemaVersion = domain.FlatRunSnapshotSchemaVersion
	snapshot.Plan = domain.EntityRevisionRef{ID: quickRunID, Revision: 1}
	snapshot.PlanDocument = nil
	snapshot.Cases = append([]domain.CaseRevisionRef(nil), authoredSuite.Cases...)
	snapshot.CaseDefinitions = append([]domain.TestCase(nil), authoredSuite.CaseDefinitions...)
	snapshot.Load = load
	snapshot.SLA = authoredSuite.SLA
	snapshot.Suites = nil
	snapshot.QuickTask = &domain.QuickTaskSnapshot{
		Suite:  suite,
		Inputs: map[string]json.RawMessage{},
	}
	run, err := domain.NewRun(runMeta, quickRunID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(context.Background(), run); err != nil {
		t.Fatalf("CreateRun(v2 quick task) error = %v", err)
	}
	projections, err := repository.ListRunProjections(context.Background())
	if err != nil {
		t.Fatalf("ListRunProjections(v2 quick task) error = %v", err)
	}
	if len(projections) != 1 || !reflect.DeepEqual(projections[0].PinnedPlan, domain.Plan{}) {
		t.Fatalf("quick task projections = %#v", projections)
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
				tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.success.semantic', 'yes') WHERE id = ?`, fixture.result.ID)
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
