package runs

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

// saveTerminal requires control.mu. Read after a failed write to reconcile an
// ambiguous commit or a cancellation before retrying the revision-aware update.
func (service *Service) saveTerminal(ctx context.Context, control *runControl, terminal domain.RunStatus, failure domain.RunFailure) error {
	if isTerminal(control.run.Status()) {
		return nil
	}
	var err error
	if terminal == domain.RunFailed {
		err = service.fail(ctx, control, failure)
	} else {
		err = service.transition(ctx, control, terminal)
	}
	if err == nil {
		return nil
	}
	stored, readErr := service.repository.GetRun(ctx, control.run.Meta().ID)
	if readErr == nil && stored.Meta().ID == control.run.Meta().ID && stored.Meta().Revision >= control.run.Meta().Revision {
		control.run = stored
		if isTerminal(stored.Status()) {
			return nil
		}
	}
	return err
}

// Retain ownership of this finished execution until its terminal state is
// durable. Waits release control.mu so cancellation and shutdown stay usable.
// Shutdown leaves any outstanding write for startup's RecoverInterrupted.
func (service *Service) recoverTerminal(control *runControl, terminal domain.RunStatus, failure domain.RunFailure) error {
	delay := 25 * time.Millisecond
	for {
		timer := time.NewTimer(delay)
		select {
		case <-service.recoveryContext.Done():
			timer.Stop()
			return service.recoveryContext.Err()
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(service.recoveryContext, 5*time.Second)
		control.mu.Lock()
		err := service.saveTerminal(ctx, control, terminal, failure)
		control.mu.Unlock()
		cancel()
		if err == nil {
			return nil
		}
		delay = min(delay*2, time.Second)
	}
}

func isTerminal(status domain.RunStatus) bool {
	return status == domain.RunCompleted || status == domain.RunFailed || status == domain.RunCancelled
}
