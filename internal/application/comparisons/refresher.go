package comparisons

import (
	"context"

	"github.com/894x/llm-test-studio/internal/domain"
)

type ExecutionRepository interface {
	GetComparison(context.Context, string) (domain.Comparison, error)
	UpdateComparison(context.Context, uint64, domain.Comparison) error
}

type RunReader interface {
	GetRun(context.Context, string) (domain.Run, error)
}

type RefresherDependencies struct {
	Repository ExecutionRepository
	Runs       RunReader
	Clock      Clock
}

// Refresher advances a complete group's state from execution facts alone.
// Both the node owner and desktop queries use this single aggregation rule.
type Refresher struct {
	repository ExecutionRepository
	runs       RunReader
	clock      Clock
}

func NewRefresher(dependencies RefresherDependencies) (*Refresher, error) {
	invalid := nilInterface(dependencies.Repository) || nilInterface(dependencies.Runs) || nilInterface(dependencies.Clock)
	if invalid {
		return nil, ErrInvalid
	}
	return &Refresher{
		repository: dependencies.Repository, runs: dependencies.Runs, clock: dependencies.Clock,
	}, nil
}

func (service *Refresher) Refresh(ctx context.Context, comparisonID string) (domain.Comparison, error) {
	if service == nil || ctx == nil || !domain.IsUUID(comparisonID) {
		return domain.Comparison{}, ErrInvalid
	}
	comparison, err := service.repository.GetComparison(ctx, comparisonID)
	if err != nil || comparison.Status() != domain.ComparisonRunning {
		return comparison, err
	}
	terminal := domain.ComparisonCompleted
	for _, item := range comparison.Runs() {
		run, runErr := service.runs.GetRun(ctx, item.RunID)
		if runErr != nil {
			return domain.Comparison{}, runErr
		}
		snapshot := run.Snapshot()
		matchesPlan := snapshot.Plan == comparison.Plan()
		matchesModel := snapshot.Model.EntityRevisionRef == comparison.Model()
		matchesChannel := snapshot.Channel.EntityRevisionRef == item.Channel
		if !matchesPlan || !matchesModel || !matchesChannel {
			return domain.Comparison{}, ErrNotReady
		}
		switch run.Status() {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
			return comparison, nil
		case domain.RunFailed:
			terminal = domain.ComparisonFailed
		case domain.RunCancelled:
			if terminal != domain.ComparisonFailed {
				terminal = domain.ComparisonCancelled
			}
		case domain.RunCompleted:
		default:
			return domain.Comparison{}, ErrNotReady
		}
	}
	updated, err := comparison.Transition(terminal, service.clock.Now())
	if err != nil {
		return domain.Comparison{}, err
	}
	if err := service.repository.UpdateComparison(ctx, comparison.Meta().Revision, updated); err != nil {
		// Concurrent refreshers return the same durable terminal winner.
		current, reloadErr := service.repository.GetComparison(ctx, comparisonID)
		if reloadErr == nil && current.Status() != domain.ComparisonRunning {
			return current, nil
		}
		return domain.Comparison{}, err
	}
	return updated, nil
}
