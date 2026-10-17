package remotesync

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

// This fixture isolates the per-observation work limit. Full journal semantics and
// original document imports are verified by the runnerhttp integration tests.
type cursorRepository struct {
	remoteresults.Repository
	mirror remoteresults.Mirror
}

func (repository *cursorRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	return repository.mirror, nil
}

func (repository *cursorRepository) ApplyRemoteBatch(
	_ context.Context,
	batch remoteresults.Batch,
) (remoteresults.Mirror, error) {
	if repository.mirror.Snapshot.LatestSequence != batch.AfterSequence {
		return remoteresults.Mirror{}, remoteresults.ErrConflict
	}
	repository.mirror.Snapshot.LatestSequence += uint64(len(batch.Events))
	return repository.mirror, nil
}

func (*cursorRepository) ListRemoteDocuments(context.Context, string) ([]remoteresults.DocumentReference, error) {
	return []remoteresults.DocumentReference{}, nil
}

type endlessRemote struct {
	Remote
	calls     int
	lastAfter uint64
	empty     bool
	origin    remoteresults.Origin
}

func (remote *endlessRemote) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return jobcontract.RunnerInfo{NodeID: remote.origin.NodeID, NodeInstallationID: remote.origin.InstallationID}, nil
}

func (remote *endlessRemote) ReadEvents(_ context.Context, query runnerclient.EventQuery) (jobhost.EventPage, error) {
	remote.calls++
	remote.lastAfter = query.AfterSequence
	if remote.empty {
		return jobhost.EventPage{Events: []jobcontract.Event{}, LastSequence: query.AfterSequence, HasMore: true}, nil
	}
	event := jobcontract.Event{JobID: query.JobID, Sequence: query.AfterSequence + 1,
		Kind: jobcontract.EventProgress, CreatedAt: time.Now().UTC(), Payload: []byte(`{"fixture":true}`)}
	return jobhost.EventPage{Events: []jobcontract.Event{event}, LastSequence: event.Sequence, HasMore: true}, nil
}

type unusedCache struct{}

func (unusedCache) Read(
	context.Context,
	jobcontract.ReportReference,
	func(context.Context, io.ReadWriteSeeker) error,
) ([]byte, error) {
	return nil, errors.New("unexpected document read in cursor fixture")
}

func TestObserveBoundsPaginationAndResumesCommittedCursor(t *testing.T) {
	origin := remoteresults.Origin{ConfigurationID: "11111111-1111-4111-8111-111111111111",
		NodeID: "22222222-2222-4222-8222-222222222222", InstallationID: "33333333-3333-4333-8333-333333333333"}
	repository := &cursorRepository{mirror: remoteresults.Mirror{Origin: origin}}
	synchronizer, err := New(Dependencies{Repository: repository, Documents: unusedCache{}})
	if err != nil {
		t.Fatal(err)
	}
	remote := &endlessRemote{origin: origin}
	request := Request{Origin: origin, JobID: "44444444-4444-4444-8444-444444444444", Remote: remote}
	for pass := 1; pass <= 2; pass++ {
		outcome, err := synchronizer.Observe(t.Context(), request)
		want := uint64(pass * maxPagesPerObservation)
		if err != nil || !outcome.MoreEvents || outcome.Mirror.Snapshot.LatestSequence != want {
			t.Fatalf("observation did not stop at its work bound: %v", err)
		}
		if remote.calls != int(want) || remote.lastAfter != want-1 {
			t.Fatal("observation restarted or fetched beyond the committed cursor")
		}
	}
	request.Origin.ConfigurationID = "55555555-5555-4555-8555-555555555555"
	if _, err := synchronizer.Observe(t.Context(), request); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal("source switch was not rejected before observation")
	}
	if remote.calls != 2*maxPagesPerObservation {
		t.Fatal("source conflict opened a remote request")
	}
	request.Origin = origin
	remote.origin.InstallationID = "55555555-5555-4555-8555-555555555555"
	if _, err := synchronizer.Observe(t.Context(), request); !errors.Is(err, remoteresults.ErrConflict) {
		t.Fatal("wrong remote installation was not rejected")
	}
	if remote.calls != 2*maxPagesPerObservation {
		t.Fatal("wrong remote installation read an event page")
	}
	remote.origin = origin
	remote.empty = true
	before := repository.mirror.Snapshot.LatestSequence
	if _, err := synchronizer.Observe(t.Context(), request); !errors.Is(err, runnerclient.ErrIntegrity) {
		t.Fatal("endless empty page was accepted")
	}
	if repository.mirror.Snapshot.LatestSequence != before {
		t.Fatal("invalid page advanced the durable cursor")
	}
}
