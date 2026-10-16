package jobhost_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

// Execution is deliberately absent: these tests settle already admitted facts.
type settlementOnlyCore struct{ *jobhost.RunExecutor }

func (core settlementOnlyCore) Execute(context.Context, jobhost.Execution) (jobhost.Outcome, error) {
	return jobhost.Outcome{Status: domain.RunCancelled}, nil
}

var errLostReportAcknowledgement = errors.New("lost report acknowledgement")
var errReportConfirmationRead = errors.New("report confirmation read failed")

type delayedSettlementRepository struct {
	*sqlite.Repository
	t                *testing.T
	blocked          string
	loseReport       bool
	failConfirmation bool
	calls            map[string]int
}

func (repository *delayedSettlementRepository) pause(ctx context.Context, operation string) error {
	repository.t.Helper()
	deadline, bounded := ctx.Deadline()
	if !bounded || deadline.Sub(time.Now()) != 5*time.Second {
		repository.t.Errorf("%s did not receive its own five-second allowance: %v", operation, deadline.Sub(time.Now()))
	}
	repository.calls[operation]++
	if operation == repository.blocked {
		<-ctx.Done()
		return ctx.Err()
	}
	time.Sleep(2 * time.Second)
	return ctx.Err()
}

func (repository *delayedSettlementRepository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	if err := repository.pause(ctx, "GetRun"); err != nil {
		return domain.Run{}, err
	}
	return repository.Repository.GetRun(ctx, id)
}

func (repository *delayedSettlementRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	if err := repository.pause(ctx, "UpdateRun"); err != nil {
		return err
	}
	return repository.Repository.UpdateRun(ctx, revision, run)
}

func (repository *delayedSettlementRepository) ListReportsForRuns(ctx context.Context, ids []string) ([]domain.Report, error) {
	if err := repository.pause(ctx, "ListReportsForRuns"); err != nil {
		return nil, err
	}
	if repository.failConfirmation && repository.calls["ListReportsForRuns"] == 2 {
		return nil, errReportConfirmationRead
	}
	return repository.Repository.ListReportsForRuns(ctx, ids)
}

func (repository *delayedSettlementRepository) RunExecutionOrigin(ctx context.Context, id string) (*domain.ExecutionOrigin, error) {
	if err := repository.pause(ctx, "RunExecutionOrigin"); err != nil {
		return nil, err
	}
	return repository.Repository.RunExecutionOrigin(ctx, id)
}

func (repository *delayedSettlementRepository) ListResults(ctx context.Context, id string) ([]domain.Result, error) {
	if err := repository.pause(ctx, "ListResults"); err != nil {
		return nil, err
	}
	return repository.Repository.ListResults(ctx, id)
}

func (repository *delayedSettlementRepository) ListEvidence(ctx context.Context, id string) ([]domain.Evidence, error) {
	if err := repository.pause(ctx, "ListEvidence"); err != nil {
		return nil, err
	}
	return repository.Repository.ListEvidence(ctx, id)
}

func (repository *delayedSettlementRepository) CreateReport(ctx context.Context, report domain.Report) error {
	if err := repository.pause(ctx, "CreateReport"); err != nil {
		return err
	}
	if err := repository.Repository.CreateReport(ctx, report); err != nil {
		return err
	}
	if repository.loseReport {
		return errLostReportAcknowledgement
	}
	return nil
}

func (repository *delayedSettlementRepository) GetComparison(ctx context.Context, id string) (domain.Comparison, error) {
	if err := repository.pause(ctx, "GetComparison"); err != nil {
		return domain.Comparison{}, err
	}
	return repository.Repository.GetComparison(ctx, id)
}

func (repository *delayedSettlementRepository) IsLocalComparison(ctx context.Context, id string) (bool, error) {
	if err := repository.pause(ctx, "IsLocalComparison"); err != nil {
		return false, err
	}
	return repository.Repository.IsLocalComparison(ctx, id)
}

