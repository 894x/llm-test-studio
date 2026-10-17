package runs

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const runStorageTimeout = 5 * time.Second

// ExecutionRepository contains only execution facts, never authored catalogs.
type ExecutionRepository interface {
	GetRun(context.Context, string) (domain.Run, error)
	UpdateRun(context.Context, uint64, domain.Run) error
	// AppendResults atomically writes outputs whose suite and case identities
	// have been resolved against the prepared run snapshot by the caller.
	AppendResults(context.Context, ...domain.Result) error
}

type HostDependencies struct {
	Repository       ExecutionRepository
	Executor         Executor
	Clock            Clock
	MetaFactory      MetaFactory
	Reporter         ReportGenerator
	ReportError      func(error)
	ReportDiagnostic func(Diagnostic)
	// PersistenceFailure is a synchronous owner signal, separate from lossy
	// diagnostics. It must return promptly and must not call back into this Host.
	PersistenceFailure func(string)
}

// Host executes complete frozen runs independently of the preparing client.
type Host struct {
	repository         ExecutionRepository
	executor           Executor
	clock              Clock
	metaFactory        MetaFactory
	reporter           ReportGenerator
	reportError        func(error)
	diagnostics        *diagnosticDispatcher
	persistenceFailure func(string)
	recoveryContext    context.Context
	stopRecovery       context.CancelFunc

	mu        sync.Mutex
	active    map[string]*runControl
	closed    bool
	closeDone chan struct{}
	closeErr  error
	wg        sync.WaitGroup
}

type runControl struct {
	mu              sync.Mutex
	run             domain.Run
	cases           []domain.TestCase
	lease           *credentials.Lease
	stop            chan struct{}
	stopOnce        sync.Once
	reportOnce      sync.Once
	finishOnce      sync.Once
	finished        chan struct{}
	recoveryContext context.Context
	stopRecovery    context.CancelFunc
	stopRequested   bool
	terminating     bool
	cancel          context.CancelFunc
	summaryInputs   map[string][]caseSummaryInput
	entries         []preparedRunEntry
}

type preparedRunEntry struct {
	snapshot domain.RunEntrySnapshot
	cases    []domain.TestCase
}

type caseSummaryInput struct {
	phase              string
	executionStatus    domain.ExecutionStatus
	verificationStatus testspec.VerdictStatus
	failure            domain.FailureKind
	errorCode          domain.ErrorCode
	metrics            map[string]float64
	evidenceIDs        []string
}

func suiteCaseKey(entryID, caseID string) string {
	return entryID + "\x00" + caseID
}

func NewHost(dependencies HostDependencies) (*Host, error) {
	invalid := isNil(dependencies.Repository) || isNil(dependencies.Executor) || isNil(dependencies.Clock)
	if invalid {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	reporter := dependencies.Reporter
	if isNil(reporter) {
		reporter = nil
	}
	recoveryContext, stopRecovery := context.WithCancel(context.Background())
	return &Host{
		repository: dependencies.Repository, executor: dependencies.Executor,
		clock: dependencies.Clock, metaFactory: factory, reporter: reporter,
		reportError:        dependencies.ReportError,
		diagnostics:        newDiagnosticDispatcher(dependencies.ReportDiagnostic),
		persistenceFailure: dependencies.PersistenceFailure,
		recoveryContext:    recoveryContext, stopRecovery: stopRecovery,
		active: make(map[string]*runControl), closeDone: make(chan struct{}),
	}, nil
}

func (service *Host) reportStartTiming(runID, operation string, duration time.Duration) {
	service.report(Diagnostic{
		RunID: runID, Operation: operation, ErrorCode: "phase_timing", Duration: duration,
	})
}

// AdoptQueued takes ownership of lease on every path. Admission must already
// have persisted the complete Run; adoption never creates execution facts.
func (service *Host) AdoptQueued(
	ctx context.Context,
	runID string,
	lease *credentials.Lease,
) error {
	owned := true
	defer func() {
		if owned {
			_ = lease.Close()
		}
	}()
	invalid := service == nil || ctx == nil || lease == nil || !domain.IsUUID(runID)
	if invalid {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return ErrClosed
	}
	if _, exists := service.active[runID]; exists {
		return ErrAlreadyAccepted
	}
	stored, err := service.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("read admitted run: %w", err)
	}
	initial := stored.Meta().ID == runID && stored.Meta().Revision == 1 && stored.Status() == domain.RunQueued
	if !initial {
		return ErrNotActive
	}
	run, err := domain.NewRun(stored.Meta(), stored.PlanID(), stored.Snapshot())
	if err != nil {
		return fmt.Errorf("validate admitted run: %w", err)
	}
	service.registerQueued(run, lease)
	owned = false
	return nil
}

