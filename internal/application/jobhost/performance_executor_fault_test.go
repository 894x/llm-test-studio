package jobhost_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type performanceFaultRepository struct {
	*sqlite.Repository
	loseObservation bool
	loseResult      bool
	loseArchive     bool
	failCheckpoint  func(quicktest.PerformanceCheckpoint) bool
	sealEntered     chan struct{}
	releaseSeal     chan struct{}
	sealOnce        sync.Once
	failed          atomic.Bool
}

func (repository *performanceFaultRepository) RecordPerformanceObservation(
	ctx context.Context,
	input jobhost.PerformanceObservationWrite,
) (jobcontract.PerformanceObservation, error) {
	if repository.failCheckpoint != nil && repository.failCheckpoint(input.Checkpoint) {
		repository.failed.Store(true)
		return jobcontract.PerformanceObservation{}, errors.New("injected checkpoint outage")
	}
	observation, err := repository.Repository.RecordPerformanceObservation(ctx, input)
	if err == nil && repository.loseObservation {
		return jobcontract.PerformanceObservation{}, errors.New("injected lost observation confirmation")
	}
	return observation, err
}

func (repository *performanceFaultRepository) SealPerformanceResult(
	ctx context.Context,
	id string,
	at time.Time,
) (jobhost.PerformanceResultDocument, error) {
	if repository.failed.Load() {
		repository.sealOnce.Do(func() { close(repository.sealEntered) })
		select {
		case <-repository.releaseSeal:
		case <-ctx.Done():
			return jobhost.PerformanceResultDocument{}, ctx.Err()
		}
		return jobhost.PerformanceResultDocument{}, errors.New("injected result outage")
	}
	sealed, err := repository.Repository.SealPerformanceResult(ctx, id, at)
	if err == nil && repository.loseResult {
		return jobhost.PerformanceResultDocument{}, errors.New("injected lost result confirmation")
	}
	return sealed, err
}

func (repository *performanceFaultRepository) SaveQuickPerformanceReport(
	ctx context.Context,
	report quicktest.PerformanceReport,
) error {
	if err := repository.Repository.SaveQuickPerformanceReport(ctx, report); err != nil {
		return err
	}
	if repository.loseArchive {
		return errors.New("injected lost archive confirmation")
	}
	return nil
}

func TestPerformanceExecutorConfirmsLostWritesWithoutRepeatingTraffic(t *testing.T) {
	for _, fault := range []string{"observation", "result", "archive", "all"} {
		t.Run(fault, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writePerformanceCoreResponse(writer)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			flaky := &performanceFaultRepository{
				Repository: repository, loseObservation: fault == "observation" || fault == "all",
				loseResult: fault == "result" || fault == "all", loseArchive: fault == "archive" || fault == "all",
			}
			core := newPerformanceCore(t, flaky)
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			submission := performanceCoreSubmission(t, server.URL, func(command *quicktest.PerformanceCommand) {
				command.WarmupRequests = 1
			})
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			waitPerformanceJob(
				t,
				repository,
				spec.JobID,
				domain.RunCompleted,
			)
			waitSettled(t, repository)
			if !host.Status().Ready || requests.Load() != 3 {
				t.Fatal("lost acknowledgements repeated traffic or incorrectly isolated healthy storage")
			}
			sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
			if err != nil || sealed.Result.Observation.Revision != 7 {
				t.Fatalf("lost acknowledgement changed observation revisions: %v", err)
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
			counts := map[jobcontract.EventKind]int{}
			for _, event := range page.Events {
				counts[event.Kind]++
			}
			if counts[jobcontract.EventPerformanceCheckpoint] != 7 ||
				counts[jobcontract.EventPerformanceResult] != 1 || counts[jobcontract.EventReport] != 1 {
				t.Fatal("lost acknowledgements duplicated or omitted durable result events")
			}
		})
	}
}

