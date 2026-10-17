package jobs

import (
	"context"
	"sync"
	"time"
)

const controlsPerPass = 4

type ControlDeliveryDependencies struct {
	Repository ControlRepository
	Deliver    func(context.Context, string) (ControlRecord, error)
}

type ControlDeliverySnapshot struct {
	Running   bool
	ErrorCode string
	CheckedAt time.Time
}

// ControlDeliveryWatcher replays only already-persisted control instructions.
// It cannot create a command, change its revision or submit paid work.
type ControlDeliveryWatcher struct {
	repository ControlRepository
	deliver    func(context.Context, string) (ControlRecord, error)
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	wait       sync.WaitGroup
	running    bool
	closed     bool
	status     ControlDeliverySnapshot
	pollEvery  time.Duration
}

func NewControlDeliveryWatcher(
	ctx context.Context,
	dependencies ControlDeliveryDependencies,
) (*ControlDeliveryWatcher, error) {
	validPorts := !nilPort(dependencies.Repository) && dependencies.Deliver != nil
	if ctx == nil || !validPorts {
		return nil, ErrInvalid
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &ControlDeliveryWatcher{repository: dependencies.Repository, deliver: dependencies.Deliver,
		ctx: lifetime, cancel: cancel, pollEvery: 5 * time.Second}, nil
}

func (watcher *ControlDeliveryWatcher) Run() error {
	watcher.mu.Lock()
	if watcher.closed || watcher.ctx.Err() != nil {
		watcher.mu.Unlock()
		return context.Canceled
	}
	if watcher.running {
		watcher.mu.Unlock()
		return ErrConflict
	}
	watcher.running = true
	watcher.wait.Add(1)
	watcher.mu.Unlock()
	defer watcher.wait.Done()
	defer watcher.cancel()
	ticker := time.NewTicker(watcher.pollEvery)
	defer ticker.Stop()
	cursor := ""
	for {
		next, err := watcher.pass(cursor)
		if watcher.ctx.Err() != nil {
			return watcher.ctx.Err()
		}
		cursor = next
		watcher.mu.Lock()
		watcher.status.CheckedAt = time.Now().UTC()
		watcher.status.ErrorCode = ""
		if err != nil {
			watcher.status.ErrorCode = "control_delivery_pending"
		}
		watcher.mu.Unlock()
		select {
		case <-watcher.ctx.Done():
			return watcher.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (watcher *ControlDeliveryWatcher) Close() error {
	watcher.mu.Lock()
	watcher.closed = true
	watcher.cancel()
	watcher.mu.Unlock()
	watcher.wait.Wait()
	return nil
}

func (watcher *ControlDeliveryWatcher) Snapshot() ControlDeliverySnapshot {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	result := watcher.status
	result.Running = watcher.running && !watcher.closed && watcher.ctx.Err() == nil
	return result
}

func (watcher *ControlDeliveryWatcher) pass(after string) (string, error) {
	ctx, cancel := context.WithTimeout(watcher.ctx, 5*time.Second)
	page, err := watcher.repository.ListPendingControls(ctx, after, controlsPerPass)
	cancel()
	if err != nil {
		return after, err
	}
	if !validControlDeliveryPage(page, after) {
		return after, ErrInvalid
	}
	ctx, cancel = context.WithTimeout(watcher.ctx, 30*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	results := make(chan error, len(page.Records))
	for _, record := range page.Records {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := watcher.deliver(ctx, id)
			results <- err
		}(record.Command.CommandID)
	}
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil {
			err = result
		}
	}
	if !page.HasMore {
		return "", err
	}
	return page.NextCursor, err
}

func validControlDeliveryPage(page ControlPage, after string) bool {
	validCount := page.Records != nil && len(page.Records) <= controlsPerPass
	if !validCount || page.HasMore && len(page.Records) == 0 {
		return false
	}
	previous := after
	for _, record := range page.Records {
		validPending := record.State == ControlPending && record.Validate() == nil
		if !validPending || record.Command.CommandID <= previous {
			return false
		}
		previous = record.Command.CommandID
	}
	return page.NextCursor == previous
}
