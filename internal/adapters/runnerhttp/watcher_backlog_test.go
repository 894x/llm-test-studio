package runnerhttp_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type backlogHTTPAccounting struct {
	opened           atomic.Int64
	closed           atomic.Int64
	activeBodies     atomic.Int64
	maximumBodies    atomic.Int64
	closedWithActive atomic.Bool
	closeErrors      atomic.Int64
	posts            atomic.Int64
	listingCalls     atomic.Int64
	maximumAfter     atomic.Uint64
}

type backlogResponseBody struct {
	io.ReadCloser
	once       sync.Once
	accounting *backlogHTTPAccounting
}

func (body *backlogResponseBody) Close() error {
	err := body.ReadCloser.Close()
	body.once.Do(func() {
		body.accounting.activeBodies.Add(-1)
		if err != nil {
			body.accounting.closeErrors.Add(1)
		}
	})
	return err
}

type backlogHTTPConnection struct {
	*runnerclient.Client
	transport  *http.Transport
	accounting *backlogHTTPAccounting
}

func (connection *backlogHTTPConnection) Close() error {
	if connection.accounting.activeBodies.Load() != 0 {
		connection.accounting.closedWithActive.Store(true)
	}
	connection.transport.CloseIdleConnections()
	connection.accounting.closed.Add(1)
	return nil
}

func newBacklogHTTPConnection(
	fixture *httpFixture,
	url string,
	accounting *backlogHTTPAccounting,
) (*backlogHTTPConnection, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	httpClient := &http.Client{Transport: transport}
	identity := fixture.host.Identity()
	client, err := runnerclient.New(runnerclient.Identity{
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID,
	}, func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
		if input.Method != http.MethodGet {
			accounting.posts.Add(1)
			return nil, errors.New("observation attempted a mutating HTTP operation")
		}
		request, err := http.NewRequestWithContext(
			ctx,
			input.Method,
			url+input.Route,
			input.Body,
		)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Authorization", "Bearer "+nodeToken)
		request.Header.Set(jobcontract.ContractHeader, "1")
		request.Header.Set("Accept-Encoding", "identity")
		if input.JSONGzip {
			request.Header.Set("Accept-Encoding", "gzip")
		}
		if input.RangeStart != nil {
			request.Header.Set("Range", "bytes="+strconv.FormatInt(*input.RangeStart, 10)+"-")
		}
		active := accounting.activeBodies.Add(1)
		for maximum := accounting.maximumBodies.Load(); active > maximum; maximum = accounting.maximumBodies.Load() {
			if accounting.maximumBodies.CompareAndSwap(maximum, active) {
				break
			}
		}
		response, err := httpClient.Do(request)
		if err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			accounting.activeBodies.Add(-1)
			return nil, err
		}
		response.Body = &backlogResponseBody{ReadCloser: response.Body, accounting: accounting}
		return response, nil
	})
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	accounting.opened.Add(1)
	return &backlogHTTPConnection{Client: client, transport: transport, accounting: accounting}, nil
}

type heldBacklogReportWriter struct {
	http.ResponseWriter
	ctx     context.Context
	entered func()
}

func (writer heldBacklogReportWriter) Write(document []byte) (int, error) {
	written, err := writer.ResponseWriter.Write(document[:min(16, len(document))])
	if err != nil {
		return written, err
	}
	if err := http.NewResponseController(writer.ResponseWriter).Flush(); err != nil {
		return written, err
	}
	writer.entered()
	<-writer.ctx.Done()
	return written, writer.ctx.Err()
}

func TestWatcherPrioritizesNewOwnJobDuringRealHTTPHistoryBacklog(t *testing.T) {
	verifyOwnJobDuringHTTPBacklog(t, 1)
}

func TestWatcherReservesOwnPriorityDuringColdHTTPBacklogSaturation(t *testing.T) {
	verifyOwnJobDuringHTTPBacklog(t, 4)
}

