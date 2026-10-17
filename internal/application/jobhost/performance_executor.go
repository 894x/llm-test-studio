package jobhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type PerformanceExecutionRepository interface {
	PerformanceRepository
	quicktest.PerformanceArchive
	GetJob(context.Context, string) (jobcontract.Snapshot, error)
	GetJobSpec(context.Context, string) (jobcontract.JobSpec, error)
	GetQuickPerformanceReport(context.Context, string) (quicktest.PerformanceReport, error)
}

type PerformanceExecutorDependencies struct {
	Repository PerformanceExecutionRepository
	Transport  http.RoundTripper
	Clock      Clock
}

// PerformanceExecutor runs only durably admitted Performance jobs. The JobHost
// owns dispatch, control and the winning terminal decision.
type PerformanceExecutor struct {
	repository PerformanceExecutionRepository
	host       *quicktest.Host
	clock      Clock
	mu         sync.Mutex
	active     map[string]struct{}
}

func NewPerformanceExecutor(dependencies PerformanceExecutorDependencies) (*PerformanceExecutor, error) {
	if nilDependency(dependencies.Repository) || nilDependency(dependencies.Clock) {
		return nil, ErrInvalid
	}
	return &PerformanceExecutor{
		repository: performanceStorage{PerformanceExecutionRepository: dependencies.Repository},
		clock:      dependencies.Clock, active: map[string]struct{}{},
		host: quicktest.NewHost(quicktest.HostDependencies{
			Transport: dependencies.Transport, Clock: dependencies.Clock,
		}),
	}, nil
}

func (core *PerformanceExecutor) Execute(ctx context.Context, execution Execution) (Outcome, error) {
	if core == nil || ctx == nil {
		return Outcome{}, ErrInvalid
	}
	spec, err := execution.Spec.Freeze()
	if err != nil || spec.Kind != jobcontract.KindPerformance || len(execution.Secrets) != 1 {
		return Outcome{}, ErrInvalid
	}
	lease := execution.Secrets[spec.Spec.Performance.ReportID]
	if lease == nil {
		return Outcome{}, ErrInvalid
	}
	defer lease.Close()
	if ctx.Err() != nil {
		return reasonOutcome(context.Cause(ctx)), nil
	}
	core.mu.Lock()
	_, exists := core.active[spec.JobID]
	if !exists {
		core.active[spec.JobID] = struct{}{}
	}
	core.mu.Unlock()
	if exists {
		return Outcome{}, ErrConflict
	}
	defer func() {
		core.mu.Lock()
		delete(core.active, spec.JobID)
		core.mu.Unlock()
	}()
	var failureOnce sync.Once
	notifyFailure := func() {
		failureOnce.Do(func() {
			if execution.OnPersistenceFailure != nil {
				execution.OnPersistenceFailure()
			}
		})
	}
	storeContext := context.Background()
	snapshot, err := core.admittedJob(storeContext, spec)
	if err == nil && !jobcontract.Terminal(snapshot.Receipt.Status) {
		_, err = core.repository.GetPerformanceObservation(storeContext, spec.JobID)
		if err == nil {
			err = ErrConflict
		} else if errors.Is(err, ErrNotFound) {
			err = nil
		}
	}
	if err != nil {
		notifyFailure()
		return Outcome{}, err
	}
	if jobcontract.Terminal(snapshot.Receipt.Status) {
		return outcomeOf(snapshot), nil
	}
	if ctx.Err() != nil {
		return reasonOutcome(context.Cause(ctx)), nil
	}
	if snapshot.Receipt.Status != domain.RunRunning && snapshot.Receipt.Status != domain.RunDraining {
		return Outcome{}, ErrConflict
	}
	var revision uint64
	lastTime := snapshot.UpdatedAt
	report, err := core.host.ExecutePrepared(
		ctx,
		*spec.Spec.Performance,
		lease,
		quicktest.ExecutionOptions{
			StopSending: execution.StopSending, OnProgress: execution.OnProgress,
			OnCheckpoint: func(storeContext context.Context, checkpoint quicktest.PerformanceCheckpoint) error {
				at := core.clock.Now().UTC()
				if at.Before(lastTime) {
					at = lastTime
				}
				input := PerformanceObservationWrite{
					JobID: spec.JobID, ExpectedRevision: revision, At: at, Checkpoint: checkpoint,
				}
				confirmation, writeErr := core.repository.RecordPerformanceObservation(storeContext, input)
				if writeErr != nil {
					var readErr error
					confirmation, readErr = core.repository.GetPerformanceObservation(storeContext, spec.JobID)
					if readErr != nil {
						notifyFailure()
						return ErrUnavailable
					}
				}
				matching := confirmation.Revision == revision+1 && confirmation.RecordedAt.Equal(at) &&
					reflect.DeepEqual(confirmation.Checkpoint, checkpoint)
				if !matching || confirmation.ValidateFor(spec) != nil {
					notifyFailure()
					return ErrUnavailable
				}
				revision, lastTime = confirmation.Revision, confirmation.RecordedAt
				return nil
			},
		},
	)
	if err != nil {
		notifyFailure()
		return Outcome{}, ErrUnavailable
	}
	if ctx.Err() != nil {
		return reasonOutcome(context.Cause(ctx)), nil
	}
	if report.ErrorCode != "" {
		return failed(report.ErrorCode), nil
	}
	return Outcome{Status: domain.RunCompleted}, nil
}

