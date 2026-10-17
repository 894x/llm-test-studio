package jobs

import (
	"context"
	"sync"
	"time"
)

const confirmationsPerPass = 4

type ConfirmationDependencies struct {
	Repository SubmissionRepository
	Reconcile  func(context.Context, string) (SubmissionRecord, error)
}

type ConfirmationSnapshot struct {
	Running   bool      `json:"running"`
	ErrorCode string    `json:"error_code"`
	CheckedAt time.Time `json:"checked_at"`
}

// ConfirmationWatcher only queries durable pending intents. Its port cannot
// submit a job or obtain channel secrets. A failed destination does not stop
// the page cursor from reaching other pending submissions.
type ConfirmationWatcher struct {
	repository SubmissionRepository
	reconcile  func(context.Context, string) (SubmissionRecord, error)
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	wait       sync.WaitGroup
	running    bool
	closed     bool
	status     ConfirmationSnapshot
	pollEvery  time.Duration
}

func NewConfirmationWatcher(
	ctx context.Context,
	dependencies ConfirmationDependencies,
) (*ConfirmationWatcher, error) {
	validDependencies := !nilPort(dependencies.Repository) && dependencies.Reconcile != nil
	if ctx == nil || !validDependencies {
		return nil, ErrInvalid
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &ConfirmationWatcher{
		repository: dependencies.Repository, reconcile: dependencies.Reconcile,
		ctx: lifetime, cancel: cancel, pollEvery: 5 * time.Second,
	}, nil
}

func (watcher *ConfirmationWatcher) Run() error {
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
			watcher.status.ErrorCode = "submission_reconciliation_failed"
		}
		watcher.mu.Unlock()
		select {
		case <-watcher.ctx.Done():
			return watcher.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (watcher *ConfirmationWatcher) Close() error {
	watcher.mu.Lock()
	watcher.closed = true
	watcher.cancel()
	watcher.mu.Unlock()
	watcher.wait.Wait()
	return nil
}

func (watcher *ConfirmationWatcher) Snapshot() ConfirmationSnapshot {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	result := watcher.status
	result.Running = watcher.running && !watcher.closed && watcher.ctx.Err() == nil
	return result
}

func (watcher *ConfirmationWatcher) pass(after string) (string, error) {
	ctx, cancel := context.WithTimeout(watcher.ctx, 5*time.Second)
	page, err := watcher.repository.ListPendingSubmissions(ctx, after, confirmationsPerPass)
	cancel()
	if err != nil {
		return after, err
	}
	if !validConfirmationPage(page, after) {
		return after, ErrInvalid
	}
	ctx, cancel = context.WithTimeout(watcher.ctx, 30*time.Second)
	defer cancel()
	var wait sync.WaitGroup
	results := make(chan error, len(page.Records))
	for _, record := range page.Records {
		// The outbox page also includes prepared work that has never been
		// sent. Keep traversing its key, without querying or submitting it.
		if record.State == SubmissionPrepared {
			continue
		}
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := watcher.reconcile(ctx, id)
			results <- err
		}(record.Prepared.Spec.JobID)
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

func validConfirmationPage(page SubmissionPage, after string) bool {
	if len(page.Records) > confirmationsPerPass || (page.HasMore && len(page.Records) == 0) {
		return false
	}
	previous := after
	for _, record := range page.Records {
		unconfirmed := record.State == SubmissionPrepared || record.State == SubmissionPending
		validRecord := unconfirmed && record.Validate() == nil
		if !validRecord || record.Prepared.Spec.JobID <= previous {
			return false
		}
		previous = record.Prepared.Spec.JobID
	}
	return page.NextCursor == previous
}
