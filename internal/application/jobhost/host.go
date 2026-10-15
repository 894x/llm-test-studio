package jobhost

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrClosed         = errors.New("job_host_closed")
	ErrCapacity       = errors.New("job_capacity_exceeded")
	ErrIdentity       = errors.New("job_host_identity_mismatch")
	ErrUnavailable    = errors.New("job_host_unavailable")
	ErrCancelled      = errors.New("job_cancelled")
	ErrLocalShutdown  = errors.New("local_shutdown")
	ErrInterrupted    = errors.New("runner_interrupted")
	ErrExecutionLimit = errors.New("job_execution_timeout")
)

const storageTimeout = 5 * time.Second

type Dependencies struct {
	Repository     Repository
	Executor       Executor
	Guard          Guard
	Clock          Clock
	Policy         Policy
	Target         jobcontract.ExecutionTarget
	InstallationID string
	EngineBuildID  string
	CommitmentKey  []byte
}

type Host struct {
	repository              Repository
	executor                Executor
	guard                   Guard
	clock                   Clock
	policy                  Policy
	target                  jobcontract.ExecutionTarget
	installationID, buildID string
	commitmentKey           []byte

	mu             sync.Mutex
	controls       map[string]*jobControl
	queue          []*jobControl
	active         uint32
	concurrency    uint64
	closed         bool
	failureHandled bool
	unhealthy      atomic.Bool
	wg             sync.WaitGroup
	closeDone      chan struct{}
}

type jobPhase uint8

const (
	phaseQueued jobPhase = iota
	phaseExecuting
	phaseSettling
)

type jobControl struct {
	spec         jobcontract.JobSpec
	secrets      map[string]*credentials.Lease
	reservation  io.Closer
	units        uint64
	phase        jobPhase
	reserved     bool
	stop         chan struct{}
	stopOnce     sync.Once
	cancel       context.CancelCauseFunc
	timer        *time.Timer
	progressMu   sync.Mutex
	lastProgress time.Time
}

type Status struct {
	Ready  bool   `json:"ready"`
	Active uint32 `json:"active"`
	Queued uint32 `json:"queued"`
}

func (*Host) String() string   { return "jobhost.Host{redacted}" }
func (*Host) GoString() string { return "jobhost.Host{redacted}" }

func New(ctx context.Context, dependencies Dependencies) (*Host, error) {
	invalid := ctx == nil || nilDependency(dependencies.Repository) || nilDependency(dependencies.Executor)
	invalid = invalid || nilDependency(dependencies.Guard) || nilDependency(dependencies.Clock)
	invalid = invalid || !domain.IsUUID(dependencies.InstallationID) || !jobcontract.IsSHA256(dependencies.EngineBuildID)
	if invalid || len(dependencies.CommitmentKey) != 32 || dependencies.Target.Validate() != nil {
		return nil, ErrInvalid
	}
	if err := dependencies.Policy.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dependencies.Clock.Now().IsZero() {
		return nil, ErrInvalid
	}
	host := &Host{
		repository: dependencies.Repository, executor: dependencies.Executor, guard: dependencies.Guard,
		clock: dependencies.Clock, policy: dependencies.Policy, target: dependencies.Target,
		installationID: dependencies.InstallationID, buildID: dependencies.EngineBuildID,
		commitmentKey: slices.Clone(dependencies.CommitmentKey),
		controls:      map[string]*jobControl{}, queue: []*jobControl{}, closeDone: make(chan struct{}),
	}
	if err := host.recover(ctx); err != nil {
		clear(host.commitmentKey)
		return nil, err
	}
	return host, nil
}