func verifyOwnJobDuringHTTPBacklog(t *testing.T, heldCandidates int) {
	t.Helper()
	const historicalJobs = 64 // More than two real 25-root discovery pages.
	fixture := newHTTPFixture(t, false, capabilities())
	management := httpManagementClient(t, fixture)
	history := make([]jobcontract.Submission, 0, historicalJobs)
	for index := range historicalJobs {
		submission := fixture.runSubmission(t, false)
		submission.Job.JobID = fmt.Sprintf("71000000-0000-4000-8000-%012d", index+1)
		if _, err := management.Submit(t.Context(), &submission); err != nil {
			t.Fatal(err)
		}
		waitHTTPJob(
			t,
			fixture,
			submission.Job.JobID,
			domain.RunCompleted,
		)
		history = append(history, submission)
	}
	if fixture.requests.Load() != historicalJobs {
		t.Fatal("historical fixture did not execute exactly one local Mock request per job")
	}
	blockedJob := history[0].Job.JobID
	blockedSnapshot, err := fixture.repository.GetJob(t.Context(), blockedJob)
	if err != nil {
		t.Fatal(err)
	}
	reports, err := fixture.repository.ListReportsForRuns(t.Context(), blockedSnapshot.Receipt.RunIDs)
	if err != nil || len(reports) != 1 {
		t.Fatalf("first historical report count=%d: %v", len(reports), err)
	}
	blockedDocument, err := fixture.repository.ReadRunReport(t.Context(), blockedJob, reports[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	blockedPath := "/jobs/" + blockedJob + "/reports/" + reports[0].ID
	blockedPaths := map[string]*atomic.Int64{blockedPath: new(atomic.Int64)}
	lastCandidatePath := blockedPath
	for _, submission := range history[1:heldCandidates] {
		reports, err := fixture.repository.ListReportsForRuns(t.Context(), []string{submission.Job.Spec.Run.Meta.ID})
		if err != nil || len(reports) != 1 {
			t.Fatal("cold saturation fixture is missing a real historical report")
		}
		lastCandidatePath = "/jobs/" + submission.Job.JobID + "/reports/" + reports[0].ID
		blockedPaths[lastCandidatePath] = new(atomic.Int64)
	}
	entered := make(chan struct{})
	var enterOnce sync.Once
	var handlers sync.WaitGroup
	var downloads, activeDownloads atomic.Int64
	accounting := &backlogHTTPAccounting{}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/jobs" {
			accounting.listingCalls.Add(1)
			after, err := strconv.ParseUint(request.URL.Query().Get("after"), 10, 64)
			if err != nil {
				t.Error("watcher sent a noncanonical discovery cursor")
			}
			for maximum := accounting.maximumAfter.Load(); after > maximum; maximum = accounting.maximumAfter.Load() {
				if accounting.maximumAfter.CompareAndSwap(maximum, after) {
					break
				}
			}
		}
		counter, blocked := blockedPaths[request.URL.Path]
		if !blocked {
			fixture.api.Config.Handler.ServeHTTP(writer, request)
			return
		}
		counter.Add(1)
		downloads.Add(1)
		activeDownloads.Add(1)
		handlers.Add(1)
		defer handlers.Done()
		defer activeDownloads.Add(-1)
		held := heldBacklogReportWriter{ResponseWriter: writer, ctx: request.Context(),
			entered: func() { enterOnce.Do(func() { close(entered) }) }}
		fixture.api.Config.Handler.ServeHTTP(held, request)
	}))
	t.Cleanup(api.Close)
	desktop, _ := performanceMirrorStore(t)
	cache, cacheRoot := syncDocuments(t)
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
		Connect: func(_ context.Context, node remotesync.Node) (remotesync.Connection, error) {
			if node.Origin != origin {
				return nil, remoteresults.ErrConflict
			}
			return newBacklogHTTPConnection(fixture, api.URL, accounting)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Keep the production 5s tick, 2min session and global four-worker budget.
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
				t.Errorf("watcher Run returned %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("watcher Run did not return after Close")
		}
	})
	defer stop()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("initial real tail discovery never began its historical report download")
	}
	wantHeld := 1 // History documents have one slot; Focus retains the other.
	waitHeld, cancelHeld := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancelHeld()
	for activeDownloads.Load() < int64(wantHeld) {
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-waitHeld.Done():
			timer.Stop()
			t.Fatal("eligible cold historical downloads did not occupy their background slots")
		case <-timer.C:
		}
	}
	if _, err := desktop.GetSubmission(t.Context(), blockedJob); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal("foreign history was given a fabricated local submission")
	}
	_, err = desktop.GetRemoteMirror(t.Context(), history[historicalJobs-1].Job.JobID)
	if !errors.Is(err, remoteresults.ErrNotFound) {
		t.Fatal("fixture did not retain an undiscovered historical backlog")
	}
	var coordinatorOpened, coordinatorClosed, newSubmissions atomic.Int64
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{Repository: desktop, Now: time.Now,
		Open: func(_ context.Context, location jobs.Location, _ jobs.Access) (jobs.Backend, error) {
			if location != coordinatorLocation {
				return nil, jobs.ErrTargetChanged
			}
			coordinatorOpened.Add(1)
			client := coordinatorClient(t, fixture, func(*http.Response) { newSubmissions.Add(1) })
			return &coordinatorConnection{Client: client, closed: &coordinatorClosed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := fixture.runSubmission(t, false)
	prepareCoordinatedJob(t, coordinator, fresh)
	started := time.Now()
	accepted, err := coordinator.Submit(t.Context(), fresh.Job.JobID, coordinatorLeases(t, fresh))
	if err != nil || accepted.State != jobs.SubmissionAccepted {
		t.Fatalf("new own job was not durably accepted: %v", err)
	}
	final := waitHTTPJob(
		t,
		fixture,
		fresh.Job.JobID,
		domain.RunCompleted,
	)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var sharedImported, sharedObserved, remainingHistory int
	for {
		mirror, mirrorErr := desktop.GetRemoteMirror(ctx, fresh.Job.JobID)
		documents, documentErr := desktop.ListRemoteDocuments(ctx, fresh.Job.JobID)
		ownCurrent := mirrorErr == nil && mirror.Snapshot.LatestSequence == final.LatestSequence &&
			mirror.Snapshot.Receipt.Status == domain.RunCompleted
		ownImported := documentErr == nil && len(documents) == 1 && documents[0].Imported
		if ownCurrent && ownImported {
			break
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatalf("new own job remained unmirrored after four production ticks; held=%d server=%s",
				activeDownloads.Load(), final.Receipt.Status)
		case <-timer.C:
		}
	}
	elapsed := time.Since(started)
	for _, submission := range history {
		if _, err := desktop.GetRemoteMirror(t.Context(), submission.Job.JobID); err == nil {
			sharedObserved++
		}
		documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
		if errors.Is(err, remoteresults.ErrNotFound) {
			remainingHistory++
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(documents) == 1 && documents[0].Imported {
			sharedImported++
		} else {
			remainingHistory++
		}
	}
	if sharedObserved <= wantHeld {
		t.Fatal("a held report starved other shared history facts")
	}
	if remainingHistory < 25 {
		t.Fatal("own priority was only observed after exhausting the history backlog")
	}
	_, err = desktop.GetRemoteMirror(t.Context(), history[historicalJobs-1].Job.JobID)
	if !errors.Is(err, remoteresults.ErrNotFound) {
		t.Fatal("new own job was only observed after all historical roots were discovered")
	}
	documents, err := desktop.ListRemoteDocuments(t.Context(), blockedJob)
	stillPending := err == nil && len(documents) == 1 && !documents[0].Imported
	validDownloads := activeDownloads.Load() == int64(wantHeld) && downloads.Load() == int64(wantHeld)
	if !stillPending || !validDownloads {
		t.Fatal("blocked historical report was falsely imported or its download duplicated")
	}
	if heldCandidates == 4 && blockedPaths[lastCandidatePath].Load() != 0 {
		t.Fatal("another cold historical report occupied the reserved Focus document slot")
	}
	partialPath := filepath.Join(cacheRoot, blockedDocument.Reference.SHA256+".partial")
	partial, err := os.ReadFile(partialPath)
	if err != nil || !bytes.Equal(partial, blockedDocument.Document[:16]) {
		t.Fatal("real HTTP download did not retain exactly its unverified partial prefix")
	}
	completePath := filepath.Join(cacheRoot, blockedDocument.Reference.SHA256+".json")
	if _, err := os.Lstat(completePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("incomplete historical report was published as a verified cache document")
	}
	stop()
	exited := make(chan struct{})
	go func() { handlers.Wait(); close(exited) }()
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled historical HTTP response did not exit")
	}
	joined := accounting.opened.Load() == accounting.closed.Load() && accounting.activeBodies.Load() == 0 &&
		!accounting.closedWithActive.Load() && accounting.closeErrors.Load() == 0
	readOnlyAndIdle := activeDownloads.Load() == 0 && accounting.posts.Load() == 0
	if !joined || !readOnlyAndIdle {
		t.Fatal("observation shutdown leaked a body/connection or attempted a mutation")
	}
	if accounting.maximumBodies.Load() < 2 || accounting.listingCalls.Load() == 0 {
		t.Fatal("fixture did not overlap a real historical response with other HTTP observations")
	}
	if newSubmissions.Load() != 1 || fixture.requests.Load() != historicalJobs+1 {
		t.Fatal("observation repeated a submission or provider request")
	}
	if coordinatorOpened.Load() != coordinatorClosed.Load() {
		t.Fatal("new job coordinator did not release its connections")
	}
	after, err := fixture.repository.GetJob(t.Context(), blockedJob)
	unchanged := err == nil && bytes.Equal(encode(t, after), encode(t, blockedSnapshot))
	if !unchanged {
		t.Fatal("closing the observer changed the server's historical job")
	}
	documents, err = desktop.ListRemoteDocuments(t.Context(), blockedJob)
	stillPending = err == nil && len(documents) == 1 && !documents[0].Imported
	if !stillPending {
		t.Fatal("cancelling the download fabricated historical import completion")
	}
	t.Logf("history=%d shared_imported=%d pending_history=%d own_visible_after=%s "+
		"downloads=%d open/close=%d/%d max_http=%d",
		historicalJobs, sharedImported, remainingHistory, elapsed, downloads.Load(),
		accounting.opened.Load(), accounting.closed.Load(), accounting.maximumBodies.Load())
}