func (repository *delayedSettlementRepository) UpdateComparison(ctx context.Context, revision uint64, comparison domain.Comparison) error {
	if err := repository.pause(ctx, "UpdateComparison"); err != nil {
		return err
	}
	return repository.Repository.UpdateComparison(ctx, revision, comparison)
}

func TestSettlementHasIndependentDeadlinesAcrossRunAndComparison(t *testing.T) {
	for _, comparison := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "comparison"}[comparison], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repository := openHostRepository(t)
				slow := &delayedSettlementRepository{Repository: repository, t: t, calls: map[string]int{}, loseReport: true}
				core := newRunCore(t, slow)
				host := newTestHost(t, hostDependencies(repository, settlementOnlyCore{core}, &countedGuard{}))
				maintenance, err := jobhost.NewMaintenance(t.Context(), host, &maintenanceMemory{})
				if err != nil {
					t.Fatal(err)
				}
				submission := coreSubmission(t, "https://must-not-be-requested.example.test", comparison)
				spec := submission.Job
				if _, _, err := host.Submit(t.Context(), &submission); err != nil {
					t.Fatal(err)
				}
				time.Sleep(6 * time.Second)
				status := host.Status()
				if !status.Healthy || status.Pending != 1 || status.Active != 1 {
					t.Fatalf("a multi-step settlement lost ownership after one I/O allowance: %+v", status)
				}
				if _, err := maintenance.Apply(t.Context(), pauseCommand()); err != nil {
					t.Fatal(err)
				}
				if host.Status().Pending != 1 {
					t.Fatal("maintenance lost unfinished report work")
				}
				time.Sleep(90 * time.Second)
				status = host.Status()
				if !status.Healthy || status.Pending != 0 || status.Active != 0 || status.Ready {
					t.Fatalf("settlement did not finish under maintenance: %+v", status)
				}
				for _, operation := range []string{"GetRun", "UpdateRun", "ListReportsForRuns", "RunExecutionOrigin", "ListResults", "ListEvidence", "CreateReport"} {
					if slow.calls[operation] == 0 {
						t.Fatalf("missing storage boundary: %s", operation)
					}
				}
				if comparison && slow.calls["CreateReport"] != 2 {
					t.Fatalf("comparison reports = %d", slow.calls["CreateReport"])
				}
				pending, err := repository.RecoveryJobs(t.Context())
				if err != nil || len(pending) != 0 {
					t.Fatalf("durable settlement: %d, %v", len(pending), err)
				}
				reports, err := repository.ListReportsForRuns(t.Context(), runIDsForSettlement(spec))
				if err != nil || len(reports) != len(spec.PreparedRuns()) {
					t.Fatalf("reports=%d err=%v", len(reports), err)
				}
			})
		})
	}
}

func runIDsForSettlement(spec jobcontract.JobSpec) []string {
	ids := []string{}
	for _, run := range spec.PreparedRuns() {
		ids = append(ids, run.Meta.ID)
	}
	return ids
}

func TestSettlementBlockedOperationIsolatesAndRemainsPendingForRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repository := openHostRepository(t)
		blocked := &delayedSettlementRepository{Repository: repository, t: t, blocked: "ListResults", calls: map[string]int{}}
		core := newRunCore(t, blocked)
		host := newTestHost(t, hostDependencies(repository, settlementOnlyCore{core}, &countedGuard{}))
		submission := coreSubmission(t, "https://must-not-be-requested.example.test", true)
		spec := submission.Job
		if _, _, err := host.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		status := host.Status()
		if blocked.calls["ListReportsForRuns"] != 1 {
			t.Fatal("known read failure performed an acknowledgement read before isolation")
		}
		if status.Healthy || status.Pending != 1 || status.Active != 1 {
			t.Fatalf("failed settlement lost pending ownership: %+v", status)
		}
		next := coreSubmission(t, "https://must-not-be-requested.example.test", false)
		if _, _, err := host.Submit(t.Context(), &next); !errors.Is(err, jobhost.ErrUnavailable) {
			t.Fatal(err)
		}
		if err := host.Close(); !errors.Is(err, jobhost.ErrUnavailable) {
			t.Fatal(err)
		}
		pending, err := repository.RecoveryJobs(t.Context())
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending=%d err=%v", len(pending), err)
		}
		recovered := newRunCore(t, repository)
		restarted := newTestHost(t, hostDependencies(repository, settlementOnlyCore{recovered}, &countedGuard{}))
		if !restarted.Status().Ready {
			t.Fatal("recovery did not finish")
		}
		reports, err := repository.ListReportsForRuns(t.Context(), runIDsForSettlement(spec))
		if err != nil || len(reports) != 2 {
			t.Fatalf("recovered reports=%d err=%v", len(reports), err)
		}
	})
}

