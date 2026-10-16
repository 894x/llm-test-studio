package remotesync

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

func emptyPriorityJobs(
	_ context.Context,
	_ remoteresults.Origin,
	after string,
	_ uint32,
) (remoteresults.PriorityPage, error) {
	return remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}, nil
}

func TestWatcherRequiresTheCurrentPrioritySourcePort(t *testing.T) {
	synchronizer, _ := New(Dependencies{
		Repository: missingMirrorRepository{}, Documents: unusedCache{},
	})
	_, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return nil, errors.New("unused") },
	})
	if !errors.Is(err, remoteresults.ErrInvalid) {
		t.Fatal("missing priority source silently retained the superseded discovery-only path")
	}
}

func TestWatcherSlowPriorityJobDoesNotBlockLaterTicks(t *testing.T) {
	node := watcherNode(1)
	slow, later := queuedJob(node, 900), queuedJob(node, 901)
	entered, progressed := make(chan struct{}), make(chan struct{}, 1)
	var once sync.Once
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			return emptyJobPage(after), nil
		}},
		snapshots: map[string]jobcontract.Snapshot{slow.Receipt.JobID: slow, later.Receipt.JobID: later},
		events: func(ctx context.Context, id string) error {
			if id == slow.Receipt.JobID {
				once.Do(func() { close(entered) })
				<-ctx.Done()
				return ctx.Err()
			}
			select {
			case progressed <- struct{}{}:
			default:
			}
			return errors.New("fixture observation")
		}}
	synchronizer, _ := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	var pages atomic.Int32
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
		PriorityJobs: func(
			_ context.Context,
			_ remoteresults.Origin,
			_ string,
			_ uint32,
		) (remoteresults.PriorityPage, error) {
			id := slow.Receipt.JobID
			if pages.Add(1) > 1 {
				id = later.Receipt.JobID
			}
			return remoteresults.PriorityPage{JobIDs: []string{id}, NextCursor: id}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	watcher.pollEvery = 5 * time.Millisecond
	scan := resetDiscovery()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := watcher.attempt(
			ctx,
			node,
			&scan,
			nil,
		)
		done <- err
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("slow priority did not start")
	}
	select {
	case <-progressed:
	case <-time.After(time.Second):
		t.Fatal("slow priority blocked the next tick")
	}
}

type gatedMirrorRepository struct {
	remoteresults.Repository
	entered chan struct{}
	active  atomic.Int32
}

func (repository *gatedMirrorRepository) GetRemoteMirror(ctx context.Context, _ string) (remoteresults.Mirror, error) {
	repository.active.Add(1)
	defer repository.active.Add(-1)
	repository.entered <- struct{}{}
	<-ctx.Done()
	return remoteresults.Mirror{}, ctx.Err()
}

func TestWatcherGlobalBudgetIncludesFullMirrorValidation(t *testing.T) {
	repository := &gatedMirrorRepository{entered: make(chan struct{}, maxConcurrentSyncs*2)}
	synchronizer, _ := New(Dependencies{Repository: repository, Documents: unusedCache{}})
	node := watcherNode(1)
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		PriorityJobs: emptyPriorityJobs, Nodes: func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return nil, errors.New("unused") }})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan syncResult, maxConcurrentSyncs*2)
	for index := range maxConcurrentSyncs * 2 {
		snapshot := queuedJob(node, index+1)
		go func() { finished <- watcher.syncJob(ctx, node, nil, snapshot.Receipt.JobID, &snapshot) }()
	}
	for range maxConcurrentSyncs {
		select {
		case <-repository.entered:
		case <-time.After(time.Second):
			t.Fatal("mirror validation did not enter its global slots")
		}
	}
	select {
	case <-repository.entered:
		t.Fatal("full mirror/spec decoding bypassed the global synchronization budget")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	for range maxConcurrentSyncs * 2 {
		<-finished
	}
	if repository.active.Load() != 0 || len(watcher.gate) != 0 {
		t.Fatal("cancellation did not join validation workers and gate waiters")
	}
}

