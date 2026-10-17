package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type absentNodeSubmissionRepository struct {
	jobs.SubmissionRepository
	creates atomic.Int64
}

func (repository *absentNodeSubmissionRepository) CreateSubmission(
	context.Context,
	jobs.SubmissionRecord,
) (jobs.SubmissionRecord, error) {
	repository.creates.Add(1)
	return jobs.SubmissionRecord{}, jobs.ErrConflict
}

func TestProductionPreparationRejectsUnknownConfiguredNodeBeforePersistenceOrConnections(t *testing.T) {
	var sshCalls, remoteCalls, localCalls atomic.Int64
	nodes, closeNodes, err := NewProductionRemoteNodes(t.Context(), ProductionRemoteNodeDependencies{
		Directory: t.TempDir(), Credentials: credentials.NewMemoryStore(), Usage: unusedRemoteNodeUsage{},
		Options: Options{AppVersion: "unknown-node-test", RunnerSource: &unusedRemoteArtifactSource{},
			RemoteConnector: func(
				context.Context,
				remotessh.Endpoint,
				remotessh.Authentication,
			) (remotenodes.RemoteSession, error) {
				sshCalls.Add(1)
				return nil, remotessh.ErrConnection
			}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := closeNodes(); err != nil {
			t.Error(err)
		}
	})
	before, err := nodes.configuration.Snapshot(t.Context())
	if err != nil || len(before.Nodes) != 0 {
		t.Fatalf("unknown-node fixture must have an empty configuration: %v", err)
	}
	backends := productionJobBackends{
		configuration: nodes.configuration,
		local: func(context.Context) (jobs.Backend, error) {
			localCalls.Add(1)
			return nil, jobs.ErrUnavailable
		},
		connect: func(
			context.Context,
			*remotenodes.Service,
			remotesync.Node,
			jobs.Access,
		) (RunnerConnection, error) {
			remoteCalls.Add(1)
			return nil, jobs.ErrUnavailable
		},
	}
	repository := &absentNodeSubmissionRepository{}
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{
		Repository: repository, Open: backends.Open, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := desktopSubmissionFixture(t).Prepared.Spec
	location := jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: "d1045ce1-0c82-43d4-8f26-9a44e7ce21e0"}
	if location.Validate() != nil || spec.Validate() != nil {
		t.Fatal("unknown-node request must have a valid location UUID and frozen payload")
	}
	_, err = coordinator.Prepare(t.Context(), jobs.PrepareCommand{
		Location: location, JobID: spec.JobID, Kind: spec.Kind, Payload: spec.Spec, Assets: spec.Assets,
	})
	if !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatalf("unknown configuration must be rejected before preparation: %v", err)
	}
	noConnections := sshCalls.Load() == 0 && remoteCalls.Load() == 0 && localCalls.Load() == 0
	if !noConnections || repository.creates.Load() != 0 {
		t.Fatal("unknown configuration connected, fell back to local, or persisted a submission")
	}
	after, err := nodes.configuration.Snapshot(t.Context())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("rejecting an unknown configuration changed the node settings")
	}
}