// The caller holds service.mu and has validated and persisted the initial Run.
func (service *Host) registerQueued(run domain.Run, lease *credentials.Lease) {
	snapshot := run.Snapshot()
	recoveryContext, stopRecovery := context.WithCancel(service.recoveryContext)
	control := &runControl{
		run: run, lease: lease, stop: make(chan struct{}), cancel: stopRecovery,
		finished: make(chan struct{}), recoveryContext: recoveryContext, stopRecovery: stopRecovery,
		summaryInputs: make(map[string][]caseSummaryInput),
		entries:       make([]preparedRunEntry, len(snapshot.Entries)),
	}
	for index, entry := range snapshot.Entries {
		control.entries[index] = preparedRunEntry{
			snapshot: entry, cases: append([]domain.TestCase{}, entry.CaseDefinitions...),
		}
	}
	service.active[run.Meta().ID] = control
}

func (service *Host) ActivateRun(ctx context.Context, runID string) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return ErrClosed
	}
	control := service.active[runID]
	if control == nil {
		service.mu.Unlock()
		return ErrNotActive
	}
	// Reserve the WaitGroup slot while Close is excluded by service.mu. This
	// prevents Add racing with shutdown's Wait without holding service.mu while
	// acquiring the per-run lock.
	service.wg.Add(1)
	service.mu.Unlock()
	control.mu.Lock()
	if control.run.Status() != domain.RunQueued || control.terminating {
		control.mu.Unlock()
		service.wg.Done()
		return ErrNotActive
	}
	if err := service.transition(ctx, control, domain.RunStarting); err != nil {
		control.mu.Unlock()
		service.wg.Done()
		return err
	}
	executionContext, cancel := context.WithCancel(ctx)
	previousCancel := control.cancel
	control.cancel = func() {
		cancel()
		previousCancel()
	}
	control.mu.Unlock()
	go service.execute(executionContext, control)
	return nil
}

func (service *Host) StopSending(ctx context.Context, runID string) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	control, err := service.activeControl(runID)
	if err != nil {
		return err
	}
	control.mu.Lock()
	defer control.mu.Unlock()
	if control.terminating {
		return ErrNotActive
	}
	control.stopRequested = true
	control.stopOnce.Do(func() { close(control.stop) })
	if control.run.Status() == domain.RunRunning {
		return service.transition(ctx, control, domain.RunDraining)
	}
	if control.run.Status() != domain.RunStarting && control.run.Status() != domain.RunDraining {
		return ErrNotActive
	}
	return nil
}

func (service *Host) CancelRun(ctx context.Context, runID string) error {
	return service.terminateRun(
		ctx,
		runID,
		domain.RunCancelled,
		domain.RunFailure{},
	)
}

// FailRun records an explicit owner failure before stopping active requests.
// It also stops this Run's terminal retry loop when storage is unavailable.
func (service *Host) FailRun(ctx context.Context, runID string, failure domain.RunFailure) error {
	if err := failure.Validate(); err != nil {
		return ErrInvalid
	}
	return service.terminateRun(
		ctx,
		runID,
		domain.RunFailed,
		failure,
	)
}

func (service *Host) terminateRun(
	ctx context.Context,
	runID string,
	terminal domain.RunStatus,
	failure domain.RunFailure,
) error {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrInvalid
	}
	control, err := service.activeControl(runID)
	if err != nil {
		return err
	}
	control.mu.Lock()
	status := control.run.Status()
	if isTerminal(status) || control.terminating {
		control.mu.Unlock()
		return ErrNotActive
	}
	control.terminating = true
	control.stopRecovery()
	err = service.saveTerminal(
		ctx,
		control,
		terminal,
		failure,
	)
	control.cancel()
	control.mu.Unlock()
	if status == domain.RunQueued {
		service.completeControl(control)
	}
	return err
}

