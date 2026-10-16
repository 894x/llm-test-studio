package remotesync

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
)

func TestWatcherRetriesSourceTimeoutAndSuspendsOldObserversUntilRecovery(t *testing.T) {
	synchronizer, err := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	node := watcherNode(1)
	var unavailable atomic.Bool
	unavailable.Store(true)
	var opened, closed atomic.Int64
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		PriorityJobs: emptyPriorityJobs,
		Nodes: func(context.Context) ([]Node, error) {
			if unavailable.Load() {
				return nil, context.DeadlineExceeded
			}
			return []Node{node}, nil
		},
		Connect: func(context.Context, Node) (Connection, error) {
			opened.Add(1)
			return &closingWatcherConnection{watcherConnection: watcherConnection{node: node,
				list: func(ctx context.Context, _ uint64, _ uint32) (jobhost.JobPage, error) {
					<-ctx.Done()
					return jobhost.JobPage{}, ctx.Err()
				}}, closed: &closed}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	watcher.refreshEvery, watcher.pollEvery = 5*time.Millisecond, 5*time.Millisecond
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	t.Cleanup(func() { _ = watcher.Close() })
	waitFor(t, func() bool { return watcher.Snapshot().ErrorCode == "remote_sync_configuration_failed" })
	if snapshot := watcher.Snapshot(); !snapshot.Running || len(snapshot.Nodes) != 0 || opened.Load() != 0 {
		t.Fatal("source timeout stopped the supervisor or used an unvalidated source")
	}
	select {
	case err := <-done:
		t.Fatalf("source timeout terminated automatic synchronization: %v", err)
	default:
	}
	unavailable.Store(false)
	waitFor(t, func() bool {
		snapshot := watcher.Snapshot()
		return snapshot.ErrorCode == "" && len(snapshot.Nodes) == 1 && opened.Load() == 1
	})
	unavailable.Store(true)
	waitFor(t, func() bool {
		snapshot := watcher.Snapshot()
		return snapshot.ErrorCode != "" && len(snapshot.Nodes) == 0 && closed.Load() == 1
	})
	unavailable.Store(false)
	waitFor(t, func() bool { return watcher.Snapshot().ErrorCode == "" && opened.Load() == 2 })
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if opened.Load() != closed.Load() {
		t.Fatal("configuration recovery retained a superseded connection")
	}
}