type cancelledSettlementCore struct {
	*controlledCore
	entered chan struct{}
	exited  atomic.Bool
}

func (core *cancelledSettlementCore) Settle(ctx context.Context, _ jobcontract.JobSpec, _ jobhost.Outcome) error {
	close(core.entered)
	<-ctx.Done()
	core.exited.Store(true)
	return ctx.Err()
}

func TestCloseCancelsAndJoinsSettlementWithoutClearingPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repository := openHostRepository(t)
		core := &cancelledSettlementCore{controlledCore: newControlledCore(), entered: make(chan struct{})}
		host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
		submission := testSubmission(t)
		if _, _, err := host.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		close(core.release)
		<-core.entered
		started := time.Now()
		if err := host.Close(); !errors.Is(err, jobhost.ErrUnavailable) {
			t.Fatal(err)
		}
		if elapsed := time.Since(started); elapsed != 10*time.Second {
			t.Fatalf("close elapsed=%v", elapsed)
		}
		if !core.exited.Load() || host.Status().Pending != 1 {
			t.Fatal("Close returned before joining or fabricated settled status")
		}
	})
}

func TestSettlementWorkflowHasAnIndependentFiniteBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repository := openHostRepository(t)
		core := &cancelledSettlementCore{controlledCore: newControlledCore(), entered: make(chan struct{})}
		host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
		submission := testSubmission(t)
		if _, _, err := host.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		close(core.release)
		<-core.entered
		time.Sleep(3 * time.Minute)
		if !core.exited.Load() || host.Status().Healthy || host.Status().Pending != 1 {
			t.Fatal("settlement workflow was unbounded or lost unfinished ownership")
		}
	})
}

type lostSettlementMarker struct{ *sqlite.Repository }

func (repository lostSettlementMarker) MarkJobSettled(ctx context.Context, id string) error {
	if err := repository.Repository.MarkJobSettled(ctx, id); err != nil {
		return err
	}
	return errors.New("lost settled acknowledgement")
}

func TestSettlementDoesNotClearPendingAfterUnconfirmedMarker(t *testing.T) {
	repository := openHostRepository(t)
	core := newControlledCore()
	guard := &countedGuard{}
	host := newTestHost(t, hostDependencies(lostSettlementMarker{repository}, core, guard))
	submission := testSubmission(t)
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	close(core.release)
	waitHost(t, func() bool { return !host.Status().Healthy })
	if status := host.Status(); status.Pending != 1 || status.Active != 1 {
		t.Fatalf("unconfirmed marker lost ownership: %+v", status)
	}
	if err := host.Close(); !errors.Is(err, jobhost.ErrUnavailable) {
		t.Fatal(err)
	}
	guard.mu.Lock()
	released := guard.released
	guard.mu.Unlock()
	if released != 1 {
		t.Fatalf("reservation released %d times across failure and Close", released)
	}
	pending, err := repository.RecoveryJobs(t.Context())
	if err != nil || len(pending) != 0 {
		t.Fatalf("committed marker was changed: pending=%d err=%v", len(pending), err)
	}
	restarted := newTestHost(t, hostDependencies(repository, newControlledCore(), &countedGuard{}))
	if !restarted.Status().Ready || restarted.Status().Pending != 0 {
		t.Fatal("restart did not use the durable settled marker")
	}
}

