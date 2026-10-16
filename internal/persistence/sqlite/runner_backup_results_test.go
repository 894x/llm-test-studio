package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type runnerBackupResultQueries struct {
	relationQueryer
	ownerReads         int
	pages              int
	evidenceReads      int
	onOwner            func()
	onPage             func(int)
	onEvidence         func()
	failOwner          bool
	aliasFrom, aliasTo string
}

func (queryer *runnerBackupResultQueries) QueryRowContext(
	ctx context.Context,
	query string,
	args ...any,
) *sql.Row {
	if strings.Contains(query, "FROM case_results WHERE id") && len(args) == 1 {
		if args[0] == queryer.aliasFrom {
			args = []any{queryer.aliasTo}
		}
	}
	if strings.Contains(query, "FROM execution_runs AS root") {
		queryer.ownerReads++
		if queryer.onOwner != nil {
			queryer.onOwner()
		}
		if queryer.failOwner {
			query = `SELECT private_backup_database_error FROM unknown_backup_table`
		}
	}
	if strings.Contains(query, "FROM evidence WHERE id") {
		queryer.evidenceReads++
		if queryer.onEvidence != nil {
			queryer.onEvidence()
		}
	}
	return queryer.relationQueryer.QueryRowContext(ctx, query, args...)
}

func (queryer *runnerBackupResultQueries) QueryContext(
	ctx context.Context,
	query string,
	args ...any,
) (*sql.Rows, error) {
	if strings.Contains(query, "FROM case_results") {
		queryer.pages++
		if queryer.onPage != nil {
			queryer.onPage(queryer.pages)
		}
	}
	return queryer.relationQueryer.QueryContext(ctx, query, args...)
}

func TestRunnerBackupCaseResultsReadsEachLargeOwnerOnceAcrossPages(t *testing.T) {
	_, repository, runs := runnerBackupResultsFixture(t, 129, 129)
	tx, err := repository.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	queryer := &runnerBackupResultQueries{relationQueryer: tx}
	if err := validateRunnerBackupCaseResults(t.Context(), queryer); err != nil {
		t.Fatal(err)
	}
	if queryer.ownerReads != len(runs) || queryer.pages != 3 {
		t.Fatalf(
			"owner reads=%d, pages=%d; want %d owners and 3 bounded pages",
			queryer.ownerReads,
			queryer.pages,
			len(runs),
		)
	}
}

func TestRunnerBackupCaseResultsRejectsPointLookupIdentityAliases(t *testing.T) {
	for _, alias := range []string{runnerBackupResultID(0, 128), runnerBackupResultID(1, 1)} {
		t.Run(alias, func(t *testing.T) {
			_, repository, _ := runnerBackupResultsFixture(t, 129, 1)
			tx, err := repository.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			queryer := &runnerBackupResultQueries{
				relationQueryer: tx,
				aliasFrom:       runnerBackupResultID(0, 129),
				aliasTo:         alias,
			}
			if err := validateRunnerBackupCaseResults(t.Context(), queryer); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("point lookup skipped or rebound the scanned result: %v", err)
			}
			if queryer.pages != 2 {
				t.Fatalf("alias did not exercise the second page: pages=%d", queryer.pages)
			}
		})
	}
}

func TestRunnerBackupCaseResultsBoundsEvidenceReuseToEachPage(t *testing.T) {
	_, repository, runs := runnerBackupResultsFixture(t, 257)
	evidence := runnerBackupResultEvidence(t, repository, runs[0])
	for index := 1; index <= 257; index++ {
		runnerBackupRewriteResult(
			t,
			repository,
			runnerBackupResultID(0, index),
			func(result *domain.Result) { result.EvidenceIDs = []string{evidence.ID} },
		)
	}
	tx, err := repository.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	queryer := &runnerBackupResultQueries{relationQueryer: tx}
	if err := validateRunnerBackupCaseResults(t.Context(), queryer); err != nil {
		t.Fatal(err)
	}
	if queryer.ownerReads != 1 || queryer.evidenceReads != 3 || queryer.pages != 3 {
		t.Fatalf(
			"owner=%d evidence=%d pages=%d; want 1 owner and 3 independent evidence pages",
			queryer.ownerReads,
			queryer.evidenceReads,
			queryer.pages,
		)
	}
}

