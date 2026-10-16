package remotesync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

func watcherNode(index int) Node {
	return Node{Revision: 1, Origin: remoteresults.Origin{
		ConfigurationID: fmt.Sprintf("11111111-1111-4111-8111-%012d", index),
		NodeID:          fmt.Sprintf("22222222-2222-4222-8222-%012d", index),
		InstallationID:  fmt.Sprintf("33333333-3333-4333-8333-%012d", index),
	}}
}

type watcherConnection struct {
	Connection
	node   Node
	listed atomic.Int64
	closed atomic.Int64
	list   func(context.Context, uint64, uint32) (jobhost.JobPage, error)
}

func (connection *watcherConnection) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return jobcontract.RunnerInfo{NodeID: connection.node.Origin.NodeID,
		NodeInstallationID: connection.node.Origin.InstallationID}, nil
}

func (connection *watcherConnection) ListJobs(
	ctx context.Context,
	after uint64,
	limit uint32,
) (jobhost.JobPage, error) {
	connection.listed.Add(1)
	return connection.list(ctx, after, limit)
}

func (connection *watcherConnection) Close() error { connection.closed.Add(1); return nil }

func emptyJobPage(cursor uint64) jobhost.JobPage {
	return jobhost.JobPage{Contract: jobcontract.CurrentContract, Jobs: []jobcontract.Snapshot{},
		NextCursor: fmt.Sprint(cursor)}
}

func TestWatcherIndependentNodesRevisionCancellationAndShutdown(t *testing.T) {
	var sourceMu sync.Mutex
	first, second := watcherNode(1), watcherNode(2)
	nodes := []Node{first, second}
	entered, cancelled := make(chan struct{}), make(chan struct{})
	var enters, opens, closes atomic.Int64
	var independent atomic.Bool
	connect := func(ctx context.Context, node Node) (Connection, error) {
		opens.Add(1)
		if node.Origin == first.Origin && node.Revision == 1 {
			if enters.Add(1) == 1 {
				close(entered)
			}
			<-ctx.Done()
			close(cancelled)
			closes.Add(1)
			return nil, ctx.Err()
		}
		if node.Origin == second.Origin {
			independent.Store(true)
		}
		return &closingWatcherConnection{watcherConnection: watcherConnection{node: node,
			list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
				return emptyJobPage(after), nil
			}}, closed: &closes}, nil
	}
	synchronizer, err := New(Dependencies{Repository: &cursorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer, Connect: connect,
		Nodes: func(context.Context) ([]Node, error) {
			sourceMu.Lock()
			defer sourceMu.Unlock()
			return append([]Node{}, nodes...), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	watcher.refreshEvery, watcher.pollEvery = 5*time.Millisecond, 5*time.Millisecond
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	t.Cleanup(func() { _ = watcher.Close() })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first node did not enter connection")
	}
	waitFor(t, func() bool { return independent.Load() })
	sourceMu.Lock()
	nodes[0].Revision = 2
	sourceMu.Unlock()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("configuration revision did not cancel the old observer")
	}
	waitFor(t, func() bool {
		statuses := watcher.Snapshot().Nodes
		return len(statuses) == 2 && statuses[0].State == "idle" && statuses[1].State == "idle"
	})
	sourceMu.Lock()
	nodes = []Node{first}
	nodes[0].Revision = 2
	sourceMu.Unlock()
	waitFor(t, func() bool { return len(watcher.Snapshot().Nodes) == 1 })
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if opens.Load() != closes.Load() {
		t.Fatal("shutdown did not close every established observation connection")
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
}

type closingWatcherConnection struct {
	watcherConnection
	closed *atomic.Int64
}

func (connection *closingWatcherConnection) Close() error { connection.closed.Add(1); return nil }

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for !predicate() {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("observation condition did not become true")
		case <-timer.C:
		}
	}
}

