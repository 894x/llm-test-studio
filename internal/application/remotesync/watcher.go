package remotesync

import (
	"cmp"
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const (
	maxObservedNodes   = 64
	maxConcurrentSyncs = 4
	discoveryPageSize  = 25
	jobsPerAttempt     = 4
)

type Node struct {
	Origin   remoteresults.Origin
	Revision uint64
}

type Connection interface {
	Remote
	ListJobs(context.Context, uint64, uint32) (jobhost.JobPage, error)
	Close() error
}

type WatcherDependencies struct {
	Synchronizer *Synchronizer
	Nodes        func(context.Context) ([]Node, error)
	Connect      func(context.Context, Node) (Connection, error)
}

type NodeStatus struct {
	ConfigurationID string    `json:"configuration_id"`
	State           string    `json:"state"`
	ErrorCode       string    `json:"error_code"`
	ObservedAt      time.Time `json:"observed_at"`
	AppliedAt       time.Time `json:"applied_at"`
}

type WatcherSnapshot struct {
	Running   bool         `json:"running"`
	ErrorCode string       `json:"error_code"`
	Nodes     []NodeStatus `json:"nodes"`
}

type Watcher struct {
	synchronizer   *Synchronizer
	source         func(context.Context) ([]Node, error)
	connect        func(context.Context, Node) (Connection, error)
	ctx            context.Context
	cancel         context.CancelFunc
	mu             sync.Mutex
	workers        map[string]*nodeWorker
	wait           sync.WaitGroup
	gate           chan struct{}
	running        bool
	closed         bool
	sourceError    string
	refreshEvery   time.Duration
	pollEvery      time.Duration
	attemptTimeout time.Duration
}

type nodeWorker struct {
	node       Node
	generation uint64
	active     bool
	cancel     context.CancelFunc
	status     NodeStatus
}

type discovery struct {
	cursor  uint64
	pending []jobcontract.Snapshot
	index   int
}

func NewWatcher(ctx context.Context, dependencies WatcherDependencies) (*Watcher, error) {
	validDependencies := dependencies.Synchronizer != nil && dependencies.Nodes != nil && dependencies.Connect != nil
	if ctx == nil || !validDependencies {
		return nil, remoteresults.ErrInvalid
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Watcher{
		synchronizer: dependencies.Synchronizer, source: dependencies.Nodes, connect: dependencies.Connect,
		ctx: lifetime, cancel: cancel, workers: map[string]*nodeWorker{}, gate: make(chan struct{}, maxConcurrentSyncs),
		refreshEvery: 10 * time.Second, pollEvery: 5 * time.Second, attemptTimeout: 2 * time.Minute,
	}, nil
}

// Run owns bounded per-node observers. A node's page cursor wraps after its
// current tail, so later commits and jobs from other clients are rediscovered.
// Reopening starts a new read-only scan; it never resubmits accepted work.
func (watcher *Watcher) Run() error {
	watcher.mu.Lock()
	if watcher.closed || watcher.ctx.Err() != nil {
		watcher.mu.Unlock()
		return context.Canceled
	}
	if watcher.running {
		watcher.mu.Unlock()
		return remoteresults.ErrConflict
	}
	watcher.running = true
	watcher.wait.Add(1)
	watcher.mu.Unlock()
	defer watcher.wait.Done()
	defer watcher.cancel()
	ticker := time.NewTicker(watcher.refreshEvery)
	defer ticker.Stop()
	for {
		ctx, cancel := context.WithTimeout(watcher.ctx, 5*time.Second)
		nodes, err := watcher.source(ctx)
		cancel()
		if err == nil {
			err = watcher.reconcile(nodes)
		}
		if watcher.ctx.Err() != nil {
			return watcher.ctx.Err()
		}
		if err != nil {
			// Suspend observations until the single current configuration can
			// be read and validated again. A transient lock timeout must not
			// permanently stop synchronization or retain stale node settings.
			_ = watcher.reconcile([]Node{})
		}
		watcher.mu.Lock()
		watcher.sourceError = ""
		if err != nil {
			watcher.sourceError = "remote_sync_configuration_failed"
		}
		watcher.mu.Unlock()
		select {
		case <-watcher.ctx.Done():
			return watcher.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (watcher *Watcher) Close() error {
	watcher.mu.Lock()
	watcher.closed = true
	watcher.cancel()
	watcher.mu.Unlock()
	watcher.wait.Wait()
	return nil
}

func (watcher *Watcher) Snapshot() WatcherSnapshot {
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	result := []NodeStatus{}
	for _, worker := range watcher.workers {
		if worker.active {
			result = append(result, worker.status)
		}
	}
	slices.SortFunc(result, func(left, right NodeStatus) int {
		return cmp.Compare(left.ConfigurationID, right.ConfigurationID)
	})
	return WatcherSnapshot{Running: watcher.running && !watcher.closed && watcher.ctx.Err() == nil,
		ErrorCode: watcher.sourceError, Nodes: result}
}

func (watcher *Watcher) reconcile(nodes []Node) error {
	if len(nodes) > maxObservedNodes {
		return remoteresults.ErrInvalid
	}
	wanted := map[string]Node{}
	installations := map[string]bool{}
	for _, node := range nodes {
		id := node.Origin.ConfigurationID
		validNode := node.Origin.Validate() == nil && node.Revision > 0
		duplicate := wanted[id].Revision != 0 || installations[node.Origin.InstallationID]
		if !validNode || duplicate {
			return remoteresults.ErrInvalid
		}
		wanted[id], installations[node.Origin.InstallationID] = node, true
	}
	watcher.mu.Lock()
	defer watcher.mu.Unlock()
	if watcher.closed || watcher.ctx.Err() != nil {
		return context.Canceled
	}
	for id, worker := range watcher.workers {
		node, exists := wanted[id]
		if exists && worker.active && worker.node == node {
			delete(wanted, id)
			continue
		}
		worker.active = exists
		worker.generation++
		if exists {
			worker.node = node
			delete(wanted, id)
		}
		if worker.cancel != nil {
			worker.cancel()
		}
	}
	for id, node := range wanted {
		// Removed observers still count until they exit; configuration churn
		// cannot accumulate an unbounded number of cancelled goroutines.
		if len(watcher.workers) >= maxObservedNodes {
			break
		}
		worker := &nodeWorker{node: node, generation: 1, active: true,
			status: NodeStatus{ConfigurationID: id, State: "connecting"}}
		watcher.workers[id] = worker
		watcher.wait.Add(1)
		go watcher.observe(id, worker)
	}
	return nil
}

func (watcher *Watcher) observe(id string, worker *nodeWorker) {
	defer watcher.wait.Done()
	defer func() {
		watcher.mu.Lock()
		delete(watcher.workers, id)
		watcher.mu.Unlock()
	}()
	previous := Node{}
	discovery := discovery{pending: []jobcontract.Snapshot{}}
	for {
		watcher.mu.Lock()
		if !worker.active || watcher.ctx.Err() != nil {
			watcher.mu.Unlock()
			return
		}
		node, generation := worker.node, worker.generation
		if node != previous {
			discovery = resetDiscovery()
			previous = node
		}
		ctx, cancel := context.WithTimeout(watcher.ctx, watcher.attemptTimeout)
		worker.cancel = cancel
		worker.status.State, worker.status.ErrorCode = "syncing", ""
		watcher.mu.Unlock()
		applied, err := watcher.attempt(ctx, node, &discovery)
		cancel()
		watcher.mu.Lock()
		changed := generation != worker.generation
		if !changed {
			worker.status.ObservedAt = time.Now().UTC()
			worker.status.State = "idle"
			if applied {
				worker.status.AppliedAt = worker.status.ObservedAt
			}
			if err != nil {
				worker.status.State, worker.status.ErrorCode = "error", "remote_sync_failed"
			}
		}
		watcher.mu.Unlock()
		if changed {
			continue
		}
		timer := time.NewTimer(watcher.pollEvery)
		select {
		case <-watcher.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func resetDiscovery() discovery { return discovery{pending: []jobcontract.Snapshot{}} }

func (watcher *Watcher) attempt(ctx context.Context, node Node, scan *discovery) (bool, error) {
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
	if scan.index == len(scan.pending) {
		page, err := connection.ListJobs(ctx, scan.cursor, discoveryPageSize)
		if err != nil {
			return false, err
		}
		cursor, err := strconv.ParseUint(page.NextCursor, 10, 63)
		validPage := err == nil && strconv.FormatUint(cursor, 10) == page.NextCursor &&
			page.Contract == jobcontract.CurrentContract && len(page.Jobs) <= discoveryPageSize
		validCursor := cursor >= scan.cursor && (len(page.Jobs) == 0 || cursor > scan.cursor)
		validTail := len(page.Jobs) > 0 || (!page.HasMore && cursor == scan.cursor)
		if !validPage || !validCursor || !validTail {
			return false, remoteresults.ErrInvalid
		}
		scan.pending, scan.index, scan.cursor = page.Jobs, 0, cursor
		if !page.HasMore {
			scan.cursor = 0
		}
	}
	applied := false
	var firstError error
	for range jobsPerAttempt {
		if scan.index == len(scan.pending) {
			break
		}
		if err := ctx.Err(); err != nil {
			return applied, err
		}
		snapshot := scan.pending[scan.index]
		scan.index++
		needed, err := watcher.needsSync(ctx, node, snapshot)
		if err == nil && needed {
			select {
			case watcher.gate <- struct{}{}:
				_, err = watcher.synchronizer.Pass(ctx, Request{Origin: node.Origin,
					JobID: snapshot.Receipt.JobID, Remote: connection})
				<-watcher.gate
				applied = applied || err == nil
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if firstError == nil {
			firstError = err
		}
	}
	return applied, firstError
}

func (watcher *Watcher) needsSync(ctx context.Context, node Node, snapshot jobcontract.Snapshot) (bool, error) {
	receipt := snapshot.Receipt
	validOwner := snapshot.ValidateState() == nil && domain.IsUUID(receipt.JobID) &&
		receipt.Origin.Kind == string(jobcontract.TargetRemote) && receipt.Origin.NodeID == node.Origin.NodeID &&
		receipt.Origin.InstallationID == node.Origin.InstallationID
	if !validOwner {
		return false, remoteresults.ErrConflict
	}
	mirror, err := watcher.synchronizer.repository.GetRemoteMirror(ctx, receipt.JobID)
	if errors.Is(err, remoteresults.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if mirror.Origin != node.Origin || snapshot.Validate(mirror.Spec) != nil {
		return false, remoteresults.ErrConflict
	}
	if mirror.Snapshot.LatestSequence == snapshot.LatestSequence && !reflect.DeepEqual(mirror.Snapshot, snapshot) {
		return false, remoteresults.ErrConflict
	}
	if mirror.Snapshot.LatestSequence < snapshot.LatestSequence {
		return true, nil
	}
	documents, err := watcher.synchronizer.repository.ListRemoteDocuments(ctx, receipt.JobID)
	if err != nil {
		return false, err
	}
	for _, document := range documents {
		if !document.Imported {
			return true, nil
		}
	}
	// Imported describes database documents. This skip does not assert that
	// separately managed evidence, attachments or externally removed files exist.
	return false, nil
}
