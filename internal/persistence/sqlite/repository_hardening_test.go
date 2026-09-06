package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRepositoryRunUpdatesAreAppendOnlyAndRecoverable(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	createRunGraph(t, repository, fixture)
	starting := transitionRun(t, repository, fixture.run, domain.RunStarting)
	running := transitionRun(t, repository, starting, domain.RunRunning)

	for revision, want := range map[uint64]domain.Run{1: fixture.run, 2: starting, 3: running} {
		got, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, revision)
		if err != nil {
			t.Fatalf("GetRunRevision(%d) error = %v", revision, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetRunRevision(%d) = %#v, want %#v", revision, got, want)
		}
	}
	if err := repository.UpdateRun(context.Background(), 1, starting); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateRun() error = %v, want ErrConflict", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM execution_run_revisions WHERE run_id = ?`, fixture.run.Meta().ID); got != 3 {
		t.Fatalf("stored run revision count = %d, want 3", got)
	}
	if got := queryInt(t, db, `SELECT current_revision FROM execution_runs WHERE id = ?`, fixture.run.Meta().ID); got != 3 {
		t.Fatalf("current run revision = %d, want 3", got)
	}
}

func TestRepositoryRunRevisionRowsAreDatabaseImmutable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		statement string
	}{
		{"update", `UPDATE execution_run_revisions SET status = 'queued' WHERE run_id = ? AND revision = 1`},
		{"delete", `DELETE FROM execution_run_revisions WHERE run_id = ? AND revision = 1`},
		{"gap insert", `
			INSERT INTO execution_run_revisions(
				run_id, schema_version, revision, created_at, updated_at,
				plan_id, plan_revision, status, snapshot_json, document_json
			)
			SELECT run_id, schema_version, 4, created_at, updated_at,
			       plan_id, plan_revision, status, snapshot_json,
			       json_set(document_json, '$.revision', 4)
			FROM execution_run_revisions WHERE run_id = ? AND revision = 1
		`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			createRunGraph(t, repository, fixture)
			transitionRun(t, repository, fixture.run, domain.RunStarting)
			closeForTamper(t, repository)

			db := openDatabase(t, path)
			defer db.Close()
			if _, err := db.Exec(test.statement, fixture.run.Meta().ID); err == nil {
				t.Fatalf("%s of execution_run_revisions unexpectedly succeeded", test.name)
			}
			if got := queryInt(t, db, `SELECT COUNT(*) FROM execution_run_revisions WHERE run_id = ?`, fixture.run.Meta().ID); got != 2 {
				t.Fatalf("run revision count after rejected %s = %d, want 2", test.name, got)
			}
		})
	}
}

func TestRepositoryDetectsRunRevisionHistoryGaps(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	createRunGraph(t, repository, fixture)
	starting := transitionRun(t, repository, fixture.run, domain.RunStarting)
	transitionRun(t, repository, starting, domain.RunRunning)

	db := openDatabase(t, path)
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS trg_execution_run_revisions_no_delete`); err != nil {
		db.Close()
		t.Fatalf("drop immutability trigger for tamper: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM execution_run_revisions WHERE run_id = ? AND revision = 2`, fixture.run.Meta().ID); err != nil {
		db.Close()
		t.Fatalf("delete middle run revision for tamper: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetRun() with history gap error = %v, want ErrCorrupt", err)
	}
	if _, err := repository.ListRuns(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("ListRuns() with history gap error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryConcurrentRunCASReturnsStableConflict(t *testing.T) {
	path, first, fixture := openHardeningRepository(t)
	defer first.Close()
	second := reopenHardeningRepository(t, path)
	defer second.Close()
	createRunGraph(t, first, fixture)

	for index := 0; index < 8; index++ {
		run := fixture.run
		if index != 0 {
			var err error
			run, err = domain.NewRun(
				entityMeta(fmt.Sprintf("10000000-0000-4000-8000-%012d", 100+index), 1),
				fixture.plan.ID,
				fixture.run.Snapshot(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := first.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
		}
		starting, err := run.Transition(domain.RunStarting, repositoryEpoch.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, repository := range []*persistence.Repository{first, second} {
			go func(repository *persistence.Repository) {
				<-start
				results <- repository.UpdateRun(context.Background(), 1, starting)
			}(repository)
		}
		close(start)
		firstErr, secondErr := <-results, <-results
		if !((firstErr == nil && errors.Is(secondErr, persistence.ErrConflict)) ||
			(secondErr == nil && errors.Is(firstErr, persistence.ErrConflict))) {
			t.Fatalf("concurrent UpdateRun() errors = (%v, %v), want one success and one ErrConflict", firstErr, secondErr)
		}
	}
}

func TestRepositoryRejectsResultAndEvidenceOutsideExecutionStates(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)

	result := fixture.result
	result.EvidenceIDs = nil
	if err := repository.CreateEvidence(ctx, fixture.evidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(queued) error = %v, want ErrConflict", err)
	}
	if err := repository.AppendResult(ctx, result); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(queued) error = %v, want ErrConflict", err)
	}

	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning, domain.RunCompleted)
	evidence := fixture.evidence
	evidence.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000031", 1)
	result.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000032", 1)
	if err := repository.CreateEvidence(ctx, evidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(%s) error = %v, want ErrConflict", run.Status(), err)
	}
	if err := repository.AppendResult(ctx, result); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(%s) error = %v, want ErrConflict", run.Status(), err)
	}
}

