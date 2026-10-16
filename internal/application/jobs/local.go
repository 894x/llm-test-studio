package jobs

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type LocalReadRepository interface {
	GetJob(context.Context, string) (jobcontract.Snapshot, error)
	GetJobSpec(context.Context, string) (jobcontract.JobSpec, error)
}

type LocalBackendDependencies struct {
	Host       *jobhost.Host
	Repository LocalReadRepository
	NodeID     string
	Version    string
}

// LocalBackend is one borrowed connection to the application-owned JobHost.
// Close ends this handle; only the application owner shuts down the Host.
type LocalBackend struct {
	host       *jobhost.Host
	repository LocalReadRepository
	info       jobcontract.RunnerInfo
	closed     atomic.Bool
}

var _ Backend = (*LocalBackend)(nil)

func NewLocalBackend(dependencies LocalBackendDependencies) (*LocalBackend, error) {
	if dependencies.Host == nil || nilPort(dependencies.Repository) {
		return nil, ErrInvalid
	}
	identity := dependencies.Host.Identity()
	if identity.Target.Kind != jobcontract.TargetLocal {
		return nil, ErrInvalid
	}
	policy := identity.Policy
	info := jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: dependencies.NodeID,
		NodeInstallationID: identity.InstallationID, EngineBuildID: identity.EngineBuildID,
		RunnerVersion: dependencies.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		Capabilities: jobhost.CoreCapabilities(),
		Budget: jobcontract.RunnerBudget{
			MaxActiveJobs: policy.MaxActiveJobs, MaxQueuedJobs: policy.MaxQueuedJobs,
			MaxConcurrency: policy.MaxConcurrency, QueueTimeoutNS: int64(policy.QueueTimeout),
			ExecutionTimeoutNS: int64(policy.ExecutionTimeout),
		},
	}
	if info.Validate() != nil {
		return nil, ErrInvalid
	}
	return &LocalBackend{host: dependencies.Host, repository: dependencies.Repository, info: info}, nil
}

func (backend *LocalBackend) Probe(ctx context.Context) (jobcontract.RunnerInfo, error) {
	if err := backend.available(ctx); err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	info, status := backend.info.Clone(), backend.host.Status()
	info.Ready, info.Healthy = status.Ready, status.Healthy
	info.Active, info.Queued, info.Pending = status.Active, status.Queued, status.Pending
	info.MaintenanceOperationID = status.MaintenanceOperationID
	return info, nil
}

func (backend *LocalBackend) Submit(
	ctx context.Context,
	submission *jobcontract.Submission,
) (jobcontract.Receipt, error) {
	if submission != nil {
		defer submission.Clear()
	}
	if err := backend.available(ctx); err != nil {
		return jobcontract.Receipt{}, err
	}
	if submission == nil {
		return jobcontract.Receipt{}, ErrInvalid
	}
	if err := submission.Validate(); err != nil {
		return jobcontract.Receipt{}, localBackendError(err)
	}
	if !backend.info.Capabilities.SupportsJob(submission.Job) {
		return jobcontract.Receipt{}, ErrUpgradeRequired
	}
	snapshot, _, err := backend.host.Submit(ctx, submission)
	return snapshot.Receipt, localBackendError(err)
}

func (backend *LocalBackend) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	if err := backend.available(ctx); err != nil {
		return jobcontract.Snapshot{}, err
	}
	if !domain.IsUUID(id) {
		return jobcontract.Snapshot{}, ErrInvalid
	}
	spec, err := backend.repository.GetJobSpec(ctx, id)
	if err != nil {
		return jobcontract.Snapshot{}, localBackendError(err)
	}
	identity := backend.host.Identity()
	owned := spec.Target == identity.Target && spec.NodeInstallationID == identity.InstallationID
	if !owned {
		return jobcontract.Snapshot{}, ErrTargetChanged
	}
	snapshot, err := backend.repository.GetJob(ctx, id)
	if err != nil {
		return jobcontract.Snapshot{}, localBackendError(err)
	}
	if spec.JobID != id || snapshot.Validate(spec) != nil {
		return jobcontract.Snapshot{}, ErrInvalid
	}
	return snapshot, nil
}

func (backend *LocalBackend) Close() error {
	if backend != nil {
		backend.closed.Store(true)
	}
	return nil
}

func (backend *LocalBackend) Command(
	ctx context.Context,
	command jobcontract.Command,
) (jobcontract.CommandConfirmation, error) {
	if err := command.Validate(); err != nil {
		return jobcontract.CommandConfirmation{}, localBackendError(err)
	}
	if _, err := backend.GetJob(ctx, command.JobID); err != nil {
		return jobcontract.CommandConfirmation{}, err
	}
	confirmation, _, err := backend.host.ApplyCommand(ctx, command)
	return confirmation, localBackendError(err)
}

func (backend *LocalBackend) available(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if backend == nil || backend.closed.Load() {
		return ErrUnavailable
	}
	return nil
}

func localBackendError(err error) error {
	switch {
	case errors.Is(err, jobhost.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, jobhost.ErrIdentity):
		return ErrTargetChanged
	case errors.Is(err, jobhost.ErrConflict):
		return ErrConflict
	case errors.Is(err, jobhost.ErrInvalid), errors.Is(err, jobcontract.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, jobcontract.ErrUnsupported):
		return ErrUpgradeRequired
	default:
		return err
	}
}
