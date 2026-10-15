package jobhost

import (
	"context"
	"net/http"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type CoreRepository interface {
	RunRepository
	PerformanceExecutionRepository
}

type CoreDependencies struct {
	Repository CoreRepository
	Runs       runs.Executor
	Transport  http.RoundTripper
	Clock      Clock
}

// Core dispatches every current job kind to its existing execution component.
// Its owner must close JobHost before Core so owner interruption wins before
// execution resources are released.
type Core struct {
	runs        *RunExecutor
	performance *PerformanceExecutor
}

var _ Executor = (*Core)(nil)

func NewCore(dependencies CoreDependencies) (*Core, error) {
	runCore, err := NewRunExecutor(RunExecutorDependencies{
		Repository: dependencies.Repository, Executor: dependencies.Runs, Clock: dependencies.Clock,
	})
	if err != nil {
		return nil, err
	}
	performanceCore, err := NewPerformanceExecutor(PerformanceExecutorDependencies{
		Repository: dependencies.Repository, Transport: dependencies.Transport, Clock: dependencies.Clock,
	})
	if err != nil {
		_ = runCore.Close()
		return nil, err
	}
	return &Core{runs: runCore, performance: performanceCore}, nil
}

func (core *Core) Execute(ctx context.Context, execution Execution) (Outcome, error) {
	if core == nil {
		return Outcome{}, ErrInvalid
	}
	switch execution.Spec.Kind {
	case jobcontract.KindRun, jobcontract.KindComparison:
		return core.runs.Execute(ctx, execution)
	case jobcontract.KindPerformance:
		return core.performance.Execute(ctx, execution)
	default:
		return Outcome{}, ErrInvalid
	}
}

func (core *Core) Settle(ctx context.Context, spec jobcontract.JobSpec, outcome Outcome) error {
	if core == nil {
		return ErrInvalid
	}
	switch spec.Kind {
	case jobcontract.KindRun, jobcontract.KindComparison:
		return core.runs.Settle(ctx, spec, outcome)
	case jobcontract.KindPerformance:
		return core.performance.Settle(ctx, spec, outcome)
	default:
		return ErrInvalid
	}
}

func (core *Core) Close() error {
	if core == nil {
		return nil
	}
	return core.runs.Close()
}
