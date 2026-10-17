package sqlite_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const jobListSoftBytes = 1 << 20

type jobListBudgetFixture struct {
	repository *persistence.Repository
	db         *sql.DB
	query      jobhost.JobListQuery
	prepared   quicktest.PreparedPerformance
}

type jobListPageExpectation struct {
	ids    []int
	cursor string
	more   bool
}

func newJobListBudgetFixture(t *testing.T) jobListBudgetFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job-list-budget.db")
	options := persistence.MigrateOptions{AppVersion: "job-list-budget"}
	if err := persistence.Migrate(t.Context(), path, options); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	t.Cleanup(func() { _ = repository.Close() })
	db := openDatabase(t, path)
	t.Cleanup(func() { _ = db.Close() })
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.com", APIKey: "job-list-test-key", ModelID: "x",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 32, OutputTokens: 2,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	return jobListBudgetFixture{
		repository: repository, db: db, prepared: prepared,
		query: jobhost.JobListQuery{
			InstallationID: "75000000-0000-4000-8000-000000000001",
			Target: jobcontract.ExecutionTarget{
				Kind: jobcontract.TargetRemote, NodeID: "75000000-0000-4000-8000-000000000002",
			},
			Limit: 25,
		},
	}
}

func (fixture jobListBudgetFixture) spec(t *testing.T, index, size int) jobcontract.JobSpec {
	t.Helper()
	prepared := fixture.prepared
	prepared.ReportID = fmt.Sprintf("76000000-0000-4000-8000-%012d", index)
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: fmt.Sprintf("77000000-0000-4000-8000-%012d", index),
		NodeInstallationID: fixture.query.InstallationID, EngineBuildID: strings.Repeat("a", 64),
		Target: fixture.query.Target, Kind: jobcontract.KindPerformance,
		Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
	}
	if size > 0 {
		document, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		padding := size - len(document) + len(prepared.ModelID)
		if padding < 1 {
			t.Fatal("fixture spec budget is smaller than its fixed fields")
		}
		// UTF-8 content distinguishes SQLite text length from actual JSON bytes.
		prepared.ModelID = strings.Repeat("界", padding/3) + strings.Repeat("x", padding%3)
	}
	return spec
}

func (fixture jobListBudgetFixture) accept(t *testing.T, spec jobcontract.JobSpec, size int) {
	t.Helper()
	if _, created, err := fixture.repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil || !created {
		t.Fatalf("accept fixture job: created=%v error=%v", created, err)
	}
	if size > 0 {
		actual := queryInt(
			t,
			fixture.db,
			`SELECT length(CAST(spec_json AS BLOB)) FROM execution_jobs WHERE id = ?`,
			spec.JobID,
		)
		if actual != size {
			t.Fatalf("stored spec bytes=%d, want %d", actual, size)
		}
	}
}

func (fixture jobListBudgetFixture) requirePage(t *testing.T, query jobhost.JobListQuery, want jobListPageExpectation) {
	t.Helper()
	page, err := fixture.repository.ListJobs(t.Context(), query)
	validPage := err == nil && page.Contract == jobcontract.CurrentContract && page.Jobs != nil
	matchingPage := len(page.Jobs) == len(want.ids) && page.NextCursor == want.cursor && page.HasMore == want.more
	if !validPage || !matchingPage {
		t.Fatalf(
			"page count=%d cursor=%s more=%v error=%v; want count=%d cursor=%s more=%v",
			len(page.Jobs),
			page.NextCursor,
			page.HasMore,
			err,
			len(want.ids),
			want.cursor,
			want.more,
		)
	}
	for position, index := range want.ids {
		want := fmt.Sprintf("77000000-0000-4000-8000-%012d", index)
		if page.Jobs[position].Receipt.JobID != want {
			t.Fatalf(
				"page[%d]=%s, want %s",
				position,
				page.Jobs[position].Receipt.JobID,
				want,
			)
		}
	}
}

func TestJobListSpecBudgetKeepsMixedPagesAndOwnerGaps(t *testing.T) {
	fixture := newJobListBudgetFixture(t)
	sizes := []int{200 << 10, 600 << 10, 700 << 10, 200 << 10, 800 << 10, 100 << 10}
	for position, size := range sizes {
		spec := fixture.spec(t, position+1, size)
		if position == 1 {
			spec.Target.NodeID = "75000000-0000-4000-8000-000000000003"
		}
		fixture.accept(t, spec, size)
	}
	query := fixture.query
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{1, 3}, cursor: "3", more: true})
	query.After = 3
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{4, 5}, cursor: "5", more: true})
	query.After = 5
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{6}, cursor: "6"})
	query.After = 6
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{}, cursor: "6"})
}