func TestRunnerBackupCaseResultsRejectsBadReferencesBeyondFirstPage(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *domain.Result, []domain.Run, *Repository)
	}{
		{name: "orphan owner", mutate: func(t *testing.T, result *domain.Result, _ []domain.Run, repository *Repository) {
			if _, err := repository.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			result.RunID = "39000000-0000-4000-8000-000000000001"
		}},
		{name: "unknown case", mutate: func(_ *testing.T, result *domain.Result, _ []domain.Run, _ *Repository) {
			result.CaseID = "39000000-0000-4000-8000-000000000002"
		}},
		{name: "summary case", mutate: func(_ *testing.T, result *domain.Result, _ []domain.Run, _ *Repository) {
			result.RequestID = ""
			result.CaseID = "39000000-0000-4000-8000-000000000002"
		}},
		{name: "entry marker", mutate: func(_ *testing.T, result *domain.Result, _ []domain.Run, _ *Repository) {
			result.CaseID, result.RequestID = "", ""
			result.EntryStatus = domain.EntryExecutionCompleted
			result.EntryID = "39000000-0000-4000-8000-000000000003"
		}},
		{name: "missing evidence", mutate: func(_ *testing.T, result *domain.Result, _ []domain.Run, _ *Repository) {
			result.EvidenceIDs = []string{"39000000-0000-4000-8000-000000000004"}
		}},
		{name: "foreign evidence", mutate: func(t *testing.T, result *domain.Result, runs []domain.Run, repository *Repository) {
			evidence := runnerBackupResultEvidence(t, repository, runs[1])
			result.EvidenceIDs = []string{evidence.ID}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, repository, runs := runnerBackupResultsFixture(t, 129, 1)
			runnerBackupRewriteResult(
				t,
				repository,
				runnerBackupResultID(0, 129),
				func(result *domain.Result) {
					test.mutate(
						t,
						result,
						runs,
						repository,
					)
				},
			)
			tx, err := repository.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			queryer := &runnerBackupResultQueries{relationQueryer: tx}
			if err := validateRunnerBackupCaseResults(t.Context(), queryer); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("broken reference accepted or misclassified: %v", err)
			}
			if queryer.pages != 2 {
				t.Fatalf("damaged result was not checked beyond the first page: pages=%d", queryer.pages)
			}
		})
	}
}

func TestRunnerBackupCaseResultsPreservesOwnerErrorAndCancellationClasses(t *testing.T) {
	stages := []string{"owner error", "owner cancel", "evidence cancel", "second page cancel", "already canceled"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			_, repository, runs := runnerBackupResultsFixture(t, 129)
			evidence := runnerBackupResultEvidence(t, repository, runs[0])
			runnerBackupRewriteResult(
				t,
				repository,
				runnerBackupResultID(0, 1),
				func(result *domain.Result) { result.EvidenceIDs = []string{evidence.ID} },
			)
			tx, err := repository.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			queryer := &runnerBackupResultQueries{relationQueryer: tx}
			want := error(context.Canceled)
			switch stage {
			case "owner error":
				queryer.failOwner = true
				want = ErrCorrupt
			case "owner cancel":
				queryer.onOwner = cancel
			case "evidence cancel":
				queryer.onEvidence = cancel
			case "second page cancel":
				queryer.onPage = func(page int) {
					if page == 2 {
						cancel()
					}
				}
			case "already canceled":
				cancel()
			}
			err = validateRunnerBackupCaseResults(ctx, queryer)
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private_backup_database_error") {
				t.Fatalf("error classification or safe owner error lost: %v", err)
			}
		})
	}
}

