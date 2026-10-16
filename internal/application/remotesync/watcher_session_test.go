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
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func oneObservationTick(t *testing.T, watcher *Watcher, node Node, scan *discovery) (bool, error) {
	t.Helper()
	watcher.pollEvery = time.Hour
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	return watcher.attempt(
		ctx,
		node,
		scan,
		nil,
	)
}

func runObservationUntil(
	t *testing.T,
	watcher *Watcher,
	node Node,
	scan *discovery,
	ready func() bool,
) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := watcher.attempt(
			ctx,
			node,
			scan,
			nil,
		)
		done <- err
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("session cancellation: %v", err)
		}
	}()
	waitFor(t, ready)
}

func sessionWatcher(t *testing.T, node Node, connection Connection) *Watcher {
	t.Helper()
	synchronizer, err := New(Dependencies{Repository: missingMirrorRepository{}, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	watcher, err := NewWatcher(t.Context(), WatcherDependencies{
		Synchronizer: synchronizer, PriorityJobs: emptyPriorityJobs,
		Nodes:   func(context.Context) ([]Node, error) { return []Node{node}, nil },
		Connect: func(context.Context, Node) (Connection, error) { return connection, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	watcher.pollEvery = 5 * time.Millisecond
	t.Cleanup(func() { _ = watcher.Close() })
	return watcher
}

func TestSessionSlowHistoryLeavesSlotsForNewForeignAndClosesAfterJoin(t *testing.T) {
	node := watcherNode(1)
	old, fresh := queuedJob(node, 1), queuedJob(node, 2)
	fresh.Receipt.Status, fresh.Receipt.Revision, fresh.LatestSequence = domain.RunCompleted, 4, 4
	var added, historyActive atomic.Bool
	var closed, freshSeen, historySeen atomic.Int32
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			if after == 0 {
				return jobhost.JobPage{Contract: jobcontract.CurrentContract,
					Jobs: []jobcontract.Snapshot{old}, NextCursor: "1", HasMore: added.Load()}, nil
			}
			if after == 1 && added.Load() {
				return jobhost.JobPage{Contract: jobcontract.CurrentContract,
					Jobs: []jobcontract.Snapshot{fresh}, NextCursor: "2"}, nil
			}
			return emptyJobPage(after), nil
		}}, events: func(ctx context.Context, id string) error {
		if id == old.Receipt.JobID {
			historySeen.Add(1)
			historyActive.Store(true)
			defer historyActive.Store(false)
			<-ctx.Done()
			return ctx.Err()
		}
		freshSeen.Add(1)
		return errors.New("fixture import failure")
	}}
	wrapped := &joiningObservation{priorityObservation: connection, close: func() {
		if historyActive.Load() {
			t.Error("connection closed before its history worker joined")
		}
		closed.Add(1)
	}}
	watcher := sessionWatcher(t, node, wrapped)
	scan := resetDiscovery()
	scan.tail.cursor = 1 // A warm observer has already consumed this admission tail.
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
	stop := sync.OnceFunc(func() { cancel(); <-done })
	defer stop()
	waitFor(t, historyActive.Load)
	added.Store(true)
	waitFor(t, func() bool { return freshSeen.Load() > 0 })
	if historySeen.Load() != 1 {
		t.Fatal("blocked history was duplicated on later ticks")
	}
	stop()
	if closed.Load() != 1 || scan.tail.cursor != 2 {
		t.Fatal("session did not retain its new tail and close exactly once")
	}
}

type joiningObservation struct {
	*priorityObservation
	close func()
}

func (connection *joiningObservation) Close() error { connection.close(); return nil }

func TestSessionFailedFirstImportIsRevisitedByHistory(t *testing.T) {
	node := watcherNode(1)
	root := queuedJob(node, 1)
	var seen atomic.Int32
	connection := &failingJobObservation{watcherConnection: watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			if after > 0 {
				return emptyJobPage(after), nil
			}
			return jobhost.JobPage{Contract: jobcontract.CurrentContract,
				Jobs: []jobcontract.Snapshot{root}, NextCursor: "1"}, nil
		}}, observe: func(string) { seen.Add(1) }}
	watcher := sessionWatcher(t, node, connection)
	scan := resetDiscovery()
	runObservationUntil(
		t,
		watcher,
		node,
		&scan,
		func() bool { return seen.Load() >= 2 },
	)
	if scan.tail.cursor != 1 {
		t.Fatal("history retry reset the admission cursor")
	}
}