func TestJobListSpecBudgetIncludesExactBoundaryAndCountLookahead(t *testing.T) {
	fixture := newJobListBudgetFixture(t)
	for index := range 3 {
		size := jobListSoftBytes / 2
		fixture.accept(t, fixture.spec(t, index+1, size), size)
	}
	fixture.requirePage(t, fixture.query, jobListPageExpectation{ids: []int{1, 2}, cursor: "2", more: true})
	query := fixture.query
	query.Limit = 1
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{1}, cursor: "1", more: true})
	query.After = 2
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{3}, cursor: "3"})
}

func TestJobListSpecBudgetDefersCorruptLookaheadUntilItsPage(t *testing.T) {
	for _, corruption := range []string{"spec", "snapshot", "count_lookahead"} {
		t.Run(corruption, func(t *testing.T) {
			fixture := newJobListBudgetFixture(t)
			for index := range 2 {
				size := 600 << 10
				if corruption == "count_lookahead" {
					size = 64 << 10
				}
				fixture.accept(t, fixture.spec(t, index+1, size), size)
			}
			id := fixture.spec(t, 2, 0).JobID
			statement := `UPDATE execution_jobs SET spec_json = json_set(spec_json, '$.contract', 0) WHERE id = ?`
			if corruption == "snapshot" {
				statement = `UPDATE execution_jobs SET snapshot_json = '{}' WHERE id = ?`
			}
			if _, err := fixture.db.ExecContext(t.Context(), statement, id); err != nil {
				t.Fatal(err)
			}
			query := fixture.query
			if corruption == "count_lookahead" {
				query.Limit = 1
			}
			fixture.requirePage(t, query, jobListPageExpectation{ids: []int{1}, cursor: "1", more: true})
			query.After = 1
			if _, err := fixture.repository.ListJobs(t.Context(), query); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt row's page returned %v", err)
			}
		})
	}
}

func TestJobListSpecBudgetReturnsSingleMaximumSpecWithoutTruncation(t *testing.T) {
	fixture := newJobListBudgetFixture(t)
	spec := fixture.spec(t, 1, jobcontract.MaxSubmissionBytes)
	fixture.accept(t, spec, jobcontract.MaxSubmissionBytes)
	fixture.accept(t, fixture.spec(t, 2, 0), 0)
	fixture.requirePage(t, fixture.query, jobListPageExpectation{ids: []int{1}, cursor: "1", more: true})
	stored, err := fixture.repository.GetJobSpec(t.Context(), spec.JobID)
	if err != nil || stored.Spec.Performance.ModelID != spec.Spec.Performance.ModelID {
		t.Fatal("maximum spec changed during listing", err)
	}
	query := fixture.query
	query.After = 1
	fixture.requirePage(t, query, jobListPageExpectation{ids: []int{2}, cursor: "2"})
}

func TestJobListSpecBudgetRejectsHardOversizeWithoutMutation(t *testing.T) {
	fixture := newJobListBudgetFixture(t)
	spec := fixture.spec(t, 1, 0)
	fixture.accept(t, spec, 0)
	oversized, err := json.Marshal(fixture.spec(t, 1, 16<<20))
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.db.ExecContext(
		t.Context(),
		`UPDATE execution_jobs SET spec_json = ? WHERE id = ?`,
		string(oversized),
		spec.JobID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.ListJobs(t.Context(), fixture.query); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("hard oversized stored spec returned %v", err)
	}
	unchanged := queryInt(
		t,
		fixture.db,
		`SELECT CAST(spec_json AS BLOB) = CAST(? AS BLOB) FROM execution_jobs WHERE id = ?`,
		string(oversized),
		spec.JobID,
	)
	if unchanged != 1 {
		t.Fatal("rejected stored spec was modified")
	}
}

func TestJobListSpecBudgetPreservesSelectedIdentityValidation(t *testing.T) {
	for _, test := range []struct {
		name       string
		assignment string
	}{
		{name: "spec_digest", assignment: `spec_sha256 = '` + strings.Repeat("c", 64) + `'`},
		{name: "revision", assignment: `revision = 2, last_sequence = 2`},
		{
			name:       "origin",
			assignment: `snapshot_json = json_set(snapshot_json, '$.receipt.origin.runner_version', 'changed')`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newJobListBudgetFixture(t)
			spec := fixture.spec(t, 1, 0)
			fixture.accept(t, spec, 0)
			statement := `UPDATE execution_jobs SET ` + test.assignment + ` WHERE id = ?`
			if _, err := fixture.db.ExecContext(t.Context(), statement, spec.JobID); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.repository.ListJobs(t.Context(), fixture.query); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("changed identity returned %v", err)
			}
		})
	}
}