func TestSettlementConfirmationErrorPreservesOriginalWriteFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repository := openHostRepository(t)
		failing := &delayedSettlementRepository{Repository: repository, t: t, loseReport: true, failConfirmation: true, calls: map[string]int{}}
		core := newRunCore(t, failing)
		submission := coreSubmission(t, "https://must-not-be-requested.example.test", false)
		defer submission.Clear()
		spec := submission.Job
		if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
			Spec: spec, Origin: acceptedOrigin(spec), SecretCommitment: spec.EngineBuildID, AcceptedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := repository.ChangeJob(t.Context(), jobhost.Change{
			JobID: spec.JobID, ExpectedRevision: 1, Status: domain.RunCancelled, At: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
		err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCancelled})
		if !errors.Is(err, errLostReportAcknowledgement) || !errors.Is(err, errReportConfirmationRead) {
			t.Fatalf("settlement masked a failure: %v", err)
		}
	})
}

type settlementOnlyPerformanceCore struct{ *jobhost.PerformanceExecutor }

func (core settlementOnlyPerformanceCore) Execute(context.Context, jobhost.Execution) (jobhost.Outcome, error) {
	return jobhost.Outcome{Status: domain.RunCancelled}, nil
}

type delayedPerformanceSettlement struct{ *delayedSettlementRepository }

func (repository delayedPerformanceSettlement) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	if err := repository.pause(ctx, "GetJob"); err != nil {
		return jobcontract.Snapshot{}, err
	}
	return repository.Repository.GetJob(ctx, id)
}

func (repository delayedPerformanceSettlement) GetJobSpec(ctx context.Context, id string) (jobcontract.JobSpec, error) {
	if err := repository.pause(ctx, "GetJobSpec"); err != nil {
		return jobcontract.JobSpec{}, err
	}
	return repository.Repository.GetJobSpec(ctx, id)
}

func (repository delayedPerformanceSettlement) SealPerformanceResult(ctx context.Context, id string, at time.Time) (jobhost.PerformanceResultDocument, error) {
	if err := repository.pause(ctx, "SealPerformanceResult"); err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	return repository.Repository.SealPerformanceResult(ctx, id, at)
}

func (repository delayedPerformanceSettlement) ReadPerformanceResult(ctx context.Context, id string) (jobhost.PerformanceResultDocument, error) {
	if err := repository.pause(ctx, "ReadPerformanceResult"); err != nil {
		return jobhost.PerformanceResultDocument{}, err
	}
	return repository.Repository.ReadPerformanceResult(ctx, id)
}

func (repository delayedPerformanceSettlement) GetQuickPerformanceReport(ctx context.Context, id string) (quicktest.PerformanceReport, error) {
	if err := repository.pause(ctx, "GetQuickPerformanceReport"); err != nil {
		return quicktest.PerformanceReport{}, err
	}
	return repository.Repository.GetQuickPerformanceReport(ctx, id)
}

func (repository delayedPerformanceSettlement) SaveQuickPerformanceReport(ctx context.Context, report quicktest.PerformanceReport) error {
	if err := repository.pause(ctx, "SaveQuickPerformanceReport"); err != nil {
		return err
	}
	return repository.Repository.SaveQuickPerformanceReport(ctx, report)
}

