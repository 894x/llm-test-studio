package runs

import (
	"context"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

type RecoveryRepository interface {
	ListRuns(context.Context) ([]domain.Run, error)
	UpdateRun(context.Context, uint64, domain.Run) error
}

// RecoverInterrupted cancels durable non-terminal runs left by a previous
// process. Restart never resumes paid network traffic implicitly; comparison
// refresh can then deterministically seal any affected comparison.
func RecoverInterrupted(ctx context.Context, repository RecoveryRepository, clock Clock) error {
	if ctx == nil || isNil(repository) || isNil(clock) {
		return ErrInvalid
	}
	values, err := repository.ListRuns(ctx)
	if err != nil {
		return fmt.Errorf("list interrupted runs: %w", err)
	}
	for _, run := range values {
		switch run.Status() {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
		default:
			continue
		}
		at := clock.Now().UTC()
		if !at.After(run.Meta().UpdatedAt) {
			at = run.Meta().UpdatedAt.Add(time.Nanosecond)
		}
		cancelled, err := run.Transition(domain.RunCancelled, at)
		if err != nil {
			return fmt.Errorf("cancel interrupted run %s: %w", run.Meta().ID, err)
		}
		if err := repository.UpdateRun(ctx, run.Meta().Revision, cancelled); err != nil {
			return fmt.Errorf("persist interrupted run %s cancellation: %w", run.Meta().ID, err)
		}
	}
	return nil
}
