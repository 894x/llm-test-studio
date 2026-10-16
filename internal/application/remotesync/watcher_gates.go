package remotesync

import "context"

func takeSlot(ctx context.Context, gate chan struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func acquireSlots(ctx context.Context, gates ...chan struct{}) (func(), error) {
	for index, gate := range gates {
		if err := takeSlot(ctx, gate); err != nil {
			for _, held := range gates[:index] {
				<-held
			}
			return nil, err
		}
	}
	return func() {
		for _, gate := range gates {
			<-gate
		}
	}, nil
}

func (watcher *Watcher) acquireFacts(ctx context.Context, lane scanLane) (func(), error) {
	if lane == priorityLane {
		return acquireSlots(ctx, watcher.factGate, watcher.gate)
	}
	// A queued background worker never owns global or reserved fact capacity.
	return acquireSlots(
		ctx,
		watcher.backgroundFactGate,
		watcher.factGate,
		watcher.gate,
	)
}
