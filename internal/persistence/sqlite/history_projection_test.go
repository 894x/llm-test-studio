package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/overview"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func historyComparison(t *testing.T, repository *persistence.Repository, comparisonID string) domain.Comparison {
	t.Helper()
	ctx := t.Context()
	base := runWithCompleteSnapshot(t)
	firstSnapshot := base.Snapshot()
	const (
		secondChannelID = "30000000-0000-4000-8000-000000000007"
		secondMappingID = "30000000-0000-4000-8000-000000000008"
		secondRunID     = "30000000-0000-4000-8000-000000000009"
	)
	firstPlan := *firstSnapshot.PlanDocument

	firstSnapshot.PlanDocument = &firstPlan
	firstRun, err := domain.NewRun(base.Meta(), base.PlanID(), firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := firstRun.Snapshot()
	secondSnapshot.Channel = domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: secondChannelID, Revision: 1},
		Name:              "Snapshot-only channel B",
		BaseURL:           "https://snapshot-only-b.example.test/v1",
		Protocol:          domain.ProtocolOpenAIChat,
		UpstreamModelName: "upstream-snapshot-only-b",
	}
	secondMapping := *secondSnapshot.Mapping
	secondMapping.EntityMeta = entityMeta(secondMappingID, 1)
	secondMapping.ChannelID = secondChannelID
	secondMapping.UpstreamModelName = secondSnapshot.Channel.UpstreamModelName
	secondSnapshot.Mapping = &secondMapping
	secondRun, err := domain.NewRun(entityMeta(secondRunID, 1), base.PlanID(), secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.Run{firstRun, secondRun} {
		if err := repository.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun() without catalog rows error = %v", err)
		}
	}
	comparison, err := domain.NewComparison(
		entityMeta(comparisonID, 1),
		firstSnapshot.Plan,
		firstSnapshot.Model.EntityRevisionRef,
		[]domain.ComparisonRunRef{
			{Channel: firstSnapshot.Channel.EntityRevisionRef, RunID: firstRun.Meta().ID},
			{Channel: secondSnapshot.Channel.EntityRevisionRef, RunID: secondRun.Meta().ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateComparison(ctx, comparison); err != nil {
		t.Fatalf("CreateComparison() without catalog rows error = %v", err)
	}
	return comparison
}

func TestRunHistoryPagesAndRefreshAreBounded(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	createRunningOutputs(t, repository, fixture)
	for i := 0; i < 52; i++ {
		addQuickHistoryRun(t, repository, fmt.Sprintf("70000000-0000-4000-8000-%012d", i), repositoryEpoch.Add(time.Hour+time.Duration(i)*time.Second))
	}
	// A malformed old result must not be visited by the first two pages.
	tamper(t, path, `UPDATE case_results SET document_json='{}' WHERE run_id=?`, fixture.run.Meta().ID)
	history := workspace.NewHistory(repository)
	first, err := history.Page(t.Context(), "")
	if err != nil || len(first.Runs) != 25 || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := history.Page(t.Context(), first.NextCursor)
	if err != nil || len(second.Runs) != 25 || second.NextCursor == first.NextCursor {
		t.Fatalf("second: %+v %v", second, err)
	}
	seen := map[string]bool{}
	for _, run := range append(first.Runs, second.Runs...) {
		if seen[run.ID] {
			t.Fatal("duplicate page identity")
		}
		seen[run.ID] = true
	}
	refreshed, err := history.Refresh(t.Context(), []string{first.Runs[0].ID, second.Runs[24].ID})
	if err != nil || len(refreshed.Runs) != 2 {
		t.Fatalf("refresh: %+v %v", refreshed, err)
	}
	if _, err := history.Page(t.Context(), second.NextCursor); err == nil {
		t.Fatal("selected corrupt result was hidden")
	}
	tamper(t, path, `UPDATE execution_runs SET current_revision=999 WHERE id=?`, first.Runs[0].ID)
	if _, err := history.Page(t.Context(), ""); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("missing revision was hidden: %v", err)
	}
}

func TestComparisonHistoryPagesAndRefreshLegacyTerminalGroup(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	group := historyComparison(t, repository, "70000000-0000-4000-8000-000000000001")
	for i := 2; i <= 28; i++ {
		next, err := domain.NewComparison(entityMeta(fmt.Sprintf("70000000-0000-4000-8000-%012d", i), 1), group.Plan(), group.Model(), group.Runs())
		if err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateComparison(t.Context(), next); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ComparisonHistory(t.Context(), "", nil)
	if err != nil || len(first.Comparisons) != 25 || first.NextCursor == "" {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := repository.ComparisonHistory(t.Context(), first.NextCursor, nil)
	if err != nil || len(second.Comparisons) != 3 || second.NextCursor != "" {
		t.Fatalf("second: %+v %v", second, err)
	}
	for _, summary := range first.Comparisons {
		if len(summary.Channels) != 2 || summary.PlanName != "Snapshot-only plan" || summary.Channels[0].Metrics == nil {
			t.Fatalf("summary: %+v", summary)
		}
	}
	for _, child := range group.Runs() {
		run, err := repository.GetRun(t.Context(), child.RunID)
		if err != nil {
			t.Fatal(err)
		}
		transitionRun(t, repository, run, domain.RunCancelled)
	}
	refresher, err := comparisons.NewRefresher(comparisons.RefresherDependencies{Repository: repository, Runs: repository, Clock: historyClock{}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := comparisons.NewHistory(repository, refresher).ComparisonHistory(t.Context(), "", []string{group.Meta().ID})
	if err != nil || len(page.Comparisons) != 26 {
		t.Fatalf("refresh: %+v %v", page, err)
	}
	for _, summary := range page.Comparisons {
		if summary.Status != "cancelled" {
			t.Fatalf("legacy group not settled: %+v", summary)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ComparisonHistory(ctx, "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("context lost: %v", err)
	}
}

type historyClock struct{}

func (historyClock) Now() time.Time { return repositoryEpoch.Add(time.Hour) }

type overviewCatalog struct{}

func (overviewCatalog) Counts(context.Context) (catalog.Counts, error) {
	return catalog.Counts{Models: 3, Plans: 2}, nil
}

func TestOverviewUsesCountsAndBoundedRecentSummaries(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	run := createRunningOutputs(t, repository, fixture)
	transitionRun(t, repository, run, domain.RunCompleted)
	tamper(t, path, `UPDATE case_results SET document_json='{}' WHERE run_id=?`, run.Meta().ID)
	const activeID = "80000000-0000-4000-8000-000000000001"
	addQuickHistoryRun(t, repository, activeID, repositoryEpoch.Add(time.Hour))
	for index := range 8 {
		id := fmt.Sprintf("90000000-0000-4000-8000-%012d", index)
		if err := repository.SaveQuickPerformanceReport(t.Context(), validQuickPerformanceReport(id, repositoryEpoch.Add(time.Duration(index)*time.Second).Format(time.RFC3339Nano))); err != nil {
			t.Fatal(err)
		}
	}
	tamper(t, path, `DROP TRIGGER quick_performance_reports_no_update`)
	tamper(t, path, `UPDATE quick_performance_reports SET document_json='{}'`)
	snapshot, err := overview.New(repository, overviewCatalog{}).Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunCount != 2 || snapshot.ReportCount != 8 || snapshot.Workspace.ActiveRunID != activeID || len(snapshot.Workspace.Runs) != 1 || len(snapshot.Reports.Reports) != 4 || snapshot.Catalog.Models != 3 {
		t.Fatalf("unbounded or incorrect overview: %+v", snapshot)
	}
	const newest = "90000000-0000-4000-8000-000000000007"
	if snapshot.Reports.Reports[0].ID != newest {
		t.Fatal("recent report order lost")
	}
	available, err := repository.AvailableReports(t.Context(), []string{newest, activeID})
	if err != nil || len(available) != 1 || available[0] != newest {
		t.Fatalf("archive existence: %v %v", available, err)
	}
}

func TestComparisonHistoryRejectsSubstitutedRun(t *testing.T) {
	path, repository, _ := openHardeningRepository(t)
	defer repository.Close()
	group := historyComparison(t, repository, "70000000-0000-4000-8000-000000000001")
	original, err := repository.GetRun(t.Context(), group.Runs()[0].RunID)
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := domain.NewRun(
		entityMeta("70000000-0000-4000-8000-000000000099", 1), original.PlanID(), original.Snapshot(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	tamper(t, path, `UPDATE comparison_runs SET run_id=? WHERE comparison_id=? AND position=0`, replacement.Meta().ID, group.Meta().ID)
	if _, err := repository.ComparisonHistory(t.Context(), "", nil); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("substituted comparison run accepted: %v", err)
	}
}
