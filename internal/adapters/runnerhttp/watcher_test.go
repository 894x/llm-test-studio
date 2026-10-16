package runnerhttp_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	documentcache "github.com/894x/llm-test-studio/internal/remote/cache"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type observingConnection struct {
	*runnerclient.Client
	closed *atomic.Int64
}

func (connection observingConnection) Close() error { connection.closed.Add(1); return nil }

func startObservingFixture(
	t *testing.T,
	fixture *httpFixture,
	repository *sqlite.Repository,
	cache *documentcache.Cache,
	origin remoteresults.Origin,
) (*remotesync.Watcher, func()) {
	t.Helper()
	synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: repository, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	var opened, closed atomic.Int64
	watcher, err := remotesync.NewWatcher(t.Context(), remotesync.WatcherDependencies{
		Synchronizer: synchronizer,
		PriorityJobs: repository.ListRemoteSubmissionIDs,
		Nodes: func(context.Context) ([]remotesync.Node, error) {
			return []remotesync.Node{{Origin: origin, Revision: 1}}, nil
		},
		Connect: func(context.Context, remotesync.Node) (remotesync.Connection, error) {
			opened.Add(1)
			return observingConnection{Client: httpManagementClient(t, fixture), closed: &closed}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := watcher.Close(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("observer terminated unexpectedly: %v", err)
			}
			if opened.Load() != closed.Load() {
				t.Fatal("observer did not release every client connection")
			}
		})
	}
	t.Cleanup(stop)
	return watcher, stop
}

func awaitObservation(t *testing.T, predicate func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	for !predicate() {
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			t.Fatal("background observation did not become current")
		case <-timer.C:
		}
	}
}

func TestBackgroundWatcherDiscoversSharedJobsAndReopensWithoutControllingExecution(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	firstClient, otherClient := httpManagementClient(t, fixture), httpManagementClient(t, fixture)
	performance := fixture.submission(t, func(command *quicktest.PerformanceCommand) {
		command.WarmupRequests, command.RampDurationMS, command.RampRequestCap = 1, 10, 1
	})
	if _, err := firstClient.Submit(t.Context(), &performance); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("performance warmup did not enter provider")
	}
	run, comparison := fixture.runSubmission(t, false), fixture.runSubmission(t, true)
	for _, submission := range []*jobcontract.Submission{&run, &comparison} {
		if _, err := otherClient.Submit(t.Context(), submission); err != nil {
			t.Fatal(err)
		}
	}
	desktop, path := performanceMirrorStore(t)
	cache, cacheRoot := syncDocuments(t)
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	_, stop := startObservingFixture(
		t,
		fixture,
		desktop,
		cache,
		origin,
	)
	jobIDs := []string{performance.Job.JobID, run.Job.JobID, comparison.Job.JobID}
	awaitObservation(t, func() bool {
		for _, id := range jobIDs {
			if _, err := desktop.GetRemoteMirror(t.Context(), id); err != nil {
				return false
			}
		}
		return true
	})
	stop()
	for _, id := range jobIDs {
		snapshot, err := fixture.repository.GetJob(t.Context(), id)
		if err != nil || jobcontract.Terminal(snapshot.Receipt.Status) {
			t.Fatal("closing the observer controlled accepted work")
		}
	}
	if err := desktop.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.release()
	for _, id := range jobIDs {
		waitHTTPJob(
			t,
			fixture,
			id,
			domain.RunCompleted,
		)
	}
	waitHTTPSettlement(t, firstClient)
	requests := fixture.requests.Load()
	desktop, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer desktop.Close()
	cache, err = documentcache.New(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	watcher, stop := startObservingFixture(
		t,
		fixture,
		desktop,
		cache,
		origin,
	)
	awaitObservation(t, func() bool {
		for _, id := range jobIDs {
			mirror, err := desktop.GetRemoteMirror(t.Context(), id)
			if err != nil || mirror.Snapshot.Receipt.Status != domain.RunCompleted {
				return false
			}
			documents, err := desktop.ListRemoteDocuments(t.Context(), id)
			if err != nil || len(documents) == 0 {
				return false
			}
			for _, document := range documents {
				if !document.Imported {
					return false
				}
			}
		}
		return true
	})
	// The initial scan has reached its tail. A later shared-client job must be
	// discovered while this same observer remains alive.
	later := fixture.runSubmission(t, false)
	if _, err := otherClient.Submit(t.Context(), &later); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		later.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, firstClient)
	requests = fixture.requests.Load()
	awaitObservation(t, func() bool {
		documents, err := desktop.ListRemoteDocuments(t.Context(), later.Job.JobID)
		return err == nil && len(documents) == 1 && documents[0].Imported
	})
	statuses := watcher.Snapshot().Nodes
	if len(statuses) != 1 || statuses[0].ErrorCode != "" {
		t.Fatal("background synchronization reported an unexpected error")
	}
	stop()
	if fixture.requests.Load() != requests {
		t.Fatal("reopening the observer repeated provider traffic")
	}
}
