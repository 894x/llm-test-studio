package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	native "github.com/894x/llm-test-studio/apps/desktop/internal/native"
	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/data/performanceplans"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/quickhistory"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	workspacestore "github.com/894x/llm-test-studio/internal/persistence/workspace"
)

func NewInitializer(options Options) bridge.Initializer {
	return func(ctx context.Context) (result bridge.Dependencies, initializeError error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if options.UserConfigDir == nil {
			return bridge.Dependencies{}, errors.New("user configuration directory provider is unavailable")
		}
		if strings.TrimSpace(options.AppVersion) == "" {
			return bridge.Dependencies{}, errors.New("desktop application version is required")
		}
		configurationRoot, err := options.UserConfigDir()
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("locate user configuration directory: %w", err)
		}
		directory, database, err := native.StoragePaths(configurationRoot)
		if err != nil {
			return bridge.Dependencies{}, err
		}
		if err := fileconfig.EnsurePrivateDirectory(ctx, directory); err != nil {
			return bridge.Dependencies{}, fmt.Errorf("prepare desktop data directory: %w", err)
		}
		ownership, err := fileconfig.TryAcquireExclusiveLock(ctx, filepath.Join(directory, "desktop-executor.lock"))
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("desktop execution data is already owned or unavailable: %w", err)
		}
		defer func() {
			if initializeError != nil {
				initializeError = errors.Join(initializeError, ownership.Close())
			}
		}()
		if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{
			AppVersion: options.AppVersion,
			Progress: func(progress sqlite.MigrationProgress) {
				bridge.ReportStartupProgress(ctx, "database_"+progress.Stage, progress.FromVersion, progress.ToVersion)
			},
		}); err != nil {
			return bridge.Dependencies{}, fmt.Errorf("migrate desktop database: %w", err)
		}
		bridge.ReportStartupProgress(ctx, "catalogs", 0, 0)
		bundle := options.CaseBundle
		if isNilInterface(bundle) {
			bundle = casebundle.Bundle
		}
		executableProvider := options.ExecutablePath
		if executableProvider == nil {
			executableProvider = os.Executable
		}
		executable, err := executableProvider()
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("locate desktop executable: %w", err)
		}
		executableDirectory := filepath.Dir(executable)
		dataDirectory := filepath.Join(executableDirectory, "data")
		modelFiles, err := modelcatalog.New(filepath.Join(dataDirectory, "models.json"))
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create filesystem model catalog: %w", err)
		}
		channelFiles, err := channelcatalog.New(filepath.Join(dataDirectory, "channels.json"))
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create filesystem channel catalog: %w", err)
		}
		planFiles, err := plancatalog.New(
			filepath.Join(dataDirectory, "plans"),
			plancatalog.WithBuiltin(performanceplans.Bundle),
		)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create filesystem plan catalog: %w", err)
		}
		userCaseRoot, err := casecatalog.UserRootForExecutable(executable)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("locate executable case directory: %w", err)
		}
		caseFiles, err := casecatalog.New(casecatalog.Options{Builtin: bundle, UserRoot: userCaseRoot})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create filesystem case catalog: %w", err)
		}
		suiteBundleFS := options.SuiteBundle
		if isNilInterface(suiteBundleFS) {
			suiteBundleFS = suitebundle.Bundle
		}
		userSuiteRoot, err := suitecatalog.UserRootForExecutable(executable)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("locate executable suite directory: %w", err)
		}
		suiteFiles, err := suitecatalog.New(suitecatalog.Options{Builtin: suiteBundleFS, UserRoot: userSuiteRoot})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create filesystem suite catalog: %w", err)
		}
		catalogRepository := workspacestore.Catalog{
			LockPath: filepath.Join(dataDirectory, ".llm-test-studio-authored-catalog.lock"),
			Models:   modelFiles, Channels: channelFiles,
			Cases: caseFiles, Suites: suiteFiles, Plans: planFiles,
		}
		baseCredentialStore := options.CredentialStore
		if isNilInterface(baseCredentialStore) {
			baseCredentialStore = credentials.NewOSStore()
		}
		// Keep the installation scope stable when catalogs move into data so
		// existing keyring entries and cleanup registrations remain accessible.
		credentialScope, err := CredentialScopeForRoot(executableDirectory)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("scope credential keyring to authored catalog: %w", err)
		}
		credentialStore, err := credentials.NewScopedStore(baseCredentialStore, credentialScope)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create authored-catalog credential store: %w", err)
		}
		quickTaskScope, err := CredentialScopeForRoot(directory)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("scope quick task credentials: %w", err)
		}
		quickTaskCredentials, err := credentials.NewScopedStore(baseCredentialStore, quickTaskScope)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create quick task credential store: %w", err)
		}
		cleanupQueuePath, err := credentialCleanupRegistryPath(directory, executableDirectory)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("locate credential cleanup queue: %w", err)
		}
		cleanupQueue, err := credentials.NewFileCleanupQueue(cleanupQueuePath)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create credential cleanup queue: %w", err)
		}
		bridge.ReportStartupProgress(ctx, "opening_repository", 0, 0)
		repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("open desktop repository: %w", err)
		}
		var localExecutor *productionLocalExecutor
		var readView *sqlite.Repository
		defer func() {
			if initializeError != nil {
				if localExecutor != nil {
					initializeError = errors.Join(initializeError, localExecutor.Close())
				}
				if readView != nil {
					initializeError = errors.Join(initializeError, readView.Close())
				}
				initializeError = errors.Join(initializeError, repository.Close())
			}
		}()
		bridge.ReportStartupProgress(ctx, "recovering", 0, 0)
		localExecutor, err = newProductionLocalExecutor(ctx, repository, options)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create local job host: %w", err)
		}
		if err := runs.RecoverInterrupted(ctx, repository, ProductionClock{}); err != nil {
			return bridge.Dependencies{}, fmt.Errorf("recover interrupted desktop runs: %w", err)
		}
		readView, err = repository.OpenReadOnlyView(ctx)
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("open desktop history read view: %w", err)
		}
		caseTypes := casetypes.MustBuiltinRegistry()
		runtimeRepository := workspacestore.Runtime{Repository: repository, Catalog: catalogRepository}
		queryRepository := workspacestore.Runtime{Repository: readView, Catalog: catalogRepository}
		workspaceQuery := workspace.New(queryRepository)
		catalogQuery, err := catalog.New(catalog.Dependencies{
			Repository: catalogRepository,
			Clock:      ProductionClock{},
			CaseTypes:  caseTypes,
			RepositoryErrors: catalog.RepositoryErrorSet{
				NotFound: sqlite.ErrNotFound,
				Conflict: sqlite.ErrConflict,
				Corrupt:  sqlite.ErrCorrupt,
			},
		})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create desktop catalog service: %w", err)
		}
		reportingQuery := reporting.New(readView)
		reportGenerator, err := reporting.NewGenerator(reporting.GeneratorDependencies{
			Repository: repository,
			Clock:      ProductionClock{},
			Timing: func(timing reporting.GenerationTiming) {
				if options.ReportRunDiagnostic != nil {
					options.ReportRunDiagnostic(runs.Diagnostic{
						RunID: timing.RunID, Operation: "generate_report_" + timing.Phase,
						ErrorCode: "phase_timing", Duration: timing.Duration,
					})
				}
			},
		})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create report generator: %w", err)
		}
		channelService, err := channelconfig.New(channelconfig.Dependencies{
			Repository: catalogRepository, Credentials: credentialStore, CleanupQueue: cleanupQueue, Clock: ProductionClock{},
			ReportCleanupFailure: func(err error) {
				if options.ReportCredentialCleanup != nil {
					options.ReportCredentialCleanup(bridge.CredentialCleanupDiagnosticError{Err: err})
				}
			},
		})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create channel configuration service: %w", err)
		}
		if err := channelService.RetryPendingCredentialCleanup(ctx); err != nil && options.ReportCredentialCleanup != nil {
			options.ReportCredentialCleanup(bridge.CredentialCleanupDiagnosticError{Err: err})
		}
		runService, err := runs.New(runs.Dependencies{
			QuickTaskCredentials: quickTaskCredentials,
			Repository:           runtimeRepository,
			QuickTasks:           catalogRepository,
			CaseTypes:            caseTypes,
			Credentials:          credentialStore,
			Executor:             runs.NewProtocolExecutor(options.ProtocolTransport),
			Clock:                ProductionClock{},
			Reporter:             reportGenerator,
			ReportDiagnostic:     options.ReportRunDiagnostic,
			Environment: func() domain.EnvironmentSnapshot {
				return domain.EnvironmentSnapshot{
					OS: runtime.GOOS, Arch: runtime.GOARCH, Region: "local",
					NetworkEgress: "direct", AppVersion: options.AppVersion, EngineVersion: "go-core-v1",
				}
			},
		})
		if err != nil {
			return bridge.Dependencies{}, fmt.Errorf("create run application service: %w", err)
		}
		comparisonService, err := comparisons.New(comparisons.Dependencies{
			Repository: runtimeRepository, Runner: runService, Clock: ProductionClock{},
		})
		if err != nil {
			_ = runService.Close()
			return bridge.Dependencies{}, fmt.Errorf("create comparison application service: %w", err)
		}
		remoteNodes, closeRemoteNodes, err := NewProductionRemoteNodes(
			ctx,
			ProductionRemoteNodeDependencies{
				Directory: directory, Credentials: baseCredentialStore, Usage: repository, Options: options,
			},
		)
		if err != nil {
			_ = runService.Close()
			return bridge.Dependencies{}, fmt.Errorf("create remote node manager: %w", err)
		}
		remoteSync, closeRemoteSync, err := newProductionRemoteSynchronization(ctx, remoteSyncDependencies{
			Directory: directory, Repository: repository, Configuration: remoteNodes.configuration,
			Connect: options.RunnerConnector,
		})
		if err != nil {
			_ = closeRemoteNodes()
			_ = runService.Close()
			return bridge.Dependencies{}, fmt.Errorf("create remote synchronization: %w", err)
		}
		jobServices, err := newProductionJobs(ctx, repository, productionJobBackends{
			configuration: remoteNodes.configuration, connect: options.RunnerConnector, local: localExecutor.Connect,
		})
		if err != nil {
			_ = closeRemoteSync()
			_ = closeRemoteNodes()
			_ = runService.Close()
			return bridge.Dependencies{}, fmt.Errorf("create job submission coordinator: %w", err)
		}
		gate := &productionServiceGate{}
		quickPerformanceArchive := serializedQuickPerformanceArchive{gate: gate, archive: repository}
		catalogCommands := catalogQuery
		serializedCatalog := serializedCatalogService{
			gate: gate, query: catalogQuery, commands: catalogCommands, channels: channelService,
			targets: channelService, targetCredential: runService.LeaseQuickTaskCredential,
		}
		quickTests := quicktest.New(quicktest.Dependencies{
			TaskCredential: func(ctx context.Context, runID, baseURL string, selectedProtocol domain.Protocol) (*credentials.Lease, error) {
				return runService.LeaseQuickTaskCredential(ctx, runID, baseURL, selectedProtocol)
			},
			TaskPath: func(ctx context.Context, task quicktest.TaskReference, model string, selectedProtocol domain.Protocol) (string, error) {
				return runService.QuickTaskPerformancePath(ctx, runs.QuickTaskCommand{SuiteID: task.SuiteID, SourceRunID: task.SourceRunID, Model: model}, selectedProtocol)
			},
			Archive:            quickPerformanceArchive,
			Clock:              ProductionClock{},
			ChannelConnections: quicktest.NewStoredChannelConnectionResolver(catalogRepository, credentialStore),
		})
		launcher, err := jobs.NewLauncher(jobs.LauncherDependencies{
			Coordinator: jobServices.coordinator, Runs: runService.Preparer, Comparisons: comparisonService.Preparer,
			Performance: quickTests, Now: time.Now,
		})
		if err != nil {
			_ = jobServices.close()
			_ = closeRemoteSync()
			_ = closeRemoteNodes()
			_ = runService.Close()
			return bridge.Dependencies{}, fmt.Errorf("create job launcher: %w", err)
		}
		return bridge.Dependencies{
			QuickHistory:           quickhistory.New(readView),
			Query:                  serializedWorkspaceQuery{gate: gate, query: workspaceQuery},
			Catalog:                serializedCatalog,
			CatalogCommands:        serializedCatalog,
			Reports:                serializedReportingQuery{gate: gate, query: reportingQuery},
			ReportGeneration:       reportGenerator,
			Commands:               runService,
			Comparisons:            comparisonService,
			RemoteNodes:            remoteNodes.Manager,
			RemoteSync:             remoteSync,
			Jobs:                   jobServices.coordinator,
			Launcher:               launcher,
			Controls:               jobServices.controller,
			Confirmations:          jobServices.confirmations,
			ControlDelivery:        jobServices.deliveries,
			PerformancePreparation: quickTests,
			JobQuery:               readView,
			ControlRecords:         repository,
			Close: func() error {
				return errors.Join(
					jobServices.close(),
					closeRemoteSync(),
					closeRemoteNodes(),
					runService.Close(),
					localExecutor.Close(),
					readView.Close(),
					repository.Close(),
					ownership.Close(),
				)
			},
		}, nil
	}
}