func TestSessionHistoryHorizonIsFixedAndBoundaryPageIsBounded(t *testing.T) {
	node := watcherNode(1)
	var mu sync.Mutex
	queries := []uint64{}
	connection := &watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			mu.Lock()
			queries = append(queries, after)
			mu.Unlock()
			return emptyJobPage(after), nil
		}}
	watcher := sessionWatcher(t, node, connection)
	scan := resetDiscovery()
	scan.tail.cursor, scan.history.horizon = 100, 25
	ctx, cancel := context.WithCancel(t.Context())
	session := observationSession{watcher: watcher, node: node, connection: connection,
		scan: &scan, ctx: ctx, events: make(chan sessionEvent, 3)}
	page := jobhost.JobPage{Contract: jobcontract.CurrentContract,
		Jobs: []jobcontract.Snapshot{queuedJob(node, 30)}, NextCursor: "30", HasMore: true}
	if err := session.acceptPage(sessionEvent{lane: historyLane, after: 0, page: page}); err != nil {
		t.Fatal(err)
	}
	if !scan.history.end || scan.history.horizon != 25 {
		t.Fatal("growing tail moved the current history horizon")
	}
	scan.history.index = len(scan.history.pending)
	session.fetch(historyLane)
	event := <-session.events
	if event.after != 0 || scan.history.horizon != 100 {
		t.Fatal("history failed to wrap independently to its next fixed horizon")
	}
	cancel()
	session.workers.Wait()
}

func TestSessionSchedulesHistoryPriorityAndTailWithinOneTick(t *testing.T) {
	node := watcherNode(1)
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node},
		snapshots: map[string]jobcontract.Snapshot{}, events: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}}
	watcher := sessionWatcher(t, node, connection)
	scan := resetDiscovery()
	for index := range 25 {
		scan.tail.pending = append(scan.tail.pending, queuedJob(node, index+1))
		scan.history.pending = append(scan.history.pending, queuedJob(node, index+100))
	}
	for index := range 2 {
		snapshot := queuedJob(node, index+900)
		connection.snapshots[snapshot.Receipt.JobID] = snapshot
		scan.priority.pending = append(scan.priority.pending, snapshot.Receipt.JobID)
	}
	ctx, cancel := context.WithCancel(t.Context())
	session := observationSession{watcher: watcher, node: node, connection: connection, scan: &scan,
		ctx: ctx, events: make(chan sessionEvent, 4), inflight: map[string]scanLane{}, selected: map[string]bool{}}
	session.schedule()
	counts := map[scanLane]int{}
	for _, lane := range session.inflight {
		counts[lane]++
	}
	if counts[historyLane] != 1 || counts[priorityLane] != 2 || counts[tailLane] != 1 {
		t.Fatalf("lane shares: %v", counts)
	}
	// Repeated metadata/completion callbacks must not create another tick budget.
	for range 10 {
		session.schedule()
	}
	if session.started != jobsPerTick || len(session.inflight) != jobsPerTick {
		t.Fatal("callback bypassed per-tick or in-flight bound")
	}
	cancel()
	session.workers.Wait()
}

func TestSessionColdTailReservesNodeSlotForLaterPriority(t *testing.T) {
	node := watcherNode(1)
	own := queuedJob(node, 900)
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node},
		snapshots: map[string]jobcontract.Snapshot{own.Receipt.JobID: own},
		events: func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}}
	watcher := sessionWatcher(t, node, connection)
	scan := resetDiscovery()
	for index := range jobsPerTick {
		scan.tail.pending = append(scan.tail.pending, queuedJob(node, index+1))
	}
	ctx, cancel := context.WithCancel(t.Context())
	session := observationSession{watcher: watcher, node: node, connection: connection, scan: &scan,
		ctx: ctx, events: make(chan sessionEvent, jobsPerTick), inflight: map[string]scanLane{}, selected: map[string]bool{}}
	defer func() { cancel(); session.workers.Wait() }()
	session.schedule()
	reserved := session.backgroundActive() == maxBackgroundSyncs && scan.tail.index == maxBackgroundSyncs
	if !reserved || len(session.inflight) != maxBackgroundSyncs {
		t.Fatal("cold discovery occupied the priority node slot or consumed its waiting fourth root")
	}
	scan.priority.pending = []string{own.Receipt.JobID}
	session.schedule()
	priorityStarted := session.laneActive(priorityLane) == 1 && len(session.inflight) == jobsPerTick
	if !priorityStarted || session.backgroundActive() != maxBackgroundSyncs {
		t.Fatal("later priority could not use its reserved slot while cold history remained blocked")
	}
}

