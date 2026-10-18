package runnerhttp_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

func TestBackgroundWatcherRevisitsAnExistingJobForLateReports(t *testing.T) {
	fixture := newHTTPFixture(t, true, capabilities())
	client := httpManagementClient(t, fixture)
	submission := fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("performance request did not reach provider")
	}
	desktop, _ := performanceMirrorStore(t)
	cache, _ := syncDocuments(t)
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	watcher, stop := startObservingFixture(
		t,
		fixture,
		desktop,
		cache,
		origin,
	)
	awaitObservation(t, func() bool {
		mirror, err := desktop.GetRemoteMirror(t.Context(), submission.Job.JobID)
		return err == nil && !jobcontract.Terminal(mirror.Snapshot.Receipt.Status)
	})
	before, err := desktop.GetRemoteMirror(t.Context(), submission.Job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	fixture.release()
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, client)
	requests := fixture.requests.Load()
	// Observation, full result and archive are separate production turns.
	awaitObservationFor(t, 25*time.Second, func() bool {
		mirror, err := desktop.GetRemoteMirror(t.Context(), submission.Job.JobID)
		if err != nil || mirror.Snapshot.LatestSequence <= before.Snapshot.LatestSequence {
			return false
		}
		documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
		return err == nil && len(documents) == 2 && documents[0].Imported && documents[1].Imported
	})
	if statuses := watcher.Snapshot().Nodes; len(statuses) != 1 || statuses[0].ErrorCode != "" {
		t.Fatal("late reports caused a background synchronization error")
	}
	stop()
	if fixture.requests.Load() != requests {
		t.Fatal("late report observation repeated accepted traffic")
	}
}

type partialDocumentObservation struct {
	*runnerclient.Client
	results  atomic.Int32
	archives atomic.Int32
}

func (connection *partialDocumentObservation) Close() error { return nil }

func (connection *partialDocumentObservation) DownloadReport(
	ctx context.Context,
	id string,
	reference jobcontract.ReportReference,
	file io.ReadWriteSeeker,
) error {
	connection.results.Add(1)
	return connection.Client.DownloadReport(
		ctx,
		id,
		reference,
		file,
	)
}

func (connection *partialDocumentObservation) DownloadPerformanceArchive(
	ctx context.Context,
	id string,
	reference jobcontract.ReportReference,
	file io.ReadWriteSeeker,
) error {
	if connection.archives.Add(1) == 1 {
		return errors.New("fixture_archive_download_failed")
	}
	return connection.Client.DownloadPerformanceArchive(
		ctx,
		id,
		reference,
		file,
	)
}

func TestBackgroundWatcherRetriesOnlyPendingPartialDocuments(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	client := httpManagementClient(t, fixture)
	submission := fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, client)
	requests := fixture.requests.Load()
	desktop, _ := performanceMirrorStore(t)
	cache, _ := syncDocuments(t)
	synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: desktop, Documents: cache})
	if err != nil {
		t.Fatal(err)
	}
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	connection := &partialDocumentObservation{Client: client}
	watcher, err := remotesync.NewWatcher(t.Context(), remotesync.WatcherDependencies{
		Synchronizer: synchronizer, FactPriorityJobs: desktop.ListRemoteFactPriorityIDs,
		DocumentWork: desktop.ListRemoteDocumentWork,
		Nodes: func(context.Context) ([]remotesync.Node, error) {
			return []remotesync.Node{{Origin: origin, Revision: 1}}, nil
		},
		Connect: func(context.Context, remotesync.Node) (remotesync.Connection, error) { return connection, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- watcher.Run() }()
	stop := sync.OnceFunc(func() { _ = watcher.Close(); <-done })
	defer stop()
	awaitObservation(t, func() bool {
		documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
		return err == nil && len(documents) == 2 && documents[0].Imported && !documents[1].Imported
	})
	awaitObservationFor(t, 20*time.Second, func() bool {
		documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
		return err == nil && len(documents) == 2 && documents[0].Imported && documents[1].Imported
	})
	if connection.results.Load() != 1 || connection.archives.Load() != 2 {
		t.Fatal("retry downloaded an imported document or omitted the pending archive")
	}
	awaitObservation(t, func() bool {
		nodes := watcher.Snapshot().Nodes
		return len(nodes) == 1 && !nodes[0].AppliedAt.IsZero()
	})
	stop()
	if fixture.requests.Load() != requests {
		t.Fatal("partial-document synchronization repeated accepted execution")
	}
}

func TestTwoDesktopWatchersImportSameRemoteReportAndNotifyAfterCommit(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	client := httpManagementClient(t, fixture)
	submission := fixture.runSubmission(t, false)
	if _, err := client.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		submission.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, client)
	identity := fixture.host.Identity()
	origin := remoteresults.Origin{ConfigurationID: httpMeta(t).ID,
		NodeID: identity.Target.NodeID, InstallationID: identity.InstallationID}
	observed := []*atomic.Bool{}
	for range 2 {
		desktop, _ := performanceMirrorStore(t)
		cache, _ := syncDocuments(t)
		watcher, stop := startObservingFixture(
			t,
			fixture,
			desktop,
			cache,
			origin,
		)
		defer stop()
		committed := &atomic.Bool{}
		observed = append(observed, committed)
		unsubscribe := watcher.SubscribeChanges(func() {
			documents, err := desktop.ListRemoteDocuments(t.Context(), submission.Job.JobID)
			if err == nil && len(documents) == 1 && documents[0].Imported {
				committed.Store(true)
			}
		})
		defer unsubscribe()
	}
	awaitObservationFor(t, 25*time.Second, func() bool {
		return observed[0].Load() && observed[1].Load()
	})
}

func TestBackgroundWatcherFindsAnotherClientsAlreadyCompletedTailJob(t *testing.T) {
	fixture := newHTTPFixture(t, false, capabilities())
	client := httpManagementClient(t, fixture)
	first := fixture.submission(t, nil)
	if _, err := client.Submit(t.Context(), &first); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		first.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, client)
	desktop, _ := performanceMirrorStore(t)
	cache, _ := syncDocuments(t)
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
	awaitObservationFor(t, 20*time.Second, func() bool {
		documents, err := desktop.ListRemoteDocuments(t.Context(), first.Job.JobID)
		return err == nil && len(documents) == 2 && documents[0].Imported && documents[1].Imported
	})
	// This task can finish before its admission row is first discovered. It must
	// still take the tail path rather than wait for a complete history round.
	other := httpManagementClient(t, fixture)
	newJob := fixture.runSubmission(t, false)
	if _, err := other.Submit(t.Context(), &newJob); err != nil {
		t.Fatal(err)
	}
	waitHTTPJob(
		t,
		fixture,
		newJob.Job.JobID,
		domain.RunCompleted,
	)
	waitHTTPSettlement(t, client)
	requests := fixture.requests.Load()
	awaitObservationFor(t, 20*time.Second, func() bool {
		mirror, err := desktop.GetRemoteMirror(t.Context(), newJob.Job.JobID)
		if err != nil || mirror.Snapshot.Receipt.Status != domain.RunCompleted {
			return false
		}
		documents, err := desktop.ListRemoteDocuments(t.Context(), newJob.Job.JobID)
		return err == nil && len(documents) == 1 && documents[0].Imported
	})
	stop()
	if fixture.requests.Load() != requests {
		t.Fatal("shared tail discovery repeated provider work")
	}
}