// WaitRun observes completion without owning or cancelling execution. A
// terminal status alone is insufficient: all workers and report callbacks must
// finish before the owner can release resources or settle a complete Job.
func (service *Host) WaitRun(ctx context.Context, runID string) (domain.Run, error) {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return domain.Run{}, ErrInvalid
	}
	service.mu.Lock()
	control := service.active[runID]
	service.mu.Unlock()
	if control != nil {
		select {
		case <-control.finished:
		case <-ctx.Done():
			return domain.Run{}, ctx.Err()
		}
	}
	run, err := service.GetRun(ctx, runID)
	if err != nil {
		return domain.Run{}, err
	}
	if !isTerminal(run.Status()) {
		return domain.Run{}, ErrNotActive
	}
	return run, nil
}

func (service *Host) GetRun(ctx context.Context, runID string) (domain.Run, error) {
	if service == nil || ctx == nil || !domain.IsUUID(runID) {
		return domain.Run{}, ErrInvalid
	}
	storeContext, cancel := context.WithTimeout(ctx, runStorageTimeout)
	defer cancel()
	return service.repository.GetRun(storeContext, runID)
}

func (service *Host) Close() error {
	if service == nil {
		return nil
	}
	service.mu.Lock()
	if service.closed {
		done := service.closeDone
		service.mu.Unlock()
		<-done
		return service.closeErr
	}
	service.closed = true
	service.stopRecovery()
	controls := make([]*runControl, 0, len(service.active))
	for _, control := range service.active {
		controls = append(controls, control)
	}
	service.mu.Unlock()
	var closeErrors []error
	for _, control := range controls {
		control.mu.Lock()
		wasQueued := control.run.Status() == domain.RunQueued
		control.terminating = true
		switch control.run.Status() {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := service.saveTerminal(
				ctx,
				control,
				domain.RunCancelled,
				domain.RunFailure{},
			)
			cancel()
			if err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		control.cancel()
		control.mu.Unlock()
		if wasQueued {
			service.completeControl(control)
		}
	}
	service.wg.Wait()
	for _, control := range controls {
		<-control.finished
	}
	if err := service.diagnostics.close(); err != nil {
		closeErrors = append(closeErrors, err)
	}
	service.mu.Lock()
	service.closeErr = errors.Join(closeErrors...)
	close(service.closeDone)
	service.mu.Unlock()
	return service.closeErr
}

func (service *Host) execute(ctx context.Context, control *runControl) {
	defer service.wg.Done()
	defer service.completeControl(control)

	control.mu.Lock()
	if control.terminating {
		control.mu.Unlock()
		return
	}
	if err := service.transition(context.Background(), control, domain.RunRunning); err != nil {
		runID := control.run.Meta().ID
		control.mu.Unlock()
		service.notifyPersistenceFailure(runID)
		service.report(Diagnostic{
			RunID: runID, Operation: "transition_running",
			ErrorCode: "run_state_transition_failed", Err: err,
		})
		service.finishRun(control, domain.RunFailed, domain.RunFailure{
			Phase: "transition_running", ErrorCode: "run_state_transition_failed",
		})
		return
	}
	if control.stopRequested {
		if err := service.transition(context.Background(), control, domain.RunDraining); err != nil {
			runID := control.run.Meta().ID
			control.mu.Unlock()
			service.notifyPersistenceFailure(runID)
			service.report(Diagnostic{
				RunID: runID, Operation: "transition_draining",
				ErrorCode: "run_state_transition_failed", Err: err,
			})
			service.finishRun(control, domain.RunFailed, domain.RunFailure{
				Phase: "transition_draining", ErrorCode: "run_state_transition_failed",
			})
			return
		}
	}
	control.mu.Unlock()
	service.executeEntries(ctx, control)
}

func (service *Host) completeControl(control *runControl) {
	control.finishOnce.Do(func() {
		control.cancel()
		_ = control.lease.Close()
		service.generateTerminalReport(control)
		service.mu.Lock()
		delete(service.active, control.run.Meta().ID)
		close(control.finished)
		service.mu.Unlock()
	})
}

func (service *Host) executeEntries(ctx context.Context, control *runControl) {
	runID := control.run.Meta().ID
	started := time.Now()
	anyFailed := false
	for _, prepared := range control.entries {
		control.mu.Lock()
		if isTerminal(control.run.Status()) || control.terminating {
			control.mu.Unlock()
			return
		}
		request := ExecutionRequest{
			Run: control.run, Entry: prepared.snapshot, Cases: append([]domain.TestCase(nil), prepared.cases...),
			Credential: control.lease, StopSending: control.stop,
		}
		control.mu.Unlock()
		suiteOffsetMS := milliseconds(time.Since(started))
		var storageErr error
		emit := func(draft ResultDraft) error {
			control.mu.Lock()
			defer control.mu.Unlock()
			if control.terminating {
				return context.Canceled
			}
			if control.run.Status() != domain.RunRunning && control.run.Status() != domain.RunDraining {
				return context.Canceled
			}
			if draft.RequestID == "" || !preparedContainsCase(prepared, draft.CaseID) {
				return errors.New("invalid request observation identity")
			}
			result, err := service.newRequestObservation(
				control.run.Meta().ID,
				prepared.snapshot.EntryID,
				draft,
				suiteOffsetMS,
			)
			if err != nil {
				storageErr = err
				return err
			}
			if err := result.Validate(); err != nil {
				return fmt.Errorf("invalid execution result: %w", err)
			}
			if err := service.appendResults(ctx, result); err != nil {
				storageErr = err
				return err
			}
			key := suiteCaseKey(prepared.snapshot.EntryID, draft.CaseID)
			control.summaryInputs[key] = append(control.summaryInputs[key], summaryInput(draft))
			verificationFailed := result.Verification.Status == testspec.VerdictFailed ||
				result.Verification.Status == testspec.VerdictIndeterminate
			if verificationFailed {
				service.report(Diagnostic{
					RunID: result.RunID, RequestID: result.RequestID,
					Operation: "execute_request", ErrorCode: string(result.ErrorCode),
				})
			}
			return nil
		}
		executionErr := service.executor.Execute(ctx, request, emit)

		control.mu.Lock()
		if isTerminal(control.run.Status()) || control.terminating {
			control.mu.Unlock()
			return
		}
		if storageErr == nil && ctx.Err() != nil {
			storageErr = ctx.Err()
		}
		if storageErr != nil {
			control.mu.Unlock()
			service.failResultPersistence(
				control,
				runID,
				"persist_suite_result",
				storageErr,
			)
			return
		}
		if executionErr == nil && prepared.snapshot.Load.Mode == domain.LoadSingle {
			for _, testCase := range prepared.cases {
				if len(control.summaryInputs[suiteCaseKey(prepared.snapshot.EntryID, testCase.ID)]) == 0 {
					executionErr = errors.New("entry did not execute every scheduled case")
					break
				}
			}
		}
		summaryErr := service.persistSuiteCaseSummaries(context.Background(), control, prepared)
		control.mu.Unlock()
		if summaryErr != nil {
			service.failResultPersistence(
				control,
				runID,
				"persist_case_summaries",
				summaryErr,
			)
			return
		}

		status := domain.EntryExecutionCompleted
		if executionErr != nil {
			status = domain.EntryExecutionFailed
			anyFailed = true
			service.report(Diagnostic{
				RunID: runID, Operation: "execute_suite", ErrorCode: "suite_execution_failed", Err: executionErr,
			})
		}
		cancelled, err := service.appendSuiteMarker(
			context.Background(),
			control,
			prepared.snapshot.EntryID,
			status,
		)
		if cancelled {
			return
		}
		if err != nil {
			service.failResultPersistence(
				control,
				runID,
				"persist_suite_marker",
				err,
			)
			return
		}
	}
	if anyFailed {
		service.finishRun(control, domain.RunFailed, domain.RunFailure{
			Phase: "execute_suites", ErrorCode: "one_or_more_suites_failed",
		})
		return
	}
	service.finishRun(control, domain.RunCompleted, domain.RunFailure{})
}

func (service *Host) newRequestObservation(
	runID string,
	entryID string,
	draft ResultDraft,
	suiteOffsetMS float64,
) (domain.Result, error) {
	meta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return domain.Result{}, err
	}
	metrics := cloneMetrics(draft.Metrics)
	for _, key := range []string{"scheduled_offset_ms", "started_offset_ms", "finished_offset_ms"} {
		if value, exists := metrics[key]; exists {
			metrics[key] = value + suiteOffsetMS
		}
	}
	return domain.Result{
		EntityMeta: meta, RunID: runID, EntryID: entryID,
		CaseID: draft.CaseID, RequestID: entryID + ":" + draft.RequestID,
		ExecutionStatus: draft.ExecutionStatus, Verification: draft.Verification,
		Observation: draft.Observation, Failure: draft.Failure, ErrorCode: draft.ErrorCode, Detail: draft.Detail,
		Dimensions:  cloneDimensions(draft.Dimensions),
		Metrics:     metrics,
		EvidenceIDs: append([]string(nil), draft.EvidenceIDs...),
	}, nil
}

