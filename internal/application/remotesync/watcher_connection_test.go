package remotesync

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type reconnectRepository struct {
	remoteresults.Repository
	mu     sync.Mutex
	mirror remoteresults.Mirror
	latest jobcontract.Snapshot
}

func (repository *reconnectRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.mirror, nil
}

func (repository *reconnectRepository) ApplyRemoteBatch(
	_ context.Context,
	batch remoteresults.Batch,
) (remoteresults.Mirror, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if batch.Validate() != nil || batch.AfterSequence != repository.mirror.Snapshot.LatestSequence {
		return remoteresults.Mirror{}, remoteresults.ErrConflict
	}
	repository.mirror.Snapshot = repository.latest
	return repository.mirror, nil
}

func (*reconnectRepository) ListRemoteDocuments(context.Context, string) ([]remoteresults.DocumentReference, error) {
	return []remoteresults.DocumentReference{}, nil
}

type reconnectConnection struct {
	watcherConnection
	read  func(context.Context, runnerclient.EventQuery) (jobhost.EventPage, error)
	close func()
}

func (connection *reconnectConnection) ReadEvents(
	ctx context.Context,
	query runnerclient.EventQuery,
) (jobhost.EventPage, error) {
	return connection.read(ctx, query)
}

func (connection *reconnectConnection) Close() error { connection.close(); return nil }

func reconnectFixture(t *testing.T, node Node) (*reconnectRepository, []jobcontract.Event) {
	t.Helper()
	initial := queuedJob(node, 1)
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.invalid", ModelID: "fixture", APIKey: "unused-fixture-secret",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal("performance fixture preparation failed")
	}
	_ = lease.Close()
	prepared.ReportID = initial.Receipt.ReportIDs[0]
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: initial.Receipt.JobID,
		NodeInstallationID: node.Origin.InstallationID, EngineBuildID: initial.Receipt.Origin.EngineBuildID,
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: node.Origin.NodeID},
		Kind:   jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
	}
	if err := initial.Validate(spec); err != nil {
		t.Fatal(err)
	}
	latest := initial
	events := []jobcontract.Event{}
	for index, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		latest.Receipt.Status, latest.Receipt.Revision = status, uint64(index+2)
		latest.LatestSequence = uint64(index + 2)
		latest.UpdatedAt = initial.UpdatedAt.Add(time.Duration(index+1) * time.Second)
		payload, err := json.Marshal(latest)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, jobcontract.Event{Contract: jobcontract.CurrentContract, JobID: spec.JobID,
			Sequence: latest.LatestSequence, Kind: jobcontract.EventState, CreatedAt: latest.UpdatedAt, Payload: payload})
	}
	return &reconnectRepository{
		mirror: remoteresults.Mirror{Origin: node.Origin, Spec: spec, Snapshot: initial}, latest: latest,
	}, events
}

func TestWatcherReconnectsAfterMidSessionConnectionFailure(t *testing.T) {
	for _, failure := range []string{"metadata", "events"} {
		t.Run(failure, func(t *testing.T) {
			node := watcherNode(1)
			repository, events := reconnectFixture(t, node)
			synchronizer, err := New(Dependencies{Repository: repository, Documents: unusedCache{}})
			if err != nil {
				t.Fatal(err)
			}
			var opened, closed, active atomic.Int32
			var resumed, retainedTail atomic.Bool
			jobEntered := make(chan struct{})
			var entered sync.Once
			watcher, err := NewWatcher(t.Context(), WatcherDependencies{
				Synchronizer: synchronizer, DocumentWork: emptyDocumentWork, FactPriorityJobs: emptyPriorityJobs,
				Nodes: func(context.Context) ([]Node, error) { return []Node{node}, nil },
				Connect: func(context.Context, Node) (Connection, error) {
					generation := opened.Add(1)
					if generation > 1 && closed.Load() != generation-1 {
						t.Error("replacement connected before previous connection closed")
					}
					return &reconnectConnection{
						watcherConnection: watcherConnection{node: node,
							list: func(ctx context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
								if generation > 1 && after == 1 {
									retainedTail.Store(true)
								}
								if after == 0 {
									return jobhost.JobPage{Contract: jobcontract.CurrentContract,
										Jobs: []jobcontract.Snapshot{repository.latest}, NextCursor: "1"}, nil
								}
								if generation == 1 && failure == "metadata" {
									select {
									case <-jobEntered:
										return jobhost.JobPage{}, fmt.Errorf("metadata failed: %w", runnerclient.ErrConnection)
									case <-ctx.Done():
										return jobhost.JobPage{}, ctx.Err()
									}
								}
								return emptyJobPage(after), nil
							}},
						read: func(ctx context.Context, query runnerclient.EventQuery) (jobhost.EventPage, error) {
							active.Add(1)
							defer active.Add(-1)
							if generation == 1 {
								entered.Do(func() { close(jobEntered) })
								if failure == "events" {
									return jobhost.EventPage{}, runnerclient.ErrConnection
								}
								<-ctx.Done()
								return jobhost.EventPage{}, ctx.Err()
							}
							if query.AfterSequence != 1 {
								t.Error("replacement did not resume the committed event cursor")
							}
							resumed.Store(true)
							return jobhost.EventPage{Events: events, LastSequence: 3}, nil
						},
						close: func() {
							if active.Load() != 0 {
								t.Error("connection closed before cancelled workers joined")
							}
							closed.Add(1)
						},
					}, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			watcher.attemptTimeout, watcher.pollEvery = time.Hour, 5*time.Millisecond
			done := make(chan error, 1)
			go func() { done <- watcher.Run() }()
			stop := sync.OnceFunc(func() { _ = watcher.Close(); <-done })
			defer stop()
			waitFor(t, func() bool {
				repository.mu.Lock()
				defer repository.mu.Unlock()
				return repository.mirror.Snapshot.LatestSequence == 3 && resumed.Load() && retainedTail.Load()
			})
			if opened.Load() != 2 || closed.Load() != 1 {
				t.Fatal("connection failure did not replace exactly one session")
			}
			stop()
			if closed.Load() != 2 || active.Load() != 0 {
				t.Fatal("watcher shutdown did not join and close the replacement")
			}
		})
	}
}

func TestSessionKeepsConnectionAfterNonConnectionFailures(t *testing.T) {
	for _, failure := range []string{"metadata_validation", "event_integrity", "event_collection_budget"} {
		t.Run(failure, func(t *testing.T) {
			node := watcherNode(1)
			root := queuedJob(node, 1)
			var failed atomic.Int32
			connection := &priorityObservation{watcherConnection: watcherConnection{node: node,
				list: func(_ context.Context, after uint64, _ uint32) (jobhost.JobPage, error) {
					if failure == "metadata_validation" {
						failed.Add(1)
						return jobhost.JobPage{}, remoteresults.ErrInvalid
					}
					if after > 0 {
						return emptyJobPage(after), nil
					}
					return jobhost.JobPage{Contract: jobcontract.CurrentContract,
						Jobs: []jobcontract.Snapshot{root}, NextCursor: "1"}, nil
				}}, events: func(context.Context, string) error {
				failed.Add(1)
				if failure == "event_integrity" {
					return runnerclient.ErrIntegrity
				}
				return remoteresults.ErrCollectionBudget
			}}
			watcher := sessionWatcher(t, node, connection)
			scan := resetDiscovery()
			runObservationUntil(t, watcher, node, &scan, func() bool { return failed.Load() >= 2 })
			if connection.closed.Load() != 1 {
				t.Fatal("non-connection failure ended the session before cancellation")
			}
		})
	}
}