func TestSessionRejectsMalformedAdmissionPagesWithoutAdvancing(t *testing.T) {
	for _, test := range []struct {
		cursor  string
		hasMore bool
	}{
		{cursor: "01"}, {cursor: "-1"}, {cursor: "0", hasMore: true},
	} {
		t.Run(fmt.Sprint(test), func(t *testing.T) {
			_, err := validateDiscoveryPage(jobhost.JobPage{Contract: jobcontract.CurrentContract,
				Jobs: []jobcontract.Snapshot{}, NextCursor: test.cursor, HasMore: test.hasMore}, 0)
			if !errors.Is(err, remoteresults.ErrInvalid) {
				t.Fatal("invalid admission cursor accepted")
			}
		})
	}
}

func TestSessionPriorityCannotBorrowAllSlotsWhileTailMetadataIsSlow(t *testing.T) {
	node := watcherNode(1)
	fresh := queuedJob(node, 500)
	metadataRelease := make(chan struct{})
	var priorityEntered, priorityPages atomic.Int32
	var tailSeen atomic.Bool
	snapshots := map[string]jobcontract.Snapshot{}
	for index := range 100 {
		snapshot := queuedJob(node, index+900)
		snapshots[snapshot.Receipt.JobID] = snapshot
	}
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
		list: func(ctx context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			select {
			case <-metadataRelease:
			case <-ctx.Done():
				return jobhost.JobPage{}, ctx.Err()
			}
			if after != 0 {
				return emptyJobPage(after), nil
			}
			return jobhost.JobPage{Contract: jobcontract.CurrentContract,
				Jobs: []jobcontract.Snapshot{fresh}, NextCursor: "1"}, nil
		}}, snapshots: snapshots, events: func(ctx context.Context, id string) error {
		if id == fresh.Receipt.JobID {
			tailSeen.Store(true)
			return errors.New("fixture import failure")
		}
		priorityEntered.Add(1)
		<-ctx.Done()
		return ctx.Err()
	}}
	watcher := sessionWatcher(t, node, connection)
	watcher.priorityJobs = func(
		_ context.Context,
		_ remoteresults.Origin,
		after string,
		_ uint32,
	) (remoteresults.PriorityPage, error) {
		priorityPages.Add(1)
		page := remoteresults.PriorityPage{JobIDs: []string{}, NextCursor: after}
		for index := range 100 {
			id := queuedJob(node, index+900).Receipt.JobID
			if id > after && len(page.JobIDs) < priorityPageSize {
				page.JobIDs, page.NextCursor = append(page.JobIDs, id), id
			}
		}
		page.HasMore = len(page.JobIDs) > 0
		return page, nil
	}
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
	waitFor(t, func() bool { return priorityPages.Load() >= 2 && priorityEntered.Load() == 2 })
	close(metadataRelease)
	waitFor(t, tailSeen.Load)
	if priorityEntered.Load() != 2 {
		t.Fatal("priority borrowed the remaining tail/history slots across ticks")
	}
}

func TestSessionNormalRotationDoesNotReportAFailure(t *testing.T) {
	node := watcherNode(1)
	connection := &watcherConnection{node: node,
		list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
			return emptyJobPage(after), nil
		}}
	watcher := sessionWatcher(t, node, connection)
	watcher.attemptTimeout = 15 * time.Millisecond
	watcher.pollEvery = time.Hour // A normal rotation must not wait this poll interval.
	var opened atomic.Int32
	watcher.connect = func(context.Context, Node) (Connection, error) { opened.Add(1); return connection, nil }
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	waitFor(t, func() bool { return opened.Load() >= 3 })
	if nodes := watcher.Snapshot().Nodes; len(nodes) != 1 || nodes[0].ErrorCode != "" {
		t.Fatal("normal connection rotation was reported as remote failure")
	}
	_ = watcher.Close()
	<-done
}

func TestSessionMetadataSuccessDoesNotEraseAJobFailure(t *testing.T) {
	reports := []syncResult{}
	session := observationSession{ctx: t.Context(), inflight: map[string]scanLane{},
		report: func(result syncResult) { reports = append(reports, result) }}
	jobErr := errors.New("fixture_job_failure")
	session.accept(sessionEvent{id: "fixture", result: syncResult{err: jobErr}})
	session.scan = &discovery{}
	session.accept(sessionEvent{metadata: true, lane: priorityLane,
		priority: remoteresults.PriorityPage{JobIDs: []string{}}})
	if len(reports) != 2 || !errors.Is(reports[1].err, jobErr) {
		t.Fatal("successful metadata erased the unresolved session job failure")
	}
}