func (service *Host) failResultPersistence(control *runControl, runID string, phase domain.ErrorCode, err error) {
	service.notifyPersistenceFailure(runID)
	service.report(Diagnostic{RunID: runID, Operation: string(phase), ErrorCode: "result_persistence_failed", Err: err})
	service.finishRun(control, domain.RunFailed, domain.RunFailure{Phase: phase, ErrorCode: "result_persistence_failed"})
}

func (service *Host) persistSuiteCaseSummaries(
	ctx context.Context,
	control *runControl,
	prepared preparedRunEntry,
) error {
	summaries := make([]domain.Result, 0, len(prepared.cases))
	for _, testCase := range prepared.cases {
		inputs := control.summaryInputs[suiteCaseKey(prepared.snapshot.EntryID, testCase.ID)]
		if len(inputs) == 0 {
			continue
		}
		meta, err := service.metaFactory(service.clock.Now())
		if err != nil {
			return err
		}
		summary := aggregateCaseResult(
			meta,
			control.run.Meta().ID,
			prepared.snapshot.EntryID,
			testCase.ID,
			inputs,
		)
		if err := summary.Validate(); err != nil {
			return err
		}
		summaries = append(summaries, summary)
	}
	if len(summaries) == 0 {
		return nil
	}
	if err := service.appendResults(ctx, summaries...); err != nil {
		return err
	}
	for _, summary := range summaries {
		delete(control.summaryInputs, suiteCaseKey(summary.EntryID, summary.CaseID))
	}
	return nil
}