func TestPerformanceSettlementHasIndependentStorageDeadlines(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repository := openHostRepository(t)
		delayed := &delayedSettlementRepository{Repository: repository, t: t, calls: map[string]int{}}
		core, err := jobhost.NewPerformanceExecutor(jobhost.PerformanceExecutorDependencies{
			Repository: delayedPerformanceSettlement{delayed}, Clock: wallClock{},
		})
		if err != nil {
			t.Fatal(err)
		}
		host := newTestHost(t, hostDependencies(repository, settlementOnlyPerformanceCore{core}, &countedGuard{}))
		submission := testSubmission(t)
		if _, _, err := host.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Second)
		if status := host.Status(); !status.Ready || status.Pending != 0 {
			t.Fatalf("performance settlement=%+v", status)
		}
		for _, method := range []string{"GetJobSpec", "GetJob", "SealPerformanceResult", "GetQuickPerformanceReport"} {
			if delayed.calls[method] == 0 {
				t.Fatalf("missing performance port boundary: %s", method)
			}
		}
	})
}

type noProviderRunExecutor struct{}

func (noProviderRunExecutor) Execute(context.Context, runs.ExecutionRequest, func(runs.ResultDraft) error) error {
	return errors.New("fixture execution has no provider")
}

type closeDuringGroupRefresh struct {
	*sqlite.Repository
	entered         chan struct{}
	blockComparison bool
	refreshing      bool
	first           bool
	cancelled       bool
}

func (repository *closeDuringGroupRefresh) interruptibleRead(ctx context.Context) error {
	close(repository.entered)
	<-ctx.Done()
	repository.refreshing = false
	repository.cancelled = true
	return ctx.Err()
}

func (repository *closeDuringGroupRefresh) GetComparison(ctx context.Context, id string) (domain.Comparison, error) {
	if !repository.first {
		repository.first = true
		repository.refreshing = true
		if repository.blockComparison {
			return domain.Comparison{}, repository.interruptibleRead(ctx)
		}
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return domain.Comparison{}, ctx.Err()
		}
	}
	return repository.Repository.GetComparison(ctx, id)
}

func (repository *closeDuringGroupRefresh) GetRun(ctx context.Context, id string) (domain.Run, error) {
	if repository.refreshing {
		return domain.Run{}, repository.interruptibleRead(ctx)
	}
	return repository.Repository.GetRun(ctx, id)
}

func TestCloseCancelsThirtyTwoChildExecutionRefreshWithoutStorageFailure(t *testing.T) {
	for _, blockComparison := range []bool{false, true} {
		t.Run(map[bool]string{false: "child_read", true: "comparison_read"}[blockComparison], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				repository := openHostRepository(t)
				blocked := &closeDuringGroupRefresh{Repository: repository, entered: make(chan struct{}), blockComparison: blockComparison}
				core, err := jobhost.NewRunExecutor(jobhost.RunExecutorDependencies{
					Repository: blocked, Executor: noProviderRunExecutor{}, Clock: wallClock{},
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = core.Close() })
				host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
				submission := coreSubmission(t, "https://must-not-be-requested.example.test", true)
				group := submission.Job.Spec.Comparison
				first := group.Runs[0]
				for len(group.Runs) < jobcontract.MaxSecrets {
					child, err := domain.NewRun(coreMeta(t), first.PlanID, first.Snapshot)
					if err != nil {
						t.Fatal(err)
					}
					snapshot := child.Snapshot()
					snapshot.Channel.ID = coreMeta(t).ID
					snapshot.Mapping.ChannelID, snapshot.Mapping.ID = snapshot.Channel.ID, coreMeta(t).ID
					group.Runs = append(group.Runs, runs.PreparedRun{Meta: child.Meta(), PlanID: child.PlanID(), Snapshot: snapshot})
					submission.Secrets = append(submission.Secrets, jobcontract.Secret{Slot: child.Meta().ID, Value: []byte("node-secret")})
				}
				if _, _, err := host.Submit(t.Context(), &submission); err != nil {
					t.Fatal(err)
				}
				<-blocked.entered
				started := time.Now()
				if err := host.Close(); err != nil {
					t.Fatal(err)
				}
				if !blocked.cancelled || time.Since(started) != 0 || host.Status().Pending != 0 {
					t.Fatalf("Close did not cancel and join group refresh: cancelled=%v elapsed=%v status=%+v", blocked.cancelled, time.Since(started), host.Status())
				}
			})
		})
	}
}