func TestWatcherCloseBeforeRunAndRejectDuplicateSourcesWithoutConnecting(t *testing.T) {
	synchronizer, err := New(Dependencies{Repository: &cursorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	dependencies := WatcherDependencies{Synchronizer: synchronizer,
		Nodes: func(context.Context) ([]Node, error) { return []Node{}, nil },
		Connect: func(context.Context, Node) (Connection, error) {
			calls.Add(1)
			return nil, errors.New("unexpected connection")
		}}
	watcher, err := NewWatcher(t.Context(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := watcher.Run(); !errors.Is(err, context.Canceled) {
		t.Fatal("closed watcher restarted")
	}
	dependencies.Nodes = func(context.Context) ([]Node, error) {
		node := watcherNode(1)
		duplicate := watcherNode(2)
		duplicate.Origin.InstallationID = node.Origin.InstallationID
		return []Node{node, duplicate}, nil
	}
	watcher, err = NewWatcher(t.Context(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	waitFor(t, func() bool { return watcher.Snapshot().ErrorCode != "" })
	if calls.Load() != 0 || len(watcher.Snapshot().Nodes) != 0 {
		t.Fatal("duplicate installation started observation")
	}
	_ = watcher.Close()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type missingMirrorRepository struct{ remoteresults.Repository }

func (missingMirrorRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	return remoteresults.Mirror{}, remoteresults.ErrNotFound
}

type blockedObservation struct {
	watcherConnection
	entered chan struct{}
	active  *atomic.Int64
	maximum *atomic.Int64
}

func (connection *blockedObservation) ReadEvents(
	ctx context.Context,
	_ runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	active := connection.active.Add(1)
	defer connection.active.Add(-1)
	for maximum := connection.maximum.Load(); maximum < active; maximum = connection.maximum.Load() {
		if connection.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	connection.entered <- struct{}{}
	<-ctx.Done()
	return jobhost.EventPage{}, ctx.Err()
}

func TestWatcherBoundsConcurrentFullSyncAndCancelsGateWaiters(t *testing.T) {
	synchronizer, err := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	nodes := []Node{}
	for index := range maxConcurrentSyncs + 1 {
		nodes = append(nodes, watcherNode(index+1))
	}
	entered := make(chan struct{}, len(nodes))
	var active, maximum atomic.Int64
	var opened, closed atomic.Int64
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes: func(context.Context) ([]Node, error) { return nodes, nil },
		Connect: func(_ context.Context, node Node) (Connection, error) {
			opened.Add(1)
			at := time.Now().UTC()
			snapshot := jobcontract.Snapshot{Receipt: jobcontract.Receipt{
				Contract: jobcontract.CurrentContract, JobID: node.Origin.ConfigurationID,
				Kind: jobcontract.KindPerformance, AcceptedAt: at,
				Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: node.Origin.NodeID,
					InstallationID: node.Origin.InstallationID, EngineBuildID: strings.Repeat("b", 64),
					RunnerVersion: "watcher-test", OS: "linux", Arch: "amd64"}, Status: domain.RunQueued, Revision: 1,
				RunIDs: []string{}, ReportIDs: []string{node.Origin.NodeID},
			}, LatestSequence: 1, UpdatedAt: at}
			connection := &blockedObservation{watcherConnection: watcherConnection{node: node,
				list: func(context.Context, uint64, uint32) (jobhost.JobPage, error) {
					return jobhost.JobPage{Contract: jobcontract.CurrentContract,
						Jobs: []jobcontract.Snapshot{snapshot}, NextCursor: "1"}, nil
				}}, entered: entered, active: &active, maximum: &maximum}
			return &countedObservation{blockedObservation: connection, closed: &closed}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	watcher.attemptTimeout = time.Hour
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	t.Cleanup(func() { _ = watcher.Close() })
	for range maxConcurrentSyncs {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("full synchronization slots did not enter observation")
		}
	}
	waitFor(t, func() bool { return opened.Load() == int64(len(nodes)) })
	select {
	case <-entered:
		t.Fatal("fifth node bypassed the synchronization budget")
	case <-time.After(40 * time.Millisecond):
	}
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if maximum.Load() != maxConcurrentSyncs || active.Load() != 0 || closed.Load() != int64(len(nodes)) {
		t.Fatal("sync budget or shutdown resource accounting was violated")
	}
}

type countedObservation struct {
	*blockedObservation
	closed *atomic.Int64
}

func (connection *countedObservation) Close() error { connection.closed.Add(1); return nil }

type failingJobObservation struct {
	watcherConnection
	observe func(string)
}

func (connection *failingJobObservation) ReadEvents(
	_ context.Context,
	query runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	connection.observe(query.JobID)
	return jobhost.EventPage{}, errors.New("job observation fixture failed")
}

func queuedJob(node Node, index int) jobcontract.Snapshot {
	at := time.Now().UTC()
	return jobcontract.Snapshot{Receipt: jobcontract.Receipt{
		Contract: jobcontract.CurrentContract, JobID: fmt.Sprintf("44444444-4444-4444-8444-%012d", index),
		Kind: jobcontract.KindPerformance, AcceptedAt: at,
		Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: node.Origin.NodeID,
			InstallationID: node.Origin.InstallationID, EngineBuildID: strings.Repeat("b", 64),
			RunnerVersion: "watcher-test", OS: "linux", Arch: "amd64"}, Status: domain.RunQueued, Revision: 1, RunIDs: []string{},
		ReportIDs: []string{fmt.Sprintf("55555555-5555-4555-8555-%012d", index)},
	}, LatestSequence: 1, UpdatedAt: at}
}

func TestWatcherContinuesPendingPageAcrossFailuresAndWrapsAfterTail(t *testing.T) {
	node := watcherNode(1)
	roots := []jobcontract.Snapshot{}
	for index := range discoveryPageSize + 7 {
		roots = append(roots, queuedJob(node, index+1))
	}
	listed := []uint64{}
	observed := map[string]int{}
	var opened, closed, observations int
	synchronizer, err := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{Synchronizer: synchronizer,
		Nodes: func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) {
			opened++
			connection := &failingJobObservation{watcherConnection: watcherConnection{node: node,
				list: func(_ context.Context, after uint64, limit uint32) (jobhost.JobPage, error) {
					listed = append(listed, after)
					end := min(int(after)+int(limit), len(roots))
					return jobhost.JobPage{Contract: jobcontract.CurrentContract,
						Jobs: roots[int(after):end], NextCursor: fmt.Sprint(end), HasMore: end < len(roots)}, nil
				}}, observe: func(id string) {
				observed[id]++
				observations++
			}}
			return &countedFailingObservation{failingJobObservation: connection, closed: &closed}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	scan := resetDiscovery()
	// Every job deliberately fails after its own Probe. This fixture checks
	// discovery fairness, not journal import semantics or recovery of execution.
	for len(observed) < len(roots) {
		before := observations
		if _, err := watcher.attempt(t.Context(), node, &scan); err == nil {
			t.Fatal("fixture observation failure disappeared")
		}
		if observations-before > jobsPerAttempt {
			t.Fatal("attempt exceeded its per-page work budget")
		}
		if opened > 20 {
			t.Fatal("a failed job stalled subsequent roots")
		}
	}
	if len(listed) != 2 || listed[0] != 0 || listed[1] != discoveryPageSize || scan.cursor != 0 {
		t.Fatal("pending index did not continue through the second page and tail")
	}
	for _, root := range roots {
		if observed[root.Receipt.JobID] != 1 {
			t.Fatal("page boundary repeated or omitted a root")
		}
	}
	later := queuedJob(node, len(roots)+1)
	roots = append(roots, later)
	for observed[later.Receipt.JobID] == 0 {
		before := observations
		_, _ = watcher.attempt(t.Context(), node, &scan)
		if observations-before > jobsPerAttempt {
			t.Fatal("wrapped attempt exceeded its work budget")
		}
		if opened > 40 {
			t.Fatal("scan did not rediscover the new tail job")
		}
	}
	if len(listed) != 4 || listed[2] != 0 || listed[3] != discoveryPageSize || opened != closed {
		t.Fatal("wrapped scan changed its cursor or retained an old connection")
	}
}

type countedFailingObservation struct {
	*failingJobObservation
	closed *int
}

func (connection *countedFailingObservation) Close() error {
	(*connection.closed)++
	return nil
}