func (service *Host) appendSuiteMarker(
	ctx context.Context,
	control *runControl,
	entryID string,
	status domain.EntryExecutionStatus,
) (bool, error) {
	control.mu.Lock()
	defer control.mu.Unlock()
	if isTerminal(control.run.Status()) || control.terminating {
		return true, nil
	}
	meta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return false, err
	}
	marker := domain.Result{EntityMeta: meta, RunID: control.run.Meta().ID, EntryID: entryID, EntryStatus: status}
	if err := marker.Validate(); err != nil {
		return false, err
	}
	return false, service.appendResults(ctx, marker)
}

func (service *Host) finishRun(control *runControl, terminal domain.RunStatus, failure domain.RunFailure) {
	// Startup transitions can fail before the executor owns the credential.
	// Release it before storage recovery, just as after normal execution.
	_ = control.lease.Close()
	control.mu.Lock()
	if control.terminating {
		control.mu.Unlock()
		return
	}
	transitionContext, cancelTransition := context.WithTimeout(context.Background(), 5*time.Second)
	transitionErr := service.saveTerminal(
		transitionContext,
		control,
		terminal,
		failure,
	)
	cancelTransition()
	runID := control.run.Meta().ID
	control.mu.Unlock()
	if transitionErr != nil {
		service.notifyPersistenceFailure(runID)
		service.report(Diagnostic{
			RunID: runID, Operation: "transition_" + string(terminal),
			ErrorCode: "run_state_transition_failed", Err: transitionErr,
		})
		transitionErr = service.recoverTerminal(control, terminal, failure)
	}
	if transitionErr == nil {
		service.generateTerminalReport(control)
	}
}

func (service *Host) generateTerminalReport(control *runControl) {
	if service.reporter == nil {
		return
	}
	control.mu.Lock()
	status := control.run.Status()
	runID := control.run.Meta().ID
	control.mu.Unlock()
	if status != domain.RunCompleted && status != domain.RunFailed && status != domain.RunCancelled {
		return
	}
	control.reportOnce.Do(func() {
		if err := service.reporter.Generate(context.Background(), runID); err != nil {
			if service.reportError != nil {
				service.reportError(err)
			}
			service.report(Diagnostic{
				RunID: runID, Operation: "generate_report",
				ErrorCode: "report_generation_failed", Err: err,
			})
		}
	})
}

func (service *Host) report(diagnostic Diagnostic) {
	service.diagnostics.submit(diagnostic)
}

