package bootstrap

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/enginebuild"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type productionLocalExecutor struct {
	host       *jobhost.Host
	core       *jobhost.Core
	repository *sqlite.Repository
	transport  *http.Transport
	once       sync.Once
	closeError error
}

func newProductionLocalExecutor(
	ctx context.Context,
	repository *sqlite.Repository,
	options Options,
) (*productionLocalExecutor, error) {
	if ctx == nil || repository == nil || options.AppVersion == "" {
		return nil, jobs.ErrInvalid
	}
	identity, err := repository.LocalExecutorIdentity(ctx)
	if err != nil {
		return nil, err
	}
	defer identity.Clear()
	buildID, err := enginebuild.Current()
	if err != nil {
		return nil, err
	}
	transport := options.ProtocolTransport
	var ownedTransport *http.Transport
	if isNilInterface(transport) {
		ownedTransport = http.DefaultTransport.(*http.Transport).Clone()
		ownedTransport.MaxIdleConns, ownedTransport.MaxIdleConnsPerHost = 4096, 4096
		transport = ownedTransport
	}
	core, err := jobhost.NewCore(jobhost.CoreDependencies{
		Repository: repository, Runs: runs.NewProtocolExecutor(transport), Transport: transport, Clock: ProductionClock{},
		ReportDiagnostic: options.ReportRunDiagnostic,
	})
	if err != nil {
		if ownedTransport != nil {
			ownedTransport.CloseIdleConnections()
		}
		return nil, err
	}
	policy := jobhost.Policy{
		MaxActiveJobs: 128, MaxQueuedJobs: 1024, MaxConcurrency: 65_536,
		QueueTimeout: 24 * time.Hour, ExecutionTimeout: 24 * time.Hour,
	}
	host, err := jobhost.New(ctx, jobhost.Dependencies{
		Repository: repository, Executor: core, Guard: localAdmissionGuard{repository: repository}, Clock: ProductionClock{},
		Policy: policy, Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal},
		NodeID: identity.NodeID, RunnerVersion: options.AppVersion,
		InstallationID: identity.InstallationID, EngineBuildID: buildID, CommitmentKey: identity.CommitmentKey,
	})
	if err != nil {
		_ = core.Close()
		if ownedTransport != nil {
			ownedTransport.CloseIdleConnections()
		}
		return nil, err
	}
	return &productionLocalExecutor{host: host, core: core, repository: repository,
		transport: ownedTransport}, nil
}

func (executor *productionLocalExecutor) Connect(ctx context.Context) (jobs.Backend, error) {
	if ctx == nil {
		return nil, jobs.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return jobs.NewLocalBackend(jobs.LocalBackendDependencies{
		Host: executor.host, Repository: executor.repository,
	})
}

func (executor *productionLocalExecutor) Close() error {
	executor.once.Do(func() {
		executor.closeError = errors.Join(executor.host.Close(), executor.core.Close())
		if executor.transport != nil {
			executor.transport.CloseIdleConnections()
		}
	})
	return executor.closeError
}

// Known write failure rejects local admission. Host/Core stop further sends
// on persistence loss; the remote disk reservation policy is configured there.
type localAdmissionGuard struct{ repository *sqlite.Repository }

type localReservation struct{}

func (localReservation) Close() error { return nil }

func (guard localAdmissionGuard) Reserve(ctx context.Context, spec jobcontract.JobSpec) (io.Closer, error) {
	if len(spec.Assets) != 0 {
		return nil, jobhost.ErrInvalid
	}
	if err := guard.repository.CheckWritable(ctx); err != nil {
		return nil, err
	}
	return localReservation{}, nil
}
