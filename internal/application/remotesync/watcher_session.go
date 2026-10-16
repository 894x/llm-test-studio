package remotesync

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type scanPage struct {
	cursor  uint64
	pending []jobcontract.Snapshot
	index   int
}

type historyScan struct {
	scanPage
	horizon uint64
	end     bool
}

type priorityScan struct {
	cursor  string
	pending []string
	index   int
}

func resetDiscovery() discovery {
	return discovery{
		tail:     scanPage{pending: []jobcontract.Snapshot{}},
		history:  historyScan{scanPage: scanPage{pending: []jobcontract.Snapshot{}}},
		priority: priorityScan{pending: []string{}},
	}
}

type scanLane uint8

const (
	tailLane scanLane = iota
	historyLane
	priorityLane
	laneCount
)

type sessionEvent struct {
	lane     scanLane
	metadata bool
	after    uint64
	page     jobhost.JobPage
	priority remoteresults.PriorityPage
	id       string
	result   syncResult
}

// Only this session's owner mutates scan and scheduling state. Workers publish
// one bounded result; cancellation joins every worker before closing SSH/HTTP.
type observationSession struct {
	watcher    *Watcher
	node       Node
	connection Connection
	scan       *discovery
	ctx        context.Context
	workers    sync.WaitGroup
	events     chan sessionEvent
	report     func(syncResult)
	inflight   map[string]scanLane
	selected   map[string]bool
	fetching   [laneCount]bool
	fetched    [laneCount]bool
	started    int
	applied    bool
	firstError error
}

func (watcher *Watcher) attempt(
	ctx context.Context,
	node Node,
	scan *discovery,
	report func(syncResult),
) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	connection, err := watcher.connect(ctx, node)
	if err != nil {
		return false, err
	}
	defer connection.Close()
	info, err := connection.Probe(ctx)
	if err != nil {
		return false, err
	}
	validSource := info.NodeID == node.Origin.NodeID && info.NodeInstallationID == node.Origin.InstallationID
	if !validSource {
		return false, remoteresults.ErrConflict
	}
	session := observationSession{
		watcher: watcher, node: node, connection: connection, scan: scan, ctx: ctx,
		events: make(chan sessionEvent, jobsPerTick+int(laneCount)), report: report,
		inflight: map[string]scanLane{}, selected: map[string]bool{},
	}
	defer func() { cancel(); session.workers.Wait() }()
	ticker := time.NewTicker(watcher.pollEvery)
	defer ticker.Stop()
	session.tick()
	for {
		select {
		case <-ctx.Done():
			// Reaching the connection's normal lifetime is rotation, not a
			// remote failure. Explicit owner cancellation still propagates.
			err := ctx.Err()
			if errors.Is(err, context.DeadlineExceeded) {
				err = nil
			}
			return session.applied, errors.Join(session.firstError, err)
		case event := <-session.events:
			session.accept(event)
			if errors.Is(event.result.err, runnerclient.ErrConnection) {
				return session.applied, errors.Join(session.firstError, event.result.err)
			}
			session.schedule()
		case <-ticker.C:
			session.tick()
		}
	}
}

func (session *observationSession) tick() {
	session.started, session.selected = 0, map[string]bool{}
	session.fetched = [laneCount]bool{}
	session.schedule()
	for lane := range laneCount {
		session.fetch(lane)
	}
}

func (session *observationSession) fetch(lane scanLane) {
	if session.fetching[lane] || session.fetched[lane] || session.ctx.Err() != nil {
		return
	}
	var after uint64
	switch lane {
	case tailLane:
		page := &session.scan.tail
		if page.index < len(page.pending) {
			return
		}
		after = page.cursor
	case historyLane:
		history := &session.scan.history
		if history.index < len(history.pending) {
			return
		}
		if history.end {
			history.scanPage, history.horizon, history.end = scanPage{pending: []jobcontract.Snapshot{}}, 0, false
		}
		if history.horizon == 0 {
			history.horizon = session.scan.tail.cursor
		}
		if history.horizon == 0 {
			return
		}
		after = history.cursor
	case priorityLane:
		if session.scan.priority.index < len(session.scan.priority.pending) {
			return
		}
	}
	session.fetching[lane], session.fetched[lane] = true, true
	priorityAfter := session.scan.priority.cursor
	session.workers.Go(func() {
		event := sessionEvent{lane: lane, metadata: true, after: after}
		if lane == priorityLane {
			event.priority, event.result.err = session.watcher.priorityPage(session.ctx, session.node, priorityAfter)
		} else {
			event.page, event.result.err = session.connection.ListJobs(session.ctx, after, discoveryPageSize)
		}
		session.publish(event)
	})
}