func TestWatcherRejectsInvalidPriorityHintsAndStillDiscoversSharedJobs(t *testing.T) {
	node := watcherNode(1)
	first, second := queuedJob(node, 900), queuedJob(node, 901)
	for _, page := range []remoteresults.PriorityPage{
		{JobIDs: []string{}, HasMore: true},
		{JobIDs: []string{first.Receipt.JobID, first.Receipt.JobID}, NextCursor: first.Receipt.JobID},
		{JobIDs: []string{second.Receipt.JobID, first.Receipt.JobID}, NextCursor: first.Receipt.JobID},
		{JobIDs: []string{first.Receipt.JobID}, NextCursor: second.Receipt.JobID},
		{JobIDs: []string{"not-an-id"}, NextCursor: "not-an-id"},
	} {
		synchronizer, _ := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
		var shared atomic.Int32
		connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
			list: func(context.Context, uint64, uint32) (jobhost.JobPage, error) {
				return jobhost.JobPage{Contract: jobcontract.CurrentContract,
					Jobs: []jobcontract.Snapshot{queuedJob(node, 1)}, NextCursor: "1"}, nil
			}}, events: func(context.Context, string) error {
			shared.Add(1)
			return errors.New("shared observation fixture")
		}}
		watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
			Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
			Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
			PriorityJobs: func(context.Context, remoteresults.Origin, string, uint32) (remoteresults.PriorityPage, error) {
				return page, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		scan := resetDiscovery()
		if _, err := watcher.priorityPage(t.Context(), node, ""); !errors.Is(err, remoteresults.ErrInvalid) {
			t.Fatal("invalid priority source page was accepted")
		}
		_, err = oneObservationTick(
			t,
			watcher,
			node,
			&scan,
		)
		_ = watcher.Close()
		if err == nil || shared.Load() != 1 || scan.priority.cursor != "" {
			t.Fatal("invalid priority page changed its cursor or stalled shared discovery")
		}
	}
}

func TestWatcherDeduplicatesQueuesAndRejectsForeignPriorityExecution(t *testing.T) {
	for _, wrongOwner := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongOwner), func(t *testing.T) {
			node := watcherNode(1)
			own, foreign := queuedJob(node, 900), queuedJob(node, 1)
			observed := own
			if wrongOwner {
				observed.Receipt.Origin.InstallationID = watcherNode(2).Origin.InstallationID
			}
			var mu sync.Mutex
			seen := map[string]int{}
			synchronizer, _ := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
			connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
				list: func(context.Context, uint64, uint32) (jobhost.JobPage, error) {
					return jobhost.JobPage{Contract: jobcontract.CurrentContract,
						Jobs: []jobcontract.Snapshot{foreign}, NextCursor: "2"}, nil
				}}, snapshots: map[string]jobcontract.Snapshot{own.Receipt.JobID: observed},
				events: func(_ context.Context, id string) error {
					mu.Lock()
					seen[id]++
					mu.Unlock()
					return errors.New("observation fixture")
				}}
			watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
				Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
				Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
				PriorityJobs: func(context.Context, remoteresults.Origin, string, uint32) (remoteresults.PriorityPage, error) {
					return remoteresults.PriorityPage{JobIDs: []string{own.Receipt.JobID}, NextCursor: own.Receipt.JobID}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer watcher.Close()
			scan := resetDiscovery()
			_, _ = oneObservationTick(
				t,
				watcher,
				node,
				&scan,
			)
			wantOwn := 1
			if wrongOwner {
				wantOwn = 0
			}
			if seen[own.Receipt.JobID] != wantOwn || seen[foreign.Receipt.JobID] != 1 {
				t.Fatal("queue deduplication or strict remote execution ownership was lost")
			}
		})
	}
}

type priorityObservation struct {
	watcherConnection
	snapshots map[string]jobcontract.Snapshot
	events    func(context.Context, string) error
}

func (connection *priorityObservation) GetJob(_ context.Context, id string) (jobcontract.Snapshot, error) {
	return connection.snapshots[id], nil
}

func (connection *priorityObservation) ReadEvents(
	ctx context.Context,
	query runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	return jobhost.EventPage{}, connection.events(ctx, query.JobID)
}