func TestSessionCancellationJoinsBothMetadataAndJobWorkers(t *testing.T) {
	node := watcherNode(1)
	var active, metadata atomic.Int32
	connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
		list: func(ctx context.Context, _ uint64, _ uint32) (jobhost.JobPage, error) {
			metadata.Add(1)
			active.Add(1)
			defer active.Add(-1)
			<-ctx.Done()
			return jobhost.JobPage{}, ctx.Err()
		}}, events: func(ctx context.Context, _ string) error {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		return ctx.Err()
	}}
	var closes atomic.Int32
	wrapped := &joiningObservation{priorityObservation: connection, close: func() {
		if active.Load() != 0 {
			t.Error("connection closed while its consumers were still active")
		}
		closes.Add(1)
	}}
	watcher := sessionWatcher(t, node, wrapped)
	watcher.priorityJobs = func(
		ctx context.Context,
		_ remoteresults.Origin,
		_ string,
		_ uint32,
	) (remoteresults.PriorityPage, error) {
		active.Add(1)
		defer active.Add(-1)
		<-ctx.Done()
		return remoteresults.PriorityPage{}, ctx.Err()
	}
	scan := resetDiscovery()
	scan.tail.cursor = 1
	scan.tail.pending = []jobcontract.Snapshot{queuedJob(node, 1)}
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
	stop := sync.OnceFunc(func() { cancel(); <-done })
	defer stop()
	waitFor(t, func() bool { return active.Load() == 4 && metadata.Load() == 2 })
	stop()
	if closes.Load() != 1 || len(watcher.gate) != 0 {
		t.Fatal("session leaked its connection or global gate waiters")
	}
}

func TestWatcherReconfigurationJoinsOldSessionAndResetsDiscovery(t *testing.T) {
	node := watcherNode(1)
	root := queuedJob(node, 1)
	var revision, closes atomic.Int32
	var enabled, oldJob, secondObserved atomic.Bool
	revision.Store(1)
	enabled.Store(true)
	watcher := sessionWatcher(t, node, nil)
	watcher.refreshEvery = 5 * time.Millisecond
	watcher.source = func(context.Context) ([]Node, error) {
		if !enabled.Load() {
			return []Node{}, nil
		}
		current := node
		current.Revision = uint64(revision.Load())
		return []Node{current}, nil
	}
	watcher.connect = func(_ context.Context, current Node) (Connection, error) {
		if current.Revision == 2 && closes.Load() != 1 {
			t.Error("new configuration connected before old consumers joined and closed")
		}
		connection := &priorityObservation{watcherConnection: watcherConnection{node: current,
			list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
				if current.Revision == 2 {
					if after != 0 {
						t.Error("new configuration inherited the old admission cursor")
					}
					secondObserved.Store(true)
					return emptyJobPage(after), nil
				}
				if after != 0 {
					return emptyJobPage(after), nil
				}
				return jobhost.JobPage{Contract: jobcontract.CurrentContract,
					Jobs: []jobcontract.Snapshot{root}, NextCursor: "1"}, nil
			}}, events: func(ctx context.Context, _ string) error {
			oldJob.Store(true)
			defer oldJob.Store(false)
			<-ctx.Done()
			return errors.New("fixture late failure after reconfiguration")
		}}
		return &joiningObservation{priorityObservation: connection, close: func() {
			if oldJob.Load() {
				t.Error("configuration closed before old synchronization stopped")
			}
			closes.Add(1)
		}}, nil
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	stop := sync.OnceFunc(func() { _ = watcher.Close(); <-done })
	defer stop()
	waitFor(t, oldJob.Load)
	revision.Store(2)
	waitFor(t, secondObserved.Load)
	waitFor(t, func() bool {
		nodes := watcher.Snapshot().Nodes
		return len(nodes) == 1 && nodes[0].State == "idle" && !nodes[0].ObservedAt.IsZero()
	})
	if watcher.Snapshot().Nodes[0].ErrorCode != "" {
		t.Fatal("late old-generation failure changed current node status")
	}
	enabled.Store(false)
	waitFor(t, func() bool { return closes.Load() == 2 })
	if len(watcher.Snapshot().Nodes) != 0 {
		t.Fatal("removed node retained a visible observer")
	}
}
