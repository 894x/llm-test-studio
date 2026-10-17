package runnerhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

func TestWatcherObservesUnknownForeignFactsAcrossHeldHistoricalReports(t *testing.T) {
	const historicalJobs = 32
	const expectedHistoryDownloads = 1
	fixture := newHTTPFixtureWithExecutionTimeout(
		t,
		false,
		capabilities(),
		90*time.Second,
	)
	management := httpManagementClient(t, fixture)
	history := make([]jobcontract.Submission, 0, historicalJobs)
	blocked := map[string]*atomic.Int64{}
	for index := range historicalJobs {
		submission := fixture.runSubmission(t, false)
		submission.Job.JobID = fmt.Sprintf("71000000-0000-4000-8000-%012d", index+1)
		if _, err := management.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		final := waitHTTPJob(
			t,
			fixture,
			submission.Job.JobID,
			domain.RunCompleted,
		)
		if index < 4 {
			reports, err := fixture.repository.ListReportsForRuns(t.Context(), final.Receipt.RunIDs)
			if err != nil || len(reports) != 1 {
				t.Fatal("historical report missing")
			}
			blocked["/jobs/"+submission.Job.JobID+"/reports/"+reports[0].ID] = new(atomic.Int64)
		}
		history = append(history, submission)
	}
	waitHTTPSettlement(t, management)
	var held atomic.Int64
	var handlers sync.WaitGroup
	accounting := &backlogHTTPAccounting{}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/jobs" {
			accounting.listingCalls.Add(1)
			after, err := strconv.ParseUint(request.URL.Query().Get("after"), 10, 64)
			if err != nil {
				t.Error("noncanonical discovery cursor")
			}
			for maximum := accounting.maximumAfter.Load(); after > maximum; maximum = accounting.maximumAfter.Load() {
				if accounting.maximumAfter.CompareAndSwap(maximum, after) {
					break
				}
			}
		}
		counter, slow := blocked[request.URL.Path]
		if !slow {
			fixture.api.Config.Handler.ServeHTTP(writer, request)
			return
		}
		counter.Add(1)
		handlers.Add(1)
		defer handlers.Done()
		held.Add(1)
		defer held.Add(-1)
		fixture.api.Config.Handler.ServeHTTP(heldBacklogReportWriter{
			ResponseWriter: writer, ctx: request.Context(), entered: func() {},
		}, request)
	}))
	t.Cleanup(api.Close)
	desktop, _ := performanceMirrorStore(t)
	cache, _ := syncDocuments(t)
	synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: desktop, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: coordinatorLocation.ConfigurationID,
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	watcher, err := remotesync.NewWatcher(t.Context(), remotesync.WatcherDependencies{
		Synchronizer: synchronizer, FactPriorityJobs: desktop.ListRemoteFactPriorityIDs,
		DocumentWork: desktop.ListRemoteDocumentWork,
		Nodes: func(context.Context) ([]remotesync.Node, error) {
			return []remotesync.Node{{Origin: origin, Revision: 1}}, nil
		},
		Connect: func(context.Context, remotesync.Node) (remotesync.Connection, error) {
			return newBacklogHTTPConnection(fixture, api.URL, accounting)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	stop := sync.OnceFunc(func() {
		closed := make(chan error, 1)
		go func() { closed <- watcher.Close() }()
		select {
		case err := <-closed:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watcher Close did not join its HTTP workers")
		}
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("watcher returned %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("watcher Run did not return after Close")
		}
	})
	defer stop()
	awaitObservation(t, func() bool { return held.Load() == expectedHistoryDownloads })
	_, lastHistoryErr := desktop.GetRemoteMirror(t.Context(), history[historicalJobs-1].Job.JobID)
	if !errors.Is(lastHistoryErr, remoteresults.ErrNotFound) {
		t.Fatal("fixture did not retain its unknown admission tail")
	}
	foreignRelease := make(chan struct{})
	releaseForeign := sync.OnceFunc(func() { close(foreignRelease) })
	var foreignRequests atomic.Int64
	foreign := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		foreignRequests.Add(1)
		select {
		case <-foreignRelease:
		case <-request.Context().Done():
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(foreign.Close)
	t.Cleanup(releaseForeign)
	fresh := fixture.runSubmission(t, false)
	prepared := httpRunInput(t, foreign.URL)
	prepared.Snapshot.Entries[0].Load.RequestTimeoutMS = 90_000
	prepared.Snapshot.PlanDocument.Entries[0].Load.RequestTimeoutMS = 90_000
	fresh.Job.Spec.Run = &prepared
	fresh.Secrets[0].Slot = prepared.Meta.ID
	if err := fresh.Validate(); err != nil {
		t.Fatal(err)
	}
	other := httpManagementClient(t, fixture)
	accepted, err := other.Submit(t.Context(), &fresh)
	if err != nil || accepted.JobID != fresh.Job.JobID {
		t.Fatalf("other client acceptance failed: %v", err)
	}
	var serverRunning jobcontract.Snapshot
	awaitObservation(t, func() bool {
		current, err := fixture.repository.GetJob(t.Context(), fresh.Job.JobID)
		if err == nil && current.Receipt.Status == domain.RunRunning {
			serverRunning = current
			return true
		}
		return false
	})
	page, err := other.ReadEvents(t.Context(), runnerclient.EventQuery{JobID: fresh.Job.JobID, Limit: 200})
	if err != nil || len(page.Events) == 0 || page.Events[0].Kind != jobcontract.EventAccepted {
		t.Fatal("foreign job has no original acceptance journal")
	}
	var acceptedEvent jobcontract.AcceptedEvent
	if json.Unmarshal(page.Events[0].Payload, &acceptedEvent) != nil || acceptedEvent.Spec.JobID != fresh.Job.JobID {
		t.Fatal("foreign acceptance journal changed the original job")
	}
	if acceptedEvent.Snapshot.Receipt.Status != domain.RunQueued || page.Events[0].Sequence != 1 {
		t.Fatal("foreign acceptance journal omitted its original queued state")
	}
	started := time.Now()
	deadline, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var running remoteresults.Mirror
	for {
		mirror, mirrorErr := desktop.GetRemoteMirror(deadline, fresh.Job.JobID)
		child, childErr := desktop.GetRun(deadline, prepared.Meta.ID)
		if mirrorErr == nil && childErr == nil && mirror.Snapshot.Receipt.Status == domain.RunRunning &&
			child.Status() == domain.RunRunning && mirror.Snapshot.LatestSequence >= serverRunning.LatestSequence {
			running = mirror
			break
		}
		select {
		case <-deadline.Done():
			t.Fatalf("unknown foreign running facts blocked by held history: held=%d list_calls=%d server=%s",
				held.Load(), accounting.listingCalls.Load(), serverRunning.Receipt.Status)
		case <-time.After(10 * time.Millisecond):
		}
	}
	runningVisibleAfter := time.Since(started)
	if _, err := desktop.GetSubmission(t.Context(), fresh.Job.JobID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("foreign observation fabricated a local submission")
	}
	validAcceptance := running.Origin == origin && running.Spec.JobID == fresh.Job.JobID &&
		running.Snapshot.Receipt.AcceptedAt.Equal(acceptedEvent.Snapshot.Receipt.AcceptedAt)
	if !validAcceptance || accounting.maximumAfter.Load() < 25 {
		t.Fatal("foreign facts did not preserve the original acceptance across discovery pages")
	}
	releaseForeign()
	final := waitHTTPJob(
		t,
		fixture,
		fresh.Job.JobID,
		domain.RunCompleted,
	)
	serverResults, err := fixture.repository.ListResults(t.Context(), prepared.Meta.ID)
	if err != nil || len(serverResults) == 0 {
		t.Fatal("original foreign result facts missing")
	}
	terminalDeadline, stopTerminal := context.WithTimeout(t.Context(), 20*time.Second)
	defer stopTerminal()
	for {
		mirror, mirrorErr := desktop.GetRemoteMirror(terminalDeadline, fresh.Job.JobID)
		child, childErr := desktop.GetRun(terminalDeadline, prepared.Meta.ID)
		results, resultErr := desktop.ListResults(terminalDeadline, prepared.Meta.ID)
		currentFacts := mirrorErr == nil && childErr == nil && resultErr == nil &&
			bytes.Equal(encode(t, serverResults), encode(t, results))
		terminalFacts := child.Status() == domain.RunCompleted && mirror.Snapshot.Receipt.Status == domain.RunCompleted
		currentCursor := mirror.Snapshot.LatestSequence == final.LatestSequence &&
			final.LatestSequence > running.Snapshot.LatestSequence
		if currentFacts && terminalFacts && currentCursor {
			break
		}
		select {
		case <-terminalDeadline.Done():
			t.Fatalf("foreign terminal facts missing: root=%s child=%s results=%d cursor=%d/%d "+
				"mirror=%v childErr=%v resultErr=%v status=%+v",
				mirror.Snapshot.Receipt.Status, child.Status(), len(results), mirror.Snapshot.LatestSequence,
				final.LatestSequence, mirrorErr, childErr, resultErr, watcher.Snapshot())
		case <-time.After(10 * time.Millisecond):
		}
	}
	serverRun, err := fixture.repository.GetRun(t.Context(), prepared.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
	importedRun, err := desktop.GetRun(t.Context(), prepared.Meta.ID)
	if err != nil || !bytes.Equal(encode(t, serverRun), encode(t, importedRun)) {
		t.Fatal("foreign terminal child differs from the original node fact")
	}
	importedResults, err := desktop.ListResults(t.Context(), prepared.Meta.ID)
	if err != nil || !bytes.Equal(encode(t, serverResults), encode(t, importedResults)) {
		t.Fatal("foreign result facts differ from the original node children")
	}
	documents, err := desktop.ListRemoteDocuments(t.Context(), fresh.Job.JobID)
	if err != nil || len(documents) != 1 || documents[0].Imported {
		t.Fatal("foreign terminal facts fabricated readiness of a blocked history-class report")
	}
	for _, submission := range history[:4] {
		documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
		if err != nil || len(documents) != 1 || documents[0].Imported {
			t.Fatal("held history reference was imported or omitted")
		}
	}
	validRequests := foreignRequests.Load() == 1 && fixture.requests.Load() == historicalJobs
	if held.Load() != expectedHistoryDownloads || !validRequests {
		t.Fatal("facts observation released a historical response or replayed execution")
	}
	before, err := fixture.repository.GetJob(t.Context(), history[0].Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	exited := make(chan struct{})
	go func() { handlers.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not join held historical HTTP handlers")
	}
	joined := accounting.opened.Load() == accounting.closed.Load() && accounting.activeBodies.Load() == 0 &&
		!accounting.closedWithActive.Load() && accounting.closeErrors.Load() == 0 && held.Load() == 0
	if !joined || accounting.posts.Load() != 0 {
		t.Fatal("read-only facts observation leaked resources or mutated execution")
	}
	after, err := fixture.repository.GetJob(t.Context(), history[0].Job.JobID)
	if err != nil || !bytes.Equal(encode(t, before), encode(t, after)) {
		t.Fatal("observer Close changed the historical job")
	}
	t.Logf("history=%d running_visible_after=%s running_cursor=%d terminal_cursor=%d "+
		"held_until_close=%d mutation_count=%d",
		historicalJobs, runningVisibleAfter, running.Snapshot.LatestSequence, final.LatestSequence,
		expectedHistoryDownloads, accounting.posts.Load())
}
