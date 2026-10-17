package runs

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

// saveTerminal requires control.mu. Read after a failed write to reconcile an
// ambiguous commit or a cancellation before retrying the revision-aware update.
func (service *Host) saveTerminal(
	ctx context.Context,
	control *runControl,
	terminal domain.RunStatus,
	failure domain.RunFailure,
) error {
	storeContext, cancel := context.WithTimeout(ctx, runStorageTimeout)
	defer cancel()
	if isTerminal(control.run.Status()) {
		return nil
	}
	var err error
	if terminal == domain.RunFailed {
		err = service.fail(storeContext, control, failure)
	} else {
		err = service.transition(storeContext, control, terminal)
	}
	if err == nil {
		return nil
	}
	stored, readErr := service.repository.GetRun(storeContext, control.run.Meta().ID)
	validStoredRun := readErr == nil && stored.Meta().ID == control.run.Meta().ID
	if validStoredRun && stored.Meta().Revision >= control.run.Meta().Revision {
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
func (service *Host) recoverTerminal(control *runControl, terminal domain.RunStatus, failure domain.RunFailure) error {
	delay := 25 * time.Millisecond
	for {
		timer := time.NewTimer(delay)
		select {
		case <-control.recoveryContext.Done():
			timer.Stop()
			return control.recoveryContext.Err()
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(control.recoveryContext, 5*time.Second)
		control.mu.Lock()
		if control.terminating {
			control.mu.Unlock()
			cancel()
			return context.Canceled
		}
		err := service.saveTerminal(
			ctx,
			control,
			terminal,
			failure,
		)
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