func TestPerformanceExecutorCheckpointFaultIsolatesBeforeSettlementAndRecoversWithoutTraffic(t *testing.T) {
	for _, phase := range []string{"warmup", "ramp", "measured", "capacity"} {
		t.Run(phase, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writePerformanceCoreResponse(writer)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			flaky := &performanceFaultRepository{
				Repository: repository, sealEntered: make(chan struct{}), releaseSeal: make(chan struct{}),
				failCheckpoint: func(checkpoint quicktest.PerformanceCheckpoint) bool {
					if string(checkpoint.Stage) != phase {
						return false
					}
					if phase == "capacity" {
						return checkpoint.Boundary == quicktest.PerformanceBoundaryEntered &&
							checkpoint.RungIndex != nil && *checkpoint.RungIndex == 1
					}
					return checkpoint.Boundary == quicktest.PerformanceBoundaryCompleted
				},
			}
			release := sync.OnceFunc(func() { close(flaky.releaseSeal) })
			defer release()
			core := newPerformanceCore(t, flaky)
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			submission := performanceCoreSubmission(t, server.URL, func(command *quicktest.PerformanceCommand) {
				switch phase {
				case "warmup":
					command.WarmupRequests = 1
				case "ramp":
					command.WarmupRequests = 1
					command.RampDurationMS, command.RampRequestCap = 20, 20
				case "capacity":
					command.CapacityEnabled, command.Concurrency = true, 3
					command.CapacityStart, command.CapacityStep = 1, 1
					command.SLOE2EMS, command.SLOTargetPercent = 1000, 100
				}
			})
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			select {
			case <-flaky.sealEntered:
			case <-time.After(10 * time.Second):
				t.Fatal("failed checkpoint did not stop execution and enter settlement")
			}
			if host.Status().Ready {
				t.Fatal("known checkpoint storage failure did not isolate the owner before blocked settlement")
			}
			next := performanceCoreSubmission(t, server.URL, nil)
			if _, _, err := host.Submit(t.Context(), &next); !errors.Is(err, jobhost.ErrUnavailable) {
				t.Fatal("isolated owner accepted more paid work")
			}
			traffic := requests.Load()
			release()
			_ = host.Close()
			terminal := waitPerformanceJob(
				t,
				repository,
				spec.JobID,
				domain.RunFailed,
			)
			if terminal.Failure == nil || terminal.Failure.ErrorCode != "job_storage_unavailable" {
				t.Fatal("checkpoint outage lost its owner failure reason")
			}
			observation, err := repository.GetPerformanceObservation(t.Context(), spec.JobID)
			if err != nil || string(observation.Checkpoint.Stage) != phase {
				t.Fatalf("last durable phase observation missing: %v", err)
			}
			if phase == "capacity" {
				checkpoint := observation.Checkpoint
				if checkpoint.Boundary != quicktest.PerformanceBoundaryCompleted || *checkpoint.RungIndex != 0 ||
					len(checkpoint.Report.CapacityResult.Rungs) != 1 || traffic != 2 {
					t.Fatal("capacity outage changed completed rung facts or advanced to the next rung")
				}
			} else if observation.Checkpoint.Boundary != quicktest.PerformanceBoundaryEntered || traffic == 0 {
				t.Fatal("failed completion fabricated a durable completion or zero upstream traffic")
			}
			restartedCore := newPerformanceCore(t, repository)
			restarted := newTestHost(t, hostDependencies(repository, restartedCore, &countedGuard{}))
			waitSettled(t, repository)
			sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
			if err != nil || sealed.Result.ObservationState != jobcontract.PerformanceInterrupted ||
				sealed.Result.Observation.Revision != observation.Revision {
				t.Fatalf("recovery did not preserve the interrupted observation: %v", err)
			}
			before, _ := repository.GetJob(t.Context(), spec.JobID)
			outcome := jobhost.Outcome{Status: terminal.Receipt.Status, Failure: terminal.Failure}
			if err := restartedCore.Settle(t.Context(), spec, outcome); err != nil {
				t.Fatal(err)
			}
			after, _ := repository.GetJob(t.Context(), spec.JobID)
			again, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
			preserved := again.Reference == sealed.Reference && again.Result.SealedAt.Equal(sealed.Result.SealedAt) &&
				after.LatestSequence == before.LatestSequence
			if err != nil || !preserved || requests.Load() != traffic || !restarted.Status().Ready {
				t.Fatal("recovery repeated traffic, rewrote its result or failed to restore readiness")
			}
			_, err = repository.GetQuickPerformanceReport(t.Context(), spec.Spec.Performance.ReportID)
			if !errors.Is(err, quicktest.ErrPerformanceArchiveNotFound) {
				t.Fatal("unfinished phase became a measured statistical archive")
			}
		})
	}
}

type performanceReadOverrideRepository struct {
	*sqlite.Repository
	changeArchive bool
	changeResult  bool
}

func (repository performanceReadOverrideRepository) GetQuickPerformanceReport(
	ctx context.Context,
	id string,
) (quicktest.PerformanceReport, error) {
	report, err := repository.Repository.GetQuickPerformanceReport(ctx, id)
	if repository.changeArchive {
		report.GeneratedAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	}
	return report, err
}

func (repository performanceReadOverrideRepository) SealPerformanceResult(
	ctx context.Context,
	id string,
	at time.Time,
) (jobhost.PerformanceResultDocument, error) {
	sealed, err := repository.Repository.SealPerformanceResult(ctx, id, at)
	if err == nil && repository.changeResult {
		sealed.Result.Observation.Checkpoint.Report.ModelID = "another-model"
	}
	return sealed, err
}

func TestPerformanceExecutorRejectsChangedArchiveResultAndFrozenInputWithoutMutation(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writePerformanceCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newPerformanceCore(t, repository)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := performanceCoreSubmission(t, server.URL, nil)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitPerformanceJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	before, _ := repository.GetJob(t.Context(), spec.JobID)
	sealed, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"archive", "result", "input"} {
		t.Run(target, func(t *testing.T) {
			modified, err := spec.Freeze()
			if err != nil {
				t.Fatal(err)
			}
			if target == "input" {
				modified.Spec.Performance.ModelID = "another-model"
			}
			incorrect := newPerformanceCore(t, performanceReadOverrideRepository{
				Repository: repository, changeArchive: target == "archive", changeResult: target == "result",
			})
			err = incorrect.Settle(t.Context(), modified, jobhost.Outcome{Status: domain.RunCompleted})
			if !errors.Is(err, jobhost.ErrConflict) {
				t.Fatalf("changed %s was accepted: %v", target, err)
			}
		})
	}
	after, _ := repository.GetJob(t.Context(), spec.JobID)
	again, err := repository.ReadPerformanceResult(t.Context(), spec.JobID)
	if err != nil || again.Reference != sealed.Reference || after.LatestSequence != before.LatestSequence ||
		requests.Load() != 2 {
		t.Fatal("rejected persisted data changed authoritative facts or repeated upstream requests")
	}
}