func TestRepositoryReportSealsFinalResultAndEvidenceCollections(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence(running) error = %v", err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatalf("AppendResult(running) error = %v", err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}
	storedEvidence, err := repository.GetEvidence(ctx, fixture.evidence.ID)
	if err != nil {
		t.Fatalf("GetEvidence() error = %v", err)
	}
	assertRoundTrip(t, "evidence", fixture.evidence, storedEvidence)
	listedEvidence, err := repository.ListEvidence(ctx, fixture.run.Meta().ID)
	if err != nil || len(listedEvidence) != 1 {
		t.Fatalf("ListEvidence() = %#v, %v; want one evidence row", listedEvidence, err)
	}
	assertRoundTrip(t, "listed evidence", fixture.evidence, listedEvidence[0])
	storedResult, err := repository.GetResult(ctx, fixture.result.ID)
	if err != nil {
		t.Fatalf("GetResult() error = %v", err)
	}
	assertRoundTrip(t, "result", fixture.result, storedResult)
	listedResults, err := repository.ListResults(ctx, fixture.run.Meta().ID)
	if err != nil || len(listedResults) != 1 {
		t.Fatalf("ListResults() = %#v, %v; want one result row", listedResults, err)
	}
	assertRoundTrip(t, "listed result", fixture.result, listedResults[0])
	listedReports, err := repository.ListReportsForRuns(ctx, []string{fixture.run.Meta().ID})
	if err != nil || len(listedReports) != 1 {
		t.Fatalf("ListReportsForRuns() = %#v, %v; want one report", listedReports, err)
	}
	assertRoundTrip(t, "report for run", report, listedReports[0])

	extraEvidence := fixture.evidence
	extraEvidence.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000033", 1)
	extraResult := fixture.result
	extraResult.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000034", 1)
	extraResult.RequestID = "request-after-report"
	extraResult.EvidenceIDs = nil
	if err := repository.CreateEvidence(ctx, extraEvidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(after report) error = %v, want ErrConflict", err)
	}
	if err := repository.AppendResult(ctx, extraResult); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(after report) error = %v, want ErrConflict", err)
	}
}

func TestRepositoryReportSealsRequestRowsBesideFinalCaseSummaries(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatal(err)
	}
	requestResult := fixture.result
	requestResult.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000035", 1)
	requestResult.RequestID = "request-1"
	requestResult.CaseID = ""
	requestResult.EvidenceIDs = nil
	if err := repository.AppendResult(ctx, requestResult); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() with request-level rows error = %v", err)
	}
	results, err := repository.ListResults(ctx, fixture.run.Meta().ID)
	if err != nil {
		t.Fatalf("ListResults() after report error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("sealed result row count = %d, want final summary plus request row", len(results))
	}
}

