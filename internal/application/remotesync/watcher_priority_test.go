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

func emptyPriorityJobs(_ context.Context, _ remoteresults.Origin, after string, _ uint32) (remoteresults.PriorityPage, error) {
	return remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}, nil
}

func TestWatcherRequiresTheCurrentPrioritySourcePort(t *testing.T) {
	synchronizer, _ := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	_, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return nil, errors.New("unused") },
	})
	if !errors.Is(err, remoteresults.ErrInvalid) {
		t.Fatal("missing priority source silently retained the superseded discovery-only path")
	}
}

func TestWatcherSlowPriorityJobDoesNotBlockPeersAndIsRevisited(t *testing.T) {
	node := watcherNode(1)
	owners, roots := []jobcontract.Snapshot{}, []jobcontract.Snapshot{}
	snapshots := map[string]jobcontract.Snapshot{}
	for index := range 5 {
		snapshot := queuedJob(node, 900+index)
		owners = append(owners, snapshot)
		snapshots[snapshot.Receipt.JobID] = snapshot
	}
	for index := range discoveryPageSize {
		roots = append(roots, queuedJob(node, index+1))
	}
	var mu sync.Mutex
	seen := map[string]int{}
	peers := make(chan string, jobsPerAttempt)
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			return jobhost.JobPage{Contract: jobcontract.CurrentContract, Jobs: roots, NextCursor: "25"}, nil
		}}, snapshots: snapshots, events: func(ctx context.Context, id string) error {
		mu.Lock()
		seen[id]++
		count := seen[id]
		mu.Unlock()
		if id == owners[0].Receipt.JobID && count == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		if count == 1 {
			select {
			case peers <- id:
			default:
			}
		}
		return errors.New("deliberate observation failure")
	}}
	synchronizer, _ := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
		PriorityJobs: func(_ context.Context, _ remoteresults.Origin, after string, limit uint32) (remoteresults.PriorityPage, error) {
			page := remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}
			for _, snapshot := range owners {
				id := snapshot.Receipt.JobID
				if id <= after {
					continue
				}
				if len(page.JobIDs) == int(limit) {
					page.HasMore = true
					break
				}
				page.JobIDs, page.NextCursor = append(page.JobIDs, id), id
			}
			return page, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	scan := resetDiscovery()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	finished := make(chan error, 1)
	go func() { _, err := watcher.attempt(ctx, node, &scan); finished <- err }()
	for range jobsPerAttempt - 1 {
		select {
		case <-peers:
		case <-time.After(time.Second):
			t.Fatal("a slow priority job blocked the other priority or shared jobs")
		}
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation: %v", err)
	}
	for range 3 {
		_, _ = watcher.attempt(t.Context(), node, &scan)
	}
	for _, owner := range owners {
		if seen[owner.Receipt.JobID] == 0 {
			t.Fatal("failed priority job stopped the bounded ID cursor from advancing")
		}
	}
	if seen[owners[0].Receipt.JobID] != 2 {
		t.Fatal("a canceled job was lost instead of revisited after the priority tail")
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
		_, err = watcher.attempt(t.Context(), node, &scan)
		_ = watcher.Close()
		if !errors.Is(err, remoteresults.ErrInvalid) || shared.Load() != 1 || scan.priorityCursor != "" {
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
						Jobs: []jobcontract.Snapshot{own, foreign}, NextCursor: "2"}, nil
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
			_, _ = watcher.attempt(t.Context(), node, &scan)
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

func (connection *priorityObservation) ReadEvents(ctx context.Context, query runnerclient.EventQuery) (jobhost.EventPage, error) {
	return jobhost.EventPage{}, connection.events(ctx, query.JobID)
}

func TestWatcherPrioritizesLocalSubmissionsWithoutStarvingSharedDiscovery(t *testing.T) {
	node := watcherNode(1)
	roots := []jobcontract.Snapshot{}
	for index := range discoveryPageSize * 3 {
		roots = append(roots, queuedJob(node, index+1))
	}
	first, second := queuedJob(node, 900), queuedJob(node, 901)
	var mu sync.Mutex
	seen := map[string]int{}
	synchronizer, err := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	connection := &priorityObservation{
		watcherConnection: watcherConnection{node: node,
			list: func(_ context.Context, after uint64, limit uint32) (jobhost.JobPage, error) {
				end := min(int(after)+int(limit), len(roots))
				return jobhost.JobPage{Contract: jobcontract.CurrentContract,
					Jobs: roots[int(after):end], NextCursor: fmt.Sprint(end), HasMore: end < len(roots)}, nil
			}},
		snapshots: map[string]jobcontract.Snapshot{first.Receipt.JobID: first, second.Receipt.JobID: second},
		events: func(_ context.Context, id string) error {
			mu.Lock()
			seen[id]++
			mu.Unlock()
			return errors.New("failed job must not block later jobs")
		},
	}
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
		PriorityJobs: func(_ context.Context, origin remoteresults.Origin, after string, limit uint32) (remoteresults.PriorityPage, error) {
			if origin != node.Origin || after != "" || limit != priorityPerAttempt {
				t.Error("priority query changed its fixed scope or bounded page")
			}
			return remoteresults.PriorityPage{JobIDs: []string{first.Receipt.JobID, second.Receipt.JobID},
				NextCursor: second.Receipt.JobID}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	scan := resetDiscovery()
	for pass := range 2 {
		_, _ = watcher.attempt(t.Context(), node, &scan)
		if seen[first.Receipt.JobID] != pass+1 || seen[second.Receipt.JobID] != pass+1 {
			t.Fatal("new local submissions waited behind the shared history")
		}
		for _, root := range roots[:(pass+1)*(jobsPerAttempt-priorityPerAttempt)] {
			if seen[root.Receipt.JobID] != 1 {
				t.Fatal("priority jobs starved or repeated the reserved shared discovery share")
			}
		}
	}
}