func TestRunnerBackupCaseResultsRoundTripLeavesStoppedSourceUnchanged(t *testing.T) {
	path, repository, _ := runnerBackupResultsFixture(t, 129, 129)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	// All writers are closed before hashing; no live WAL checkpoint is compared.
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := VerifyRunnerDatabase(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := BackupRunnerDatabase(t.Context(), path, filepath.Join(filepath.Dir(path), "backup.db"))
	if err != nil {
		t.Fatal(err)
	}
	if !sameRunnerBusiness(before, backup) || backup.Rows.Runs != 2 || backup.Rows.CaseResults != 258 {
		t.Fatalf("paginated backup lost typed facts: %+v", backup.Rows)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(original) != sha256.Sum256(after) {
		t.Fatal("read-only result validation changed the stopped source")
	}
}

func runnerBackupResultID(owner, index int) string {
	return fmt.Sprintf("32%06d-0000-4000-8000-%012d", owner, index)
}

func runnerBackupResultEvidence(t *testing.T, repository *Repository, run domain.Run) domain.Evidence {
	t.Helper()
	evidence := domain.Evidence{
		EntityMeta: readViewMeta("34000000-0000-4000-8000-000000000001", 1),
		RunID:      run.Meta().ID, RelativePath: "evidence/result.json",
		SHA256: strings.Repeat("a", 64), MediaType: "application/json", Redacted: true,
	}
	if err := repository.CreateEvidence(t.Context(), evidence); err != nil {
		t.Fatal(err)
	}
	return evidence
}

func runnerBackupRewriteResult(
	t *testing.T,
	repository *Repository,
	id string,
	mutate func(*domain.Result),
) {
	t.Helper()
	var row storedResultRow
	if err := row.scan(repository.db.QueryRowContext(t.Context(), `
		SELECT id, schema_version, revision, created_at, updated_at,
		       run_id, entry_id, case_id, request_id, document_json FROM case_results WHERE id=?
	`, id)); err != nil {
		t.Fatal(err)
	}
	result, err := row.decode("")
	if err != nil {
		t.Fatal(err)
	}
	mutate(&result)
	document, err := marshalCanonical(result)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.db.ExecContext(
		t.Context(),
		`UPDATE case_results SET run_id=?, entry_id=?, case_id=?, request_id=?, document_json=? WHERE id=?`,
		result.RunID,
		sql.NullString{String: result.EntryID, Valid: result.EntryID != ""},
		sql.NullString{String: result.CaseID, Valid: result.CaseID != ""},
		sql.NullString{String: result.RequestID, Valid: result.RequestID != ""},
		document,
		id,
	); err != nil {
		t.Fatal(err)
	}
}

func runnerBackupResultsFixture(t *testing.T, counts ...int) (string, *Repository, []domain.Run) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "backup-results-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	base := readViewRunFixture(t)
	snapshot := base.Snapshot()
	definition := snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat]
	snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = []byte(
		strings.Replace(string(definition), "hello", strings.Repeat("x", 64*1024), 1),
	)
	runs := make([]domain.Run, 0, len(counts))
	for owner, count := range counts {
		run, err := domain.NewRun(
			readViewMeta(fmt.Sprintf("31000000-0000-4000-8000-%012d", owner+1), 1),
			base.PlanID(),
			snapshot,
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateRun(t.Context(), run); err != nil {
			t.Fatal(err)
		}
		for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
			next, err := run.Transition(status, run.Meta().UpdatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.UpdateRun(t.Context(), run.Meta().Revision, next); err != nil {
				t.Fatal(err)
			}
			run = next
		}
		results := make([]domain.Result, 0, count)
		// Reverse insertion makes physical row order differ from the keyset order.
		for index := count; index > 0; index-- {
			results = append(results, domain.Result{
				EntityMeta:      readViewMeta(runnerBackupResultID(owner, index), 1),
				RunID:           run.Meta().ID,
				EntryID:         snapshot.Entries[0].EntryID,
				CaseID:          snapshot.Entries[0].Cases[0].CaseID,
				RequestID:       fmt.Sprintf("request-%d", index),
				ExecutionStatus: domain.ExecutionCompleted,
				Verification: testspec.Verdict{
					Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{},
				},
			})
		}
		if err := repository.AppendResults(t.Context(), results...); err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	return path, repository, runs
}
