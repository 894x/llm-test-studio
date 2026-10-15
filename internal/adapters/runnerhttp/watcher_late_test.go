package runnerhttp_test

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
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
	awaitObservation(t, func() bool {
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
