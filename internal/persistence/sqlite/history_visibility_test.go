package sqlite_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/historyrecycle"
	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestDesktopHistoryVisibilityPreservesLateReportAndRestores(t *testing.T) {
	_, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	run := createRunningOutputs(t, repository, fixture)
	run = transitionRun(t, repository, run, domain.RunCompleted)
	view, err := repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	generic, err := repository.OpenReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer generic.Close()
	target := historyrecycle.Target{Kind: "run", ID: run.Meta().ID}
	entry, err := repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Hour))
	if err != nil || len(entry.ReportIDs) != 0 {
		t.Fatalf("recycle before report hydration: %+v %v", entry, err)
	}
	if err := repository.CreateReport(t.Context(), fixture.report); err != nil {
		t.Fatal(err)
	}
	assertDesktopHistoryCounts(t, view, 0, 0)
	runs, next, err := view.ListRunHistory(t.Context(), "")
	if err != nil || len(runs) != 0 || next != "" {
		t.Fatalf("recycled run history: %v %q %v", runs, next, err)
	}
	refreshed, err := view.ReadRunSummaries(t.Context(), []string{run.Meta().ID})
	if err != nil || len(refreshed) != 0 {
		t.Fatalf("recycled run refresh: %v %v", refreshed, err)
	}
	available, err := view.AvailableReports(t.Context(), []string{fixture.report.ID})
	if err != nil || len(available) != 0 {
		t.Fatalf("late recycled report availability: %v %v", available, err)
	}
	assertDesktopHistoryCounts(t, generic, 1, 1)
	if _, err := view.GetRun(t.Context(), run.Meta().ID); err != nil {
		t.Fatalf("raw run was hidden: %v", err)
	}
	if _, err := view.GetReport(t.Context(), fixture.report.ID); err != nil {
		t.Fatalf("raw report was hidden: %v", err)
	}
	lateTarget := historyrecycle.Target{Kind: "report", ID: fixture.report.ID}
	repeated, err := repository.RecycleHistory(t.Context(), lateTarget, repositoryEpoch.Add(2*time.Hour))
	if err != nil || repeated.ID != entry.ID || repeated.RemovedAt != entry.RemovedAt {
		t.Fatalf("late report changed canonical removal: %+v %v", repeated, err)
	}
	if _, err := repository.RestoreHistory(t.Context(), lateTarget); err != nil {
		t.Fatal(err)
	}
	assertDesktopHistoryCounts(t, view, 1, 1)
	available, err = view.AvailableReports(t.Context(), []string{fixture.report.ID})
	if err != nil || len(available) != 1 || available[0] != fixture.report.ID {
		t.Fatalf("restored report availability: %v %v", available, err)
	}
}

func assertDesktopHistoryCounts(t *testing.T, repository *persistence.Repository, runs int, reports int) {
	t.Helper()
	projections, err := repository.ListRunProjections(t.Context())
	if err != nil || len(projections) != runs {
		t.Fatalf("run projections count = %d, want %d: %v", len(projections), runs, err)
	}
	summaries, err := repository.ListReportProjections(t.Context())
	if err != nil || len(summaries) != reports {
		t.Fatalf("report projections count = %d, want %d: %v", len(summaries), reports, err)
	}
	activity, err := repository.OverviewActivity(t.Context())
	if err != nil || activity.RunCount != uint64(runs) || activity.ReportCount != uint64(reports) {
		t.Fatalf("overview counts = %+v, want %d/%d: %v", activity, runs, reports, err)
	}
}

func TestDesktopQuickHistoryFiltersBeforePaging(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	hidden := map[string]bool{}
	for index := range 40 {
		id := fmt.Sprintf("71000000-0000-4000-8000-%012d", index)
		stamp := repositoryEpoch.Add(time.Duration(index) * time.Second)
		target := historyrecycle.Target{Kind: "run", ID: id}
		if index%2 == 0 {
			target.Kind = "report"
			if err := repository.SaveQuickPerformanceReport(t.Context(),
				validQuickPerformanceReport(id, stamp.Format(time.RFC3339Nano))); err != nil {
				t.Fatal(err)
			}
		} else {
			addQuickHistoryRun(t, repository, id, stamp)
			run, err := repository.GetRun(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			transitionRun(t, repository, run, domain.RunCancelled)
		}
		if index >= 28 {
			hidden[id] = true
			if _, err := repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
		}
	}
	view, err := repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	service := quickhistory.New(view)
	first, err := service.Page(t.Context(), "")
	if err != nil || len(first.Runs)+len(first.Reports) != 25 || first.NextCursor == "" {
		t.Fatalf("first visible page: %+v %v", first, err)
	}
	second, err := service.Page(t.Context(), first.NextCursor)
	if err != nil || len(second.Runs)+len(second.Reports) != 3 || second.NextCursor != "" {
		t.Fatalf("second visible page: %+v %v", second, err)
	}
	for _, page := range []quickhistory.Page{first, second} {
		for _, run := range page.Runs {
			if hidden[run.ID] {
				t.Fatalf("recycled run %s returned", run.ID)
			}
		}
		for _, report := range page.Reports {
			if hidden[report.ID] {
				t.Fatalf("recycled report %s returned", report.ID)
			}
		}
	}
	summaries, err := view.ListRecentQuickPerformanceSummaries(t.Context(), 5)
	if err != nil || len(summaries) != 5 {
		t.Fatalf("visible performance window: %v %v", summaries, err)
	}
	for _, summary := range summaries {
		if hidden[summary.ReportID] {
			t.Fatalf("recycled performance report %s returned", summary.ReportID)
		}
	}
	activity, err := view.OverviewActivity(t.Context())
	if err != nil || activity.RunCount != 14 || activity.ReportCount != 14 {
		t.Fatalf("visible mixed counts: %+v %v", activity, err)
	}
	all, err := repository.ListRecentQuickPerformanceSummaries(t.Context(), 5)
	if err != nil || len(all) != 5 || !hidden[all[0].ReportID] {
		t.Fatalf("generic performance history lost recycled data: %v %v", all, err)
	}
}

func TestDesktopComparisonHistoryHidesRefreshedRecycledGroup(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	group := historyComparison(t, repository, "72000000-0000-4000-8000-000000000001")
	for _, child := range group.Runs() {
		run, err := repository.GetRun(t.Context(), child.RunID)
		if err != nil {
			t.Fatal(err)
		}
		transitionRun(t, repository, run, domain.RunCancelled)
	}
	terminal, err := group.Transition(domain.ComparisonCancelled, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateComparison(t.Context(), group.Meta().Revision, terminal); err != nil {
		t.Fatal(err)
	}
	view, err := repository.OpenDesktopReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	target := historyrecycle.Target{Kind: "comparison", ID: group.Meta().ID}
	if _, err := repository.RecycleHistory(t.Context(), target, repositoryEpoch.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	page, err := view.ComparisonHistory(t.Context(), "", []string{group.Meta().ID})
	if err != nil || len(page.Comparisons) != 0 || page.NextCursor != "" {
		t.Fatalf("recycled comparison refresh: %+v %v", page, err)
	}
	if _, err := view.GetComparison(t.Context(), group.Meta().ID); err != nil {
		t.Fatalf("raw comparison was hidden: %v", err)
	}
	if _, err := repository.RestoreHistory(t.Context(), target); err != nil {
		t.Fatal(err)
	}
	page, err = view.ComparisonHistory(t.Context(), "", nil)
	if err != nil || len(page.Comparisons) != 1 {
		t.Fatalf("restored comparison: %+v %v", page, err)
	}
}
