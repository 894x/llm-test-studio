package jobhost_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestCoreSharesOneHostQueueAcrossAllJobKinds(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	releaseRequest := sync.OnceFunc(func() { close(release) })
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if requests.Add(1) == 1 {
			close(entered)
			<-release
		}
		if request.Header.Get("Authorization") == "Bearer host-only-secret" {
			writePerformanceCoreResponse(writer)
			return
		}
		writeCoreResponse(writer)
	}))
	defer server.Close()
	defer releaseRequest()
	repository := openHostRepository(t)
	core, err := jobhost.NewCore(jobhost.CoreDependencies{
		Repository: repository, Runs: runs.NewProtocolExecutor(http.DefaultTransport),
		Transport: http.DefaultTransport, Clock: wallClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	first := coreSubmission(t, server.URL, false)
	firstSpec := first.Job
	if _, _, err := host.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first job did not start")
	}
	second := performanceCoreSubmission(t, server.URL, nil)
	third := coreSubmission(t, server.URL, true)
	specs := []jobcontract.JobSpec{firstSpec, second.Job, third.Job}
	for _, submission := range []*jobcontract.Submission{&second, &third} {
		if _, _, err := host.Submit(t.Context(), submission); err != nil {
			t.Fatal(err)
		}
	}
	if host.Status().Active != 1 || host.Status().Queued != 2 || requests.Load() != 1 {
		t.Fatal("job kinds obtained independent execution slots or escaped the shared queue")
	}
	releaseRequest()
	for _, spec := range specs {
		waitPerformanceJob(
			t,
			repository,
			spec.JobID,
			domain.RunCompleted,
		)
	}
	waitSettled(t, repository)
	if requests.Load() != 5 {
		t.Fatal("shared Core omitted or repeated an admitted job's requests")
	}
	result, err := repository.ReadPerformanceResult(t.Context(), specs[1].JobID)
	if err != nil || result.Result.ObservationState != jobcontract.PerformanceAvailable {
		t.Fatalf("shared dispatcher did not seal Performance: %v", err)
	}
	for _, spec := range []jobcontract.JobSpec{specs[0], specs[2]} {
		ids := []string{}
		for _, input := range spec.PreparedRuns() {
			ids = append(ids, input.Meta.ID)
		}
		reports, err := repository.ListReportsForRuns(t.Context(), ids)
		if err != nil || len(reports) != len(ids) {
			t.Fatalf("shared dispatcher did not settle Run reports: %v", err)
		}
	}
}

func TestCoreRejectsIncompleteDependenciesAndNilReceiver(t *testing.T) {
	if _, err := jobhost.NewCore(jobhost.CoreDependencies{}); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatal("empty execution dependencies were accepted")
	}
	var core *jobhost.Core
	if _, err := core.Execute(t.Context(), jobhost.Execution{}); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatal("nil Core accepted execution")
	}
	if err := core.Settle(t.Context(), jobcontract.JobSpec{}, jobhost.Outcome{}); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatal("nil Core accepted settlement")
	}
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
}
