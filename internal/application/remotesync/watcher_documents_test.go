package remotesync

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestWatcherFactsAndDocumentClassesShareFourSlotsAndJoinWaiters(t *testing.T) {
	repository := &gatedMirrorRepository{entered: make(chan struct{}, 8)}
	synchronizer, err := New(Dependencies{Repository: repository, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	node := watcherNode(1)
	connection := &watcherConnection{node: node}
	watcher := sessionWatcher(t, node, connection)
	watcher.synchronizer = synchronizer
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	results := make(chan syncResult, 7)
	startFact := func(lane scanLane, index int) {
		snapshot := queuedJob(node, index)
		workers.Go(func() {
			results <- watcher.syncJob(
				ctx,
				lane,
				node,
				connection,
				snapshot.Receipt.JobID,
				&snapshot,
			)
		})
	}
	startDocument := func(class int) {
		workers.Go(func() {
			work := remoteresults.DocumentWork{
				JobID: queuedJob(node, 100+class).Receipt.JobID, Document: runDocument(class + 1),
			}
			results <- watcher.syncDocument(
				ctx,
				node,
				connection,
				class,
				work,
			)
		})
	}
	startFact(tailLane, 1)
	<-repository.entered
	startFact(tailLane, 2) // Waiting for background quota must not take fact/global capacity.
	if _, err := watcher.priorityPage(ctx, node, ""); err != nil {
		t.Fatal(err)
	}
	startFact(priorityLane, 3)
	<-repository.entered
	startDocument(0)
	startDocument(1)
	for range 2 {
		select {
		case <-repository.entered:
		case <-time.After(time.Second):
			t.Fatal("a document class could not enter alongside both fact workers")
		}
	}
	startDocument(0)
	startFact(priorityLane, 4)
	select {
	case <-repository.entered:
		t.Fatal("a class waiter bypassed the global or phase bound")
	case <-time.After(20 * time.Millisecond):
	}
	fullPhases := len(watcher.factGate) == 2 && len(watcher.documentGate) == 2
	fullClasses := len(watcher.focusDocumentGate) == 1 && len(watcher.historyDocumentGate) == 1
	fullBudget := fullPhases && fullClasses && len(watcher.gate) == 4 && repository.active.Load() == 4
	if !fullBudget {
		t.Fatal("facts/documents did not share the exact four-worker budget")
	}
	cancel()
	workers.Wait()
	for range 7 {
		if result := <-results; !errors.Is(result.err, context.Canceled) {
			t.Fatal("worker or quota waiter lost cancellation")
		}
	}
	for _, gate := range []chan struct{}{watcher.gate, watcher.factGate, watcher.backgroundFactGate,
		watcher.documentGate, watcher.focusDocumentGate, watcher.historyDocumentGate} {
		if len(gate) != 0 {
			t.Fatal("cancelled worker retained a phase/class/global quota")
		}
	}
	if repository.active.Load() != 0 {
		t.Fatal("validation worker was not joined")
	}
}

func TestSessionPromotedHistoryReferenceDoesNotOccupyFocusSlot(t *testing.T) {
	old, fresh := runDocument(1), runDocument(2)
	fixture := newDocumentFixture(t, []remoteresults.DocumentReference{old, fresh})
	entered := make(chan struct{})
	fixture.cache.beforeRead = func(ctx context.Context, reference jobcontract.ReportReference) error {
		if reference == old.Reference {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}
	node := watcherNode(1)
	watcher := sessionWatcher(t, node, &watcherConnection{node: node})
	watcher.synchronizer = fixture.synchronizer
	oldWork := remoteresults.DocumentWork{JobID: fixture.request.JobID, Document: old}
	freshWork := remoteresults.DocumentWork{JobID: fixture.request.JobID, Document: fresh}
	scan := resetDiscovery()
	scan.documents[1] = documentScan{pending: []remoteresults.DocumentWork{oldWork}, end: true}
	ctx, cancel := context.WithCancel(t.Context())
	session := observationSession{watcher: watcher, node: node, connection: &watcherConnection{node: node},
		scan: &scan, ctx: ctx, events: make(chan sessionEvent, 4), inflight: map[string]scanLane{}}
	defer func() { cancel(); session.workers.Wait() }()
	session.scheduleDocuments()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("history download did not enter its held turn")
	}
	// A new durable local control can promote the same immutable reference.
	scan.documents[0] = documentScan{pending: []remoteresults.DocumentWork{oldWork, freshWork}, end: true}
	session.scheduleDocuments()
	select {
	case event := <-session.events:
		focusApplied := event.documentIndex == 0 && event.result.applied && event.result.err == nil
		if !focusApplied {
			t.Fatal("promoted reference occupied Focus instead of the independent report")
		}
		session.accept(event)
	case <-time.After(time.Second):
		t.Fatal("Focus worker waited for the held history cache lock")
	}
	if !session.documentInflight[1] || session.activeDocuments[1] != oldWork {
		t.Fatal("Focus progress released or replaced the held history turn")
	}
	if scan.documents[0].index != 2 || scan.documents[0].cursor != documentCursor(freshWork) {
		t.Fatal("cross-class duplicate did not advance the bounded selection cursor")
	}
	fixture.repository.mu.Lock()
	oldImported, freshImported := fixture.repository.documents[0].Imported, fixture.repository.documents[1].Imported
	fixture.repository.mu.Unlock()
	if oldImported || !freshImported {
		t.Fatal("Focus falsely completed the held history reference")
	}
	fixture.cache.mu.Lock()
	reads := len(fixture.cache.reads)
	fixture.cache.mu.Unlock()
	if reads != 2 {
		t.Fatal("promoted history reference started a duplicate download")
	}
}

func TestSessionDocumentFailureAdvancesTurnWithoutConsumingFactSlots(t *testing.T) {
	documents := []remoteresults.DocumentReference{runDocument(1), runDocument(2)}
	fixture := newDocumentFixture(t, documents)
	failure := errors.New("first document unavailable")
	fixture.cache.beforeRead = func(_ context.Context, reference jobcontract.ReportReference) error {
		if reference == documents[0].Reference {
			return failure
		}
		return nil
	}
	node := watcherNode(1)
	watcher := sessionWatcher(t, node, &watcherConnection{node: node})
	watcher.synchronizer = fixture.synchronizer
	scan := resetDiscovery()
	scan.documents[1] = documentScan{pending: []remoteresults.DocumentWork{
		{JobID: fixture.request.JobID, Document: documents[0]},
		{JobID: fixture.request.JobID, Document: documents[1]},
	}, end: true}
	ctx, cancel := context.WithCancel(t.Context())
	session := observationSession{watcher: watcher, node: node, connection: &watcherConnection{node: node},
		scan: &scan, ctx: ctx, events: make(chan sessionEvent, 4), inflight: map[string]scanLane{}}
	defer func() { cancel(); session.workers.Wait() }()
	session.scheduleDocuments()
	event := <-session.events
	session.accept(event)
	firstCursor := documentCursor(scan.documents[1].pending[0])
	if !errors.Is(event.result.err, failure) || scan.documents[1].cursor != firstCursor {
		t.Fatal("failed document did not leave its turn cursor advanced")
	}
	factsIdle := len(session.inflight) == 0 && len(watcher.factGate) == 0
	if !factsIdle || fixture.repository.documents[0].Imported {
		t.Fatal("document failure occupied fact slots or falsely imported the reference")
	}
	session.documentStarted = [2]bool{} // The next normal poll grants a new turn.
	session.scheduleDocuments()
	event = <-session.events
	session.accept(event)
	if event.result.err != nil || !fixture.repository.documents[1].Imported {
		t.Fatal("a failed reference blocked its next independent document turn")
	}
	if scan.documents[1].cursor != documentCursor(scan.documents[1].pending[1]) {
		t.Fatal("document cursor did not retain the selected composite key")
	}
	// Reconnection preserves the cursor/page; an exhausted page wraps only at EOF.
	reopened := scan.documents[1]
	retained := reopened.index == 2 && reopened.end && reopened.cursor != (remoteresults.DocumentCursor{})
	if !retained {
		t.Fatal("document rotation discarded pending retry metadata")
	}
}

func TestWatcherRejectsMalformedDocumentPagesAndPreservesReaderCancellation(t *testing.T) {
	node := watcherNode(1)
	watcher := sessionWatcher(t, node, nil)
	work := remoteresults.DocumentWork{JobID: queuedJob(node, 1).Receipt.JobID, Document: runDocument(1)}
	cursor := documentCursor(work)
	for _, page := range []remoteresults.DocumentPage{
		{Items: []remoteresults.DocumentWork{}, HasMore: true},
		{Items: []remoteresults.DocumentWork{work, work}, NextCursor: cursor},
		{Items: []remoteresults.DocumentWork{work}},
		{Items: []remoteresults.DocumentWork{}, NextCursor: cursor},
	} {
		watcher.documentWork = func(context.Context, remoteresults.DocumentQuery) (remoteresults.DocumentPage, error) {
			return page, nil
		}
		_, err := watcher.documentPage(t.Context(), remoteresults.DocumentQuery{
			Origin: node.Origin, Class: remoteresults.DocumentHistory, Limit: 2,
		})
		if !errors.Is(err, remoteresults.ErrInvalid) {
			t.Fatal("malformed document hint page was accepted")
		}
	}
	watcher.documentWork = func(context.Context, remoteresults.DocumentQuery) (remoteresults.DocumentPage, error) {
		return remoteresults.DocumentPage{}, context.DeadlineExceeded
	}
	_, err := watcher.documentPage(t.Context(), remoteresults.DocumentQuery{
		Origin: node.Origin, Class: remoteresults.DocumentFocus, Limit: 2,
	})
	if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal("candidate reader timeout was reclassified as corrupt ownership")
	}
}