// Submit consumes and clears the transport secret bytes on every path. Once
// accepted, neither request cancellation nor response loss owns the execution.
func (host *Host) Submit(ctx context.Context, submission *jobcontract.Submission) (jobcontract.Snapshot, bool, error) {
	if submission != nil {
		defer submission.Clear()
	}
	if host == nil || ctx == nil || submission == nil {
		return jobcontract.Snapshot{}, false, ErrInvalid
	}
	if err := submission.Validate(); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	spec, err := submission.Job.Freeze()
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	sameHost := spec.NodeInstallationID == host.installationID && spec.EngineBuildID == host.buildID
	if !sameHost || spec.Target != host.target {
		return jobcontract.Snapshot{}, false, ErrIdentity
	}
	host.mu.Lock()
	fatalStorage := false
	defer func() {
		if fatalStorage {
			host.storageFailedLocked()
		}
		host.mu.Unlock()
	}()
	if err := host.available(); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	if err := ctx.Err(); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	input := Acceptance{Spec: spec, SecretCommitment: host.secretCommitment(spec.JobID, submission.Secrets)}
	existing, err := host.repository.LookupJob(ctx, input)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		fatalStorage = storageError(err)
		return jobcontract.Snapshot{}, false, err
	}
	units := jobConcurrency(spec)
	immediate := len(host.queue) == 0 && host.active < host.policy.MaxActiveJobs
	immediate = immediate && host.concurrency+units <= uint64(host.policy.MaxConcurrency)
	queueFull := !immediate && len(host.queue) >= int(host.policy.MaxQueuedJobs)
	if units == 0 || units > uint64(host.policy.MaxConcurrency) || queueFull {
		return jobcontract.Snapshot{}, false, ErrCapacity
	}
	reservation, err := host.guard.Reserve(ctx, spec)
	if err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	if nilDependency(reservation) {
		return jobcontract.Snapshot{}, false, ErrInvalid
	}
	control := &jobControl{
		spec: spec, units: units, reservation: reservation, phase: phaseQueued,
		secrets: map[string]*credentials.Lease{}, stop: make(chan struct{}),
	}
	transferred := false
	defer func() {
		if !transferred {
			if err := releaseControl(control); err != nil {
				fatalStorage = true
			}
		}
	}()
	for _, secret := range submission.Secrets {
		lease, err := credentials.NewTemporaryLease(secret.Value)
		if err != nil {
			return jobcontract.Snapshot{}, false, err
		}
		control.secrets[secret.Slot] = lease
	}
	if err := host.available(); err != nil {
		return jobcontract.Snapshot{}, false, err
	}
	input.AcceptedAt = host.clock.Now().UTC()
	snapshot, created, err := host.repository.AcceptJob(ctx, input)
	if err != nil {
		// Commit can report a lost acknowledgement. Only a read of the same
		// immutable identity may resolve it; no second admission is attempted.
		lookupContext, cancel := context.WithTimeout(context.Background(), storageTimeout)
		confirmed, lookupErr := host.repository.LookupJob(lookupContext, input)
		cancel()
		if lookupErr != nil {
			fatalStorage = storageError(err) || !errors.Is(lookupErr, ErrNotFound) && !errors.Is(lookupErr, ErrConflict)
			return jobcontract.Snapshot{}, false, err
		}
		snapshot, created = confirmed, true
	}
	if !created {
		return snapshot, false, nil
	}
	host.controls[spec.JobID] = control
	host.queue = append(host.queue, control)
	transferred = true
	host.dispatchLocked()
	if control.phase == phaseQueued {
		control.timer = time.AfterFunc(host.policy.QueueTimeout, func() { host.expire(control) })
	}
	return snapshot, true, nil
}

func storageError(err error) bool {
	return err != nil && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrNotFound) &&
		!errors.Is(err, ErrInvalid) && !errors.Is(err, jobcontract.ErrInvalid) &&
		!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

func (host *Host) available() error {
	if host.closed {
		return ErrClosed
	}
	if host.unhealthy.Load() {
		return ErrUnavailable
	}
	return nil
}

func (host *Host) secretCommitment(id string, secrets []jobcontract.Secret) string {
	ordered := slices.Clone(secrets)
	slices.SortFunc(ordered, func(left, right jobcontract.Secret) int {
		if left.Slot < right.Slot {
			return -1
		}
		if left.Slot > right.Slot {
			return 1
		}
		return 0
	})
	mac := hmac.New(sha256.New, host.commitmentKey)
	_, _ = mac.Write([]byte("llm-test-job-secret-commitment\x00" + id))
	for _, secret := range ordered {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(secret.Slot)))
		_, _ = mac.Write(size[:])
		_, _ = mac.Write([]byte(secret.Slot))
		binary.BigEndian.PutUint64(size[:], uint64(len(secret.Value)))
		_, _ = mac.Write(size[:])
		_, _ = mac.Write(secret.Value)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func releaseControl(control *jobControl) error {
	for _, lease := range control.secrets {
		_ = lease.Close()
	}
	return control.reservation.Close()
}

func (host *Host) Status() Status {
	if host == nil {
		return Status{}
	}
	host.mu.Lock()
	defer host.mu.Unlock()
	return Status{Ready: host.available() == nil, Active: host.active, Queued: uint32(len(host.queue))}
}

func (host *Host) observe(control *jobControl, progress quicktest.PerformanceProgress) {
	control.progressMu.Lock()
	defer control.progressMu.Unlock()
	at := host.clock.Now().UTC()
	if !control.lastProgress.IsZero() && at.Sub(control.lastProgress) < time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	err := host.repository.RecordJobProgress(
		ctx,
		control.spec.JobID,
		progress,
		at,
	)
	cancel()
	if err != nil {
		host.storageFailed()
		return
	}
	control.lastProgress = at
}