func (session *observationSession) publish(event sessionEvent) {
	select {
	case session.events <- event:
	case <-session.ctx.Done():
	}
}

func (session *observationSession) accept(event sessionEvent) {
	if session.ctx.Err() != nil {
		return
	}
	if event.metadata {
		session.fetching[event.lane] = false
		if event.result.err == nil {
			event.result.err = session.acceptPage(event)
		}
	} else {
		delete(session.inflight, event.id)
	}
	session.applied = session.applied || event.result.applied
	if session.firstError == nil {
		session.firstError = event.result.err
	}
	if session.report != nil {
		// Keep the session's first real failure visible even when another
		// metadata request succeeds. A new session makes a fresh observation.
		session.report(syncResult{applied: event.result.applied, err: session.firstError})
	}
}

func (session *observationSession) acceptPage(event sessionEvent) error {
	if event.lane == priorityLane {
		priority := &session.scan.priority
		priority.pending, priority.index, priority.cursor = event.priority.JobIDs, 0, event.priority.NextCursor
		if !event.priority.HasMore {
			priority.cursor = ""
		}
		return nil
	}
	cursor, err := validateDiscoveryPage(event.page, event.after)
	if err != nil {
		return err
	}
	page := &session.scan.tail
	if event.lane == historyLane {
		history := &session.scan.history
		page = &history.scanPage
		// ListJobs exposes a page cursor, not each item's admission sequence.
		// The boundary page may cross the fixed horizon by at most one page.
		history.end = !event.page.HasMore || cursor >= history.horizon
	}
	page.cursor, page.pending, page.index = cursor, event.page.Jobs, 0
	return nil
}

func validateDiscoveryPage(page jobhost.JobPage, after uint64) (uint64, error) {
	cursor, err := strconv.ParseUint(page.NextCursor, 10, 63)
	validPage := err == nil && strconv.FormatUint(cursor, 10) == page.NextCursor &&
		page.Contract == jobcontract.CurrentContract && len(page.Jobs) <= discoveryPageSize
	validCursor := cursor >= after && (len(page.Jobs) == 0 || cursor > after)
	validTail := len(page.Jobs) > 0 || (!page.HasMore && cursor == after)
	if !validPage || !validCursor || !validTail {
		return 0, remoteresults.ErrInvalid
	}
	return cursor, nil
}

func (session *observationSession) schedule() {
	if session.ctx.Err() != nil {
		return
	}
	// Reserve one background slot before priority/tail borrow the remaining
	// capacity. First discovery imports every root, including already terminal
	// roots; only historical revisits are limited to a single in-flight worker.
	session.schedulePage(historyLane, &session.scan.history.scanPage, 1)
	priority := &session.scan.priority
	for priority.index < len(priority.pending) {
		if !session.hasSlot() || session.laneActive(priorityLane) >= priorityPageSize {
			break
		}
		id := priority.pending[priority.index]
		priority.index++
		session.start(priorityLane, id, nil)
	}
	session.schedulePage(tailLane, &session.scan.tail, jobsPerTick)
}

func (session *observationSession) hasSlot() bool {
	return session.started < jobsPerTick && len(session.inflight) < jobsPerTick
}

func (session *observationSession) schedulePage(lane scanLane, page *scanPage, limit int) {
	if lane == historyLane && session.laneActive(historyLane) != 0 {
		return
	}
	var started int
	for page.index < len(page.pending) && session.hasSlot() && started < limit {
		snapshot := page.pending[page.index]
		page.index++
		if session.start(lane, snapshot.Receipt.JobID, &snapshot) {
			started++
		}
	}
}

func (session *observationSession) laneActive(lane scanLane) int {
	var count int
	for _, active := range session.inflight {
		if active == lane {
			count++
		}
	}
	return count
}

func (session *observationSession) start(lane scanLane, id string, snapshot *jobcontract.Snapshot) bool {
	if _, active := session.inflight[id]; active || session.selected[id] {
		return false
	}
	session.inflight[id], session.selected[id] = lane, true
	session.started++
	session.workers.Go(func() {
		result := session.watcher.syncJob(
			session.ctx,
			session.node,
			session.connection,
			id,
			snapshot,
		)
		session.publish(sessionEvent{lane: lane, id: id, result: result})
	})
	return true
}
