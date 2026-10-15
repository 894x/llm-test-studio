package jobhost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type reportOverrideRepository struct {
	*sqlite.Repository
	change func(*domain.Report)
}

func (repository reportOverrideRepository) ListReportsForRuns(
	ctx context.Context,
	ids []string,
) ([]domain.Report, error) {
	reports, err := repository.Repository.ListReportsForRuns(ctx, ids)
	for index := range reports {
		repository.change(&reports[index])
	}
	return reports, err
}

func TestRunExecutorRejectsMismatchedExistingReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newRunCore(t, repository)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, false)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	for _, change := range []func(*domain.Report){
		func(report *domain.Report) { report.RunStatus = domain.RunCancelled },
		func(report *domain.Report) {
			report.PlanSnapshot.Channel.Name, report.Channel.Name = "different channel name", "different channel name"
		},
	} {
		incorrect := newRunCore(t, reportOverrideRepository{Repository: repository, change: change})
		err := incorrect.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted})
		if !errors.Is(err, jobhost.ErrConflict) {
			t.Fatalf("accepted report that did not match its Run: %v", err)
		}
	}
}

func TestRunExecutorRecoversOnlyMissingComparisonReport(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newRunCore(t, &runFaultRepository{Repository: repository, failReportNumber: 2})
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, true)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitHost(t, func() bool { return !host.Status().Ready })
	_ = host.Close()
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, input := range spec.Spec.Comparison.Runs {
		ids = append(ids, input.Meta.ID)
	}
	before, err := repository.ListReportsForRuns(t.Context(), ids)
	if err != nil || len(before) != 1 {
		t.Fatalf("partial report settlement = %d, %v", len(before), err)
	}
	restartedCore := newRunCore(t, repository)
	_ = newTestHost(t, hostDependencies(repository, restartedCore, &countedGuard{}))
	waitSettled(t, repository)
	after, err := repository.ListReportsForRuns(t.Context(), ids)
	if err != nil || len(after) != 2 || requests.Load() != 2 {
		t.Fatalf(
			"recovered reports=%d requests=%d err=%v",
			len(after),
			requests.Load(),
			err,
		)
	}
	preserved := false
	for _, report := range after {
		if report.ID == before[0].ID && report.GeneratedAt.Equal(before[0].GeneratedAt) {
			preserved = true
		}
	}
	if !preserved {
		t.Fatal("recovery rewrote the report that was already committed")
	}
	page, err := repository.JobEvents(
		t.Context(),
		spec.JobID,
		0,
		200,
	)
	if err != nil {
		t.Fatal(err)
	}
	reportEvents := 0
	for _, event := range page.Events {
		if event.Kind == jobcontract.EventReport {
			reportEvents++
		}
	}
	if reportEvents != 2 {
		t.Fatalf("recovery duplicated report references: %d", reportEvents)
	}
}
