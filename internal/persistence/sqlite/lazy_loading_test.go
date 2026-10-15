package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestReportDetailsAcceptStructuredRunFailure(t *testing.T) {
	t.Parallel()
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	run := createRunningOutputs(t, repository, fixture)
	failed, err := run.Fail(domain.RunFailure{
		Phase: "execute", ErrorCode: "run_execution_failed",
	}, repositoryEpoch.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repository.UpdateRun(ctx, run.Meta().Revision, failed); err != nil {
		t.Fatal(err)
	}
	report := fixture.report
	report.RunStatus = failed.Status()
	report.Conclusion = domain.ReportConclusion{
		Passed: false, Verdict: "fail", Issues: []string{"run_execution_failed"},
	}
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport(failed run): %v", err)
	}
	stored, err := repository.GetReport(ctx, report.ID)
	if err != nil {
		t.Fatalf("GetReport(failed run): %v", err)
	}
	assertRoundTrip(t, "failed run report", report, stored)
}

func TestReportDetailsRejectGenerationBeforeRunTermination(t *testing.T) {
	t.Parallel()
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	storeFixtureReport(t, repository, fixture)
	at := repositoryEpoch.Add(time.Minute).Format(time.RFC3339Nano)
	tamper(t, path, `UPDATE reports
		SET generated_at = ?, document_json = json_set(document_json, '$.generated_at', ?)
		WHERE id = ?`, at, at, fixture.report.ID)
	if _, err := repository.GetReport(context.Background(), fixture.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetReport() = %v, want ErrCorrupt", err)
	}
}

func TestStartupDefersUnrelatedArchivedDocumentValidation(t *testing.T) {
	t.Parallel()
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	// Preserve the current database schema but reject an unsupported report format
	// only when that particular report is requested.
	tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.schema_version', 0) WHERE id = ?`, fixture.report.ID)
	db := openDatabase(t, path)
	defer db.Close()
	readDocument := func() string {
		t.Helper()
		var document string
		if err := db.QueryRow(`SELECT document_json FROM reports WHERE id = ?`, fixture.report.ID).Scan(&document); err != nil {
			t.Fatal(err)
		}
		return document
	}
	before := readDocument()
	ctx := context.Background()
	if err := persistence.Migrate(ctx, path, persistence.MigrateOptions{AppVersion: "lazy-loading-test"}); err != nil {
		t.Fatalf("Migrate() with unrelated archived document: %v", err)
	}
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	interrupted, err := repository.ListInterruptedRuns(ctx)
	if err != nil || len(interrupted) != 0 {
		t.Fatalf("ListInterruptedRuns() = %v, %v; want no interrupted runs", interrupted, err)
	}
	if _, err := repository.GetReport(ctx, fixture.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetReport(unsupported format) = %v, want ErrCorrupt", err)
	}
	if after := readDocument(); after != before {
		t.Fatal("startup or rejected detail read changed the archived document")
	}
}

func TestWholeDatabaseIntegrityCheckIsExplicit(t *testing.T) {
	t.Parallel()
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamperWithoutForeignKeys(t, path,
		`UPDATE evidence SET run_id = 'ffffffff-ffff-4fff-8fff-ffffffffffff' WHERE id = ?`,
		fixture.evidence.ID)
	ctx := context.Background()
	if err := persistence.Migrate(ctx, path, persistence.MigrateOptions{AppVersion: "lazy-loading-test"}); err != nil {
		t.Fatalf("schema validation scanned unrelated archived relations: %v", err)
	}
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	if _, err := repository.ListInterruptedRuns(ctx); err != nil {
		t.Fatalf("recovery scanned unrelated archived relations: %v", err)
	}
	if err := repository.CheckIntegrity(ctx); err == nil {
		t.Fatal("explicit integrity check accepted an orphaned evidence record")
	}
	if _, err := repository.GetReport(ctx, fixture.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetReport(orphaned evidence) = %v, want ErrCorrupt", err)
	}
}