func (core *PerformanceExecutor) admittedJob(
	ctx context.Context,
	spec jobcontract.JobSpec,
) (jobcontract.Snapshot, error) {
	stored, err := core.repository.GetJobSpec(ctx, spec.JobID)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	expected, err := spec.Digest()
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	actual, err := stored.Digest()
	if err != nil || actual != expected {
		return jobcontract.Snapshot{}, ErrConflict
	}
	snapshot, err := core.repository.GetJob(ctx, spec.JobID)
	if err != nil {
		return jobcontract.Snapshot{}, err
	}
	if snapshot.Validate(spec) != nil {
		return jobcontract.Snapshot{}, ErrConflict
	}
	return snapshot, nil
}

func (core *PerformanceExecutor) Settle(ctx context.Context, spec jobcontract.JobSpec, outcome Outcome) error {
	invalid := core == nil || ctx == nil || outcome.Validate() != nil
	if invalid || spec.Validate() != nil || spec.Kind != jobcontract.KindPerformance {
		return ErrInvalid
	}
	storeContext := ctx
	snapshot, err := core.admittedJob(storeContext, spec)
	if err != nil {
		return err
	}
	if !jobcontract.Terminal(snapshot.Receipt.Status) {
		return ErrConflict
	}
	at := core.clock.Now().UTC()
	if at.Before(snapshot.UpdatedAt) {
		at = snapshot.UpdatedAt
	}
	sealed, sealErr := core.repository.SealPerformanceResult(storeContext, spec.JobID, at)
	if sealErr != nil {
		sealed, err = core.repository.ReadPerformanceResult(storeContext, spec.JobID)
		if err != nil {
			return errors.Join(sealErr, err)
		}
	}
	if err := verifyPerformanceResult(sealed, spec, snapshot); err != nil {
		return err
	}
	return core.ensurePerformanceArchive(storeContext, sealed.Result)
}

func verifyPerformanceResult(
	sealed PerformanceResultDocument,
	spec jobcontract.JobSpec,
	snapshot jobcontract.Snapshot,
) error {
	result, reference := sealed.Result, sealed.Reference
	matching := result.JobID == spec.JobID && result.ReportID == spec.Spec.Performance.ReportID &&
		result.Terminal.Validate(spec) == nil && reflect.DeepEqual(result.Terminal.Receipt, snapshot.Receipt) &&
		reflect.DeepEqual(result.Terminal.Failure, snapshot.Failure)
	if !matching || reference.Validate() != nil {
		return ErrConflict
	}
	if result.Observation != nil && result.Observation.ValidateFor(spec) != nil {
		return ErrConflict
	}
	expected, err := jobcontract.EncodePerformanceResult(result)
	if err != nil || !bytes.Equal(expected, sealed.Document) {
		return ErrConflict
	}
	digest := sha256.Sum256(sealed.Document)
	validReference := reference.JobID == result.JobID && reference.ReportID == result.ReportID &&
		reference.Size == int64(len(sealed.Document)) && reference.SHA256 == hex.EncodeToString(digest[:])
	if !validReference {
		return ErrConflict
	}
	return nil
}

func (core *PerformanceExecutor) ensurePerformanceArchive(
	ctx context.Context,
	result jobcontract.PerformanceResult,
) error {
	expected, eligible, err := result.StatisticalArchive()
	if err != nil {
		return err
	}
	stored, readErr := core.repository.GetQuickPerformanceReport(ctx, result.ReportID)
	if readErr == nil {
		if !eligible || !reflect.DeepEqual(expected, stored) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(readErr, quicktest.ErrPerformanceArchiveNotFound) {
		return readErr
	}
	if !eligible {
		return nil
	}
	writeErr := core.repository.SaveQuickPerformanceReport(ctx, expected)
	stored, readErr = core.repository.GetQuickPerformanceReport(ctx, result.ReportID)
	if readErr == nil && reflect.DeepEqual(expected, stored) {
		return nil
	}
	if writeErr != nil {
		return writeErr
	}
	if readErr != nil {
		return readErr
	}
	return ErrConflict
}
