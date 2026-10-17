package bootstrap

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type productionPriorityRepository struct {
	remoteresults.Repository
	origin remoteresults.Origin
	jobID  string
}

func (repository productionPriorityRepository) ListRemoteFactPriorityIDs(
	_ context.Context,
	origin remoteresults.Origin,
	after string,
	limit uint32,
) (remoteresults.PriorityPage, error) {
	if origin != repository.origin || after != "" || limit != 2 {
		return remoteresults.PriorityPage{}, errors.New("production priority scope changed")
	}
	return remoteresults.PriorityPage{JobIDs: []string{repository.jobID}, NextCursor: repository.jobID}, nil
}

func (productionPriorityRepository) ListRemoteDocumentWork(
	_ context.Context,
	query remoteresults.DocumentQuery,
) (remoteresults.DocumentPage, error) {
	return remoteresults.DocumentPage{Items: []remoteresults.DocumentWork{}, NextCursor: query.After}, nil
}

func (productionPriorityRepository) GetRemoteMirror(context.Context, string) (remoteresults.Mirror, error) {
	return remoteresults.Mirror{}, remoteresults.ErrNotFound
}

type productionPriorityObservation struct {
	productionObservation
	snapshot jobcontract.Snapshot
	read     chan struct{}
}

func (connection *productionPriorityObservation) GetJob(context.Context, string) (jobcontract.Snapshot, error) {
	return connection.snapshot, nil
}

func (connection *productionPriorityObservation) ReadEvents(ctx context.Context, _ runnerclient.EventQuery) (jobhost.EventPage, error) {
	close(connection.read)
	<-ctx.Done()
	return jobhost.EventPage{}, ctx.Err()
}

func TestProductionSynchronizationPrioritizesScopedLocalJobsWithObservationAccess(t *testing.T) {
	root := t.TempDir()
	services, closeNodes, err := NewProductionRemoteNodes(t.Context(), ProductionRemoteNodeDependencies{
		Directory: root, Credentials: credentials.NewMemoryStore(), Usage: unusedRemoteNodeUsage{},
		Options: Options{AppVersion: "v-test", RunnerSource: &unusedRemoteArtifactSource{},
			RemoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
				return nil, remotessh.ErrConnection
			}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeNodes()
	node := saveProductionObservedNode(t, services.configuration, true)
	origin := remoteresults.Origin{ConfigurationID: node.ID,
		NodeID: node.Binding.Identity.NodeID, InstallationID: node.Binding.Identity.InstallationID}
	at := time.Now().UTC()
	connection := &productionPriorityObservation{
		productionObservation: productionObservation{origin: origin, listed: make(chan struct{}), closed: make(chan struct{})},
		read:                  make(chan struct{}),
		snapshot: jobcontract.Snapshot{Receipt: jobcontract.Receipt{
			Contract: jobcontract.CurrentContract, JobID: node.ID, Kind: jobcontract.KindPerformance, AcceptedAt: at,
			Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: origin.NodeID, InstallationID: origin.InstallationID,
				EngineBuildID: strings.Repeat("a", 64), RunnerVersion: "original-executor", OS: "linux", Arch: "amd64"},
			Status: domain.RunQueued, Revision: 1, RunIDs: []string{}, ReportIDs: []string{origin.InstallationID},
		}, LatestSequence: 1, UpdatedAt: at},
	}
	_, closeSync, err := newProductionRemoteSynchronization(t.Context(), remoteSyncDependencies{
		Directory: root, Configuration: services.configuration,
		Repository: productionPriorityRepository{origin: origin, jobID: node.ID},
		Connect: func(_ context.Context, _ *remotenodes.Service, requested remotesync.Node, access jobs.Access) (RunnerConnection, error) {
			if requested.Origin != origin || access != jobs.ObservationAccess {
				t.Error("priority observation changed scope or requested intake authority")
			}
			return connection, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSync()
	select {
	case <-connection.read:
	case <-time.After(2 * time.Second):
		t.Fatal("local submission waited for blocked shared history discovery")
	}
	if err := closeSync(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-connection.closed:
	default:
		t.Fatal("production shutdown did not join priority observation")
	}
}
