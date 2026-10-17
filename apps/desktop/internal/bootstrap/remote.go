package bootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

type productionRemoteNodes struct {
	Manager       *remotenodes.Manager
	configuration *remotenodes.Service
}

type ProductionRemoteNodeDependencies struct {
	Directory   string
	Credentials credentials.Store
	Usage       remoteNodeUsageReader
	Options     Options
}

func NewProductionRemoteNodes(
	ctx context.Context,
	dependencies ProductionRemoteNodeDependencies,
) (*productionRemoteNodes, func() error, error) {
	if isNilInterface(dependencies.Usage) {
		return nil, nil, remotenodes.ErrUsageUnknown
	}
	applicationDirectory, base, options := dependencies.Directory, dependencies.Credentials, dependencies.Options
	directory := filepath.Join(applicationDirectory, "remote-nodes")
	if err := fileconfig.EnsurePrivateDirectory(ctx, directory); err != nil {
		return nil, nil, err
	}
	scope, err := CredentialScopeForRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	keys, err := credentials.NewScopedStore(base, scope)
	if err != nil {
		return nil, nil, err
	}
	store, err := remotenodes.NewFileStore(filepath.Join(directory, "nodes.json"))
	if err != nil {
		return nil, nil, err
	}
	configuration, err := remotenodes.New(remotenodes.Dependencies{Store: store, Credentials: keys,
		CanRemove: newRemoteNodeRemovalGuard(dependencies.Usage)})
	if err != nil {
		return nil, nil, err
	}
	if _, err := configuration.Snapshot(ctx); err != nil {
		return nil, nil, err
	}
	if err := configuration.Cleanup(ctx); err != nil && options.ReportCredentialCleanup != nil {
		options.ReportCredentialCleanup(bridge.CredentialCleanupDiagnosticError{Err: err})
	}
	source := options.RunnerSource
	if isNilInterface(source) {
		source, err = runnerartifacts.NewGitHubSource("894x/llm-test-studio")
		if err != nil {
			return nil, nil, err
		}
	}
	cacheDirectory := filepath.Join(directory, "runner-releases")
	if err := fileconfig.EnsurePrivateDirectory(ctx, cacheDirectory); err != nil {
		return nil, nil, err
	}
	cache, err := runnerartifacts.NewCache(cacheDirectory, source)
	if err != nil {
		return nil, nil, err
	}
	release := options.RunnerRelease
	if release == "" {
		release = options.AppVersion
	}
	manager, err := remotenodes.NewManager(ctx, remotenodes.ManagerDependencies{
		Configuration: configuration, Artifacts: cache, Release: release, Connect: options.RemoteConnector,
	})
	if err != nil {
		return nil, nil, err
	}
	finished := make(chan error, 1)
	go func() { finished <- manager.Run() }()
	var closeOnce sync.Once
	var closeErr error
	return &productionRemoteNodes{Manager: manager, configuration: configuration}, func() error {
		closeOnce.Do(func() {
			closeErr = manager.Close()
			if err := <-finished; !errors.Is(err, context.Canceled) {
				closeErr = errors.Join(closeErr, err)
			}
		})
		return closeErr
	}, nil
}
