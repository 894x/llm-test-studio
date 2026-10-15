package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	documentcache "github.com/894x/llm-test-studio/internal/remote/cache"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type runnerConnection interface {
	remotesync.Connection
	jobs.Backend
}

type runnerConnector func(
	context.Context,
	*remotenodes.Service,
	remotesync.Node,
	jobs.Access,
) (runnerConnection, error)

type remoteSyncDependencies struct {
	Directory     string
	Repository    remoteresults.Repository
	Configuration *remotenodes.Service
	Connect       runnerConnector
}

func newProductionRemoteSynchronization(
	ctx context.Context,
	dependencies remoteSyncDependencies,
) (*remotesync.Watcher, func() error, error) {
	validDependencies := dependencies.Configuration != nil && dependencies.Repository != nil
	if ctx == nil || !validDependencies {
		return nil, nil, remoteresults.ErrInvalid
	}
	directory := filepath.Join(dependencies.Directory, "remote-documents")
	if err := fileconfig.EnsurePrivateDirectory(ctx, directory); err != nil {
		return nil, nil, err
	}
	cache, err := documentcache.New(directory)
	if err != nil {
		return nil, nil, err
	}
	synchronizer, err := remotesync.New(remotesync.Dependencies{Repository: dependencies.Repository, Documents: cache})
	if err != nil {
		return nil, nil, err
	}
	connect := dependencies.Connect
	if connect == nil {
		connect = openProductionRunner
	}
	watcher, err := remotesync.NewWatcher(ctx, remotesync.WatcherDependencies{
		Synchronizer: synchronizer,
		Nodes: func(ctx context.Context) ([]remotesync.Node, error) {
			snapshot, err := dependencies.Configuration.Snapshot(ctx)
			if err != nil {
				return nil, err
			}
			nodes := []remotesync.Node{}
			for _, view := range snapshot.Nodes {
				paired := view.HostKey != nil && domain.IsUUID(view.RunnerNodeID) && domain.IsUUID(view.InstallationID)
				observable := paired && installationResolved(view.Installation)
				if !observable {
					continue
				}
				nodes = append(nodes, remotesync.Node{Revision: view.Revision, Origin: remoteresults.Origin{
					ConfigurationID: view.ID, NodeID: view.RunnerNodeID, InstallationID: view.InstallationID,
				}})
			}
			return nodes, nil
		},
		Connect: func(ctx context.Context, node remotesync.Node) (remotesync.Connection, error) {
			return connect(
				ctx,
				dependencies.Configuration,
				node,
				jobs.ObservationAccess,
			)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	var once sync.Once
	var closeError error
	return watcher, func() error {
		once.Do(func() {
			closeError = watcher.Close()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				closeError = errors.Join(closeError, err)
			}
		})
		return closeError
	}, nil
}

func installationResolved(installation *remotenodes.Installation) bool {
	return installation == nil || (installation.Stage == "completed" && !installation.CleanupPending &&
		installation.ErrorCode == "")
}

type productionRunnerConnection struct {
	*runnerclient.Client
	node       *remotessh.Node
	ssh        *remotessh.Client
	once       sync.Once
	closeError error
}

func (connection *productionRunnerConnection) Close() error {
	connection.once.Do(func() {
		connection.closeError = errors.Join(connection.node.Close(), connection.ssh.Close())
	})
	return connection.closeError
}

func (connection *productionRunnerConnection) Probe(ctx context.Context) (jobcontract.RunnerInfo, error) {
	info, err := connection.Client.Probe(ctx)
	return info, runnerBackendError(err)
}

func (connection *productionRunnerConnection) GetJob(ctx context.Context, id string) (jobcontract.Snapshot, error) {
	snapshot, err := connection.Client.GetJob(ctx, id)
	return snapshot, runnerBackendError(err)
}

func (connection *productionRunnerConnection) Submit(
	ctx context.Context,
	submission *jobcontract.Submission,
) (jobcontract.Receipt, error) {
	receipt, err := connection.Client.Submit(ctx, submission)
	return receipt, runnerBackendError(err)
}

func runnerBackendError(err error) error {
	if errors.Is(err, runnerinstall.ErrUpgradeRequired) || errors.Is(err, jobcontract.ErrUnsupported) {
		return jobs.ErrUpgradeRequired
	}
	var remote *runnerclient.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "not_found":
			return jobs.ErrNotFound
		case "unsupported_contract", "capability_missing":
			return jobs.ErrUpgradeRequired
		}
	}
	return err
}

func openProductionRunner(
	ctx context.Context,
	configuration *remotenodes.Service,
	requested remotesync.Node,
	access jobs.Access,
) (runnerConnection, error) {
	validRequest := requested.Origin.Validate() == nil && requested.Revision > 0
	validConfiguration := ctx != nil && configuration != nil
	validAccess := access == jobs.SubmissionAccess || access == jobs.ObservationAccess
	if !validConfiguration || !validRequest || !validAccess {
		return nil, remotenodes.ErrInvalid
	}
	connection, err := configuration.AcquireSSH(ctx, requested.Origin.ConfigurationID, requested.Revision)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	node := connection.Node
	validState := node.HostKey != nil && node.Binding != nil && installationResolved(node.Installation)
	if !validState {
		return nil, remotenodes.ErrRePairRequired
	}
	if access == jobs.SubmissionAccess && !node.Enabled {
		return nil, jobs.ErrUnavailable
	}
	identity := node.Binding.Identity
	validSource := identity.NodeID == requested.Origin.NodeID && identity.InstallationID == requested.Origin.InstallationID
	if !validSource {
		return nil, jobs.ErrTargetChanged
	}
	token, err := configuration.AcquireToken(ctx, node.ID, node.Revision)
	if err != nil {
		return nil, err
	}
	defer token.Close()
	client, err := remotessh.Dial(ctx, node.Endpoint(), connection.Authentication)
	if err != nil {
		return nil, err
	}
	bound, _, err := client.ConnectBoundNode(ctx, identity, token)
	if err != nil {
		_ = client.Close()
		return nil, runnerBackendError(err)
	}
	management, err := runnerclient.New(runnerclient.Identity{
		NodeID: identity.NodeID, InstallationID: identity.InstallationID,
	}, bound.Do)
	if err != nil {
		_ = bound.Close()
		_ = client.Close()
		return nil, err
	}
	return &productionRunnerConnection{Client: management, node: bound, ssh: client}, nil
}