func TestRepositoryReportMatchesCanonicalEmptyResultCollections(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	result := fixture.result
	result.Metrics = map[string]float64{}
	result.EvidenceIDs = []string{}
	if err := repository.AppendResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	report.CaseResults = []domain.Result{result}
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() with canonically empty result collections error = %v", err)
	}
}

func TestRepositoryUpdateRunPersistsStructuredFailure(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)

	previous := run.Meta().Revision
	failed, err := run.Fail(domain.RunFailure{
		Phase: "execute", ErrorCode: "run_execution_failed",
	}, repositoryEpoch.Add(time.Duration(previous)*time.Minute))
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if err := repository.UpdateRun(context.Background(), previous, failed); err != nil {
		t.Fatalf("UpdateRun(failed) error = %v", err)
	}
	stored, err := repository.GetRun(context.Background(), failed.Meta().ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	assertRoundTrip(t, "failed run", failed, stored)
}

func TestRepositoryOperationalReadsPreserveCancellation(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, read := range map[string]func() error{
		"get run": func() error {
			_, err := repository.GetRun(ctx, "10000000-0000-4000-8000-000000000008")
			return err
		},
		"list runs": func() error {
			_, err := repository.ListRuns(ctx)
			return err
		},
		"get evidence": func() error {
			_, err := repository.GetEvidence(ctx, "10000000-0000-4000-8000-000000000009")
			return err
		},
		"list evidence": func() error {
			_, err := repository.ListEvidence(ctx, "10000000-0000-4000-8000-000000000008")
			return err
		},
		"get result": func() error {
			_, err := repository.GetResult(ctx, "10000000-0000-4000-8000-00000000000a")
			return err
		},
		"list results": func() error {
			_, err := repository.ListResults(ctx, "10000000-0000-4000-8000-000000000008")
			return err
		},
		"list reports": func() error {
			_, err := repository.ListReports(ctx)
			return err
		},
		"list reports for runs": func() error {
			_, err := repository.ListReportsForRuns(ctx, []string{"10000000-0000-4000-8000-000000000008"})
			return err
		},
		"list comparisons": func() error {
			_, err := repository.ListComparisons(ctx)
			return err
		},
	} {
		if err := read(); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s error = %v, want context.Canceled", name, err)
		}
	}
}

func TestRepositoryRejectsCorruptOperationalRelations(t *testing.T) {
	t.Parallel()

	t.Run("deleted result evidence", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunningOutputs(t, repository, fixture)
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `DELETE FROM evidence WHERE id = ?`, fixture.evidence.ID)
		assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
			_, err := repository.GetResult(context.Background(), fixture.result.ID)
			return err
		})
	})

	t.Run("result case outside run snapshot", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunningOutputs(t, repository, fixture)
		closeForTamper(t, repository)
		outsideCase := "10000000-0000-4000-8000-000000000099"
		tamperWithoutForeignKeys(t, path, `
			UPDATE case_results
			SET case_id = ?, document_json = json_set(document_json, '$.case_id', ?)
			WHERE id = ?
		`, outsideCase, outsideCase, fixture.result.ID)
		assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
			_, err := repository.GetResult(context.Background(), fixture.result.ID)
			return err
		})
	})
}

func TestRepositoryReportWriteFailureRollsBackSealAndAttachments(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	duplicatePath := report.Attachments[0]
	duplicatePath.ArtifactID = "10000000-0000-4000-8000-000000000038"
	report.Attachments = append(report.Attachments, duplicatePath)
	if err := report.Validate(); err != nil {
		t.Fatalf("duplicate-path report fixture is invalid: %v", err)
	}
	if err := repository.CreateReport(ctx, report); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateReport() error = %v, want ErrConflict", err)
	}
	if _, err := repository.GetReport(ctx, report.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetReport() after rollback error = %v, want ErrNotFound", err)
	}
	closeForTamper(t, repository)
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT sealed FROM execution_runs WHERE id = ?`, fixture.run.Meta().ID); got != 0 {
		t.Fatalf("run seal after rollback = %d, want 0", got)
	}
	for _, table := range []string{"reports", "artifacts", "report_attachments"} {
		if got := queryInt(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Fatalf("%s row count after rollback = %d, want 0", table, got)
		}
	}
}