func (service *Host) notifyPersistenceFailure(runID string) {
	if service.persistenceFailure != nil {
		service.persistenceFailure(runID)
	}
}

func aggregateCaseResult(
	meta domain.EntityMeta,
	runID string,
	entryID string,
	caseID string,
	inputs []caseSummaryInput,
) domain.Result {
	measured := make([]caseSummaryInput, 0, len(inputs))
	for _, input := range inputs {
		if input.phase != "warmup" {
			measured = append(measured, input)
		}
	}
	inputs = measured
	result := domain.Result{EntityMeta: meta, RunID: runID, EntryID: entryID, CaseID: caseID,
		ExecutionStatus: domain.ExecutionCompleted,
		Verification: testspec.Verdict{
			Status: testspec.VerdictNotApplicable, Assertions: []testspec.AssertionResult{},
		},
		Metrics: map[string]float64{}, EvidenceIDs: []string{}}
	metricCounts := map[string]int{}
	observations := []domain.Result{}
	evidence := map[string]bool{}
	for _, input := range inputs {
		observations = append(observations, domain.Result{Verification: testspec.Verdict{Status: input.verificationStatus}})
		if input.executionStatus != domain.ExecutionCompleted {
			result.ExecutionStatus = input.executionStatus
			result.Failure = input.failure
			result.ErrorCode = input.errorCode
		}
		for name, value := range input.metrics {
			result.Metrics[name] += value
			metricCounts[name]++
		}
		for _, id := range input.evidenceIDs {
			if !evidence[id] {
				evidence[id] = true
				result.EvidenceIDs = append(result.EvidenceIDs, id)
			}
		}
	}
	for name, count := range metricCounts {
		result.Metrics[name] /= float64(count)
	}
	summary := domain.SummarizeVerification(observations)
	result.Verification.Status = summary.Status
	result.Metrics["execution_count"] = float64(len(inputs))
	result.Metrics["verification_passed"] = float64(summary.Passed)
	result.Metrics["verification_failed"] = float64(summary.Failed)
	result.Metrics["verification_observed"] = float64(summary.Observed)
	result.Metrics["verification_indeterminate"] = float64(summary.Indeterminate)
	return result
}

func preparedContainsCase(prepared preparedRunEntry, caseID string) bool {
	for _, testCase := range prepared.cases {
		if testCase.ID == caseID {
			return true
		}
	}
	return false
}

func sameCaseRevisionRefs(left, right []domain.CaseRevisionRef) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func summaryInput(draft ResultDraft) caseSummaryInput {
	return caseSummaryInput{
		phase: draft.Dimensions["phase"], executionStatus: draft.ExecutionStatus,
		verificationStatus: draft.Verification.Status, failure: draft.Failure, errorCode: draft.ErrorCode,
		metrics: cloneMetrics(draft.Metrics), evidenceIDs: append([]string(nil), draft.EvidenceIDs...),
	}
}

func cloneDimensions(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for name, value := range values {
		cloned[name] = value
	}
	return cloned
}

func (service *Host) transition(ctx context.Context, control *runControl, status domain.RunStatus) error {
	storeContext, cancel := context.WithTimeout(ctx, runStorageTimeout)
	defer cancel()
	current := control.run
	next, err := current.Transition(status, service.clock.Now())
	if err != nil {
		return err
	}
	if err := service.repository.UpdateRun(storeContext, current.Meta().Revision, next); err != nil {
		return err
	}
	control.run = next
	return nil
}

func (service *Host) fail(ctx context.Context, control *runControl, failure domain.RunFailure) error {
	storeContext, cancel := context.WithTimeout(ctx, runStorageTimeout)
	defer cancel()
	current := control.run
	next, err := current.Fail(failure, service.clock.Now())
	if err != nil {
		return err
	}
	if err := service.repository.UpdateRun(storeContext, current.Meta().Revision, next); err != nil {
		return err
	}
	control.run = next
	return nil
}

func (service *Host) appendResults(ctx context.Context, results ...domain.Result) error {
	storeContext, cancel := context.WithTimeout(ctx, runStorageTimeout)
	defer cancel()
	return service.repository.AppendResults(storeContext, results...)
}

func (service *Host) activeControl(runID string) (*runControl, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return nil, ErrClosed
	}
	control := service.active[runID]
	if control == nil {
		return nil, ErrNotActive
	}
	return control, nil
}

func cloneMetrics(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
