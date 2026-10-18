package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func addQuickHistoryRun(t *testing.T, repository *persistence.Repository, id string, stamp time.Time) {
	t.Helper()
	fixture := newRepositoryFixture(t)
	snapshot := fixture.run.Snapshot()
	fixture.plan.ID = id
	snapshot.Plan.ID = id
	snapshot.PlanDocument = &fixture.plan
	snapshot.QuickTask = &domain.QuickTaskSnapshot{}
	meta := entityMeta(id, 1)
	meta.CreatedAt, meta.UpdatedAt = stamp, stamp
	run, err := domain.NewRun(meta, id, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
}

func TestQuickHistoryMixedPaginationAndConcurrentInsert(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	expected := []string{}
	// Includes ties, whole seconds and nanoseconds, and exceeds both former caps.
	for index := range 132 {
		id := fmt.Sprintf("70000000-0000-4000-8000-%012d", index)
		stamp := repositoryEpoch.Add(time.Duration(index/4) * time.Second).Add(time.Duration(index % 2))
		kind := "run"
		if index%3 == 0 {
			kind = "performance"
			report := validQuickPerformanceReport(id, stamp.Format(time.RFC3339Nano))
			if err := repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
				t.Fatal(err)
			}
		} else {
			addQuickHistoryRun(t, repository, id, stamp)
		}
		expected = append(expected, stamp.Format(quickhistory.TimeLayout)+"|"+kind+"|"+id)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	service := quickhistory.New(repository)
	after := ""
	actual := []string{}
	for pageIndex := 0; pageIndex < 10; pageIndex++ {
		page, err := service.Page(t.Context(), after)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Runs)+len(page.Reports) > 25 {
			t.Fatal("unbounded page")
		}
		keys := []string{}
		for _, run := range page.Runs {
			keys = append(keys, run.StartedAt.Format(quickhistory.TimeLayout)+"|run|"+run.ID)
		}
		for _, report := range page.Reports {
			keys = append(keys, report.GeneratedAt.Format(quickhistory.TimeLayout)+"|performance|"+report.ID)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
		actual = append(actual, keys...)
		if pageIndex == 0 {
			addQuickHistoryRun(t, repository, "80000000-0000-4000-8000-000000000001", repositoryEpoch.Add(time.Hour))
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == after {
			t.Fatal("cursor did not advance")
		}
		after = page.NextCursor
	}
	if fmt.Sprint(actual) != fmt.Sprint(expected) {
		t.Fatalf("page order/coverage differs: got %d want %d", len(actual), len(expected))
	}
	for _, cursor := range []string{"invalid", "2026-01-01T00:00:00.000000000Z|other|70000000-0000-4000-8000-000000000000"} {
		if _, err := service.Page(t.Context(), cursor); !errors.Is(err, quickhistory.ErrInvalid) {
			t.Fatalf("invalid cursor: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Page(ctx, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestQuickHistorySkipsOffPageResultAndReportBodies(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	createRunningOutputs(t, repository, fixture)
	// A malformed result belonging to a non-quick run must not be read by history.
	tamper(t, path, `UPDATE case_results SET document_json='{}' WHERE run_id=?`, fixture.run.Meta().ID)
	for index := range 30 {
		id := fmt.Sprintf("70000000-0000-4000-8000-%012d", index)
		if err := repository.SaveQuickPerformanceReport(t.Context(), validQuickPerformanceReport(id, repositoryEpoch.Format(time.RFC3339Nano))); err != nil {
			t.Fatal(err)
		}
	}
	tamper(t, path, `DROP TRIGGER quick_performance_reports_no_update`)
	tamper(t, path, `UPDATE quick_performance_reports SET document_json='{}'`)
	addQuickHistoryRun(t, repository, "80000000-0000-4000-8000-000000000001", repositoryEpoch.Add(time.Hour))
	page, err := quickhistory.New(repository).Page(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 1 || len(page.Reports) != 24 {
		t.Fatalf("unexpected page: %d/%d", len(page.Runs), len(page.Reports))
	}
}

func TestQuickHistoryCursorTimeRange(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	id := "70000000-0000-4000-8000-000000000001"
	stamp := time.Unix(0, math.MaxInt64).UTC()
	report := validQuickPerformanceReport(id, stamp.Format(time.RFC3339Nano))
	if err := repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		after string
		count int
	}{
		{"", 1},
		{"9999-01-01T00:00:00.000000000Z|performance|" + id, 1},
		{"1600-01-01T00:00:00.000000000Z|run|" + id, 0},
		{stamp.Format(quickhistory.TimeLayout) + "|run|" + id, 1},
		{stamp.Format(quickhistory.TimeLayout) + "|performance|" + id, 0},
	} {
		page, err := quickhistory.New(repository).Page(t.Context(), test.after)
		if err != nil || len(page.Reports) != test.count {
			t.Fatalf("cursor %q: got %d reports, want %d, err %v", test.after, len(page.Reports), test.count, err)
		}
	}
}
