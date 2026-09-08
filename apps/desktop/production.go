package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

var desktopApplicationVersion = "dev"

type productionOptions struct {
	userConfigDir           func() (string, error)
	appVersion              string
	caseBundle              fs.FS
	suiteBundle             fs.FS
	executablePath          func() (string, error)
	reportRunDiagnostic     func(runs.Diagnostic)
	reportCredentialCleanup func(error)
	credentialStore         credentials.Store
}

type productionClock struct{}

func (productionClock) Now() time.Time {
	return time.Now().UTC()
}

func defaultProductionOptions() productionOptions {
	return productionOptions{
		userConfigDir:  os.UserConfigDir,
		appVersion:     desktopApplicationVersion,
		caseBundle:     casebundle.Bundle,
		suiteBundle:    suitebundle.Bundle,
		executablePath: os.Executable,
	}
}

func newProductionInitializer(options productionOptions) desktopInitializer {
	return func(ctx context.Context) (desktopDependencies, error) {
		if ctx == nil {
			ctx = context.Background()
		}
		if options.userConfigDir == nil {
			return desktopDependencies{}, errors.New("user configuration directory provider is unavailable")
		}
		if strings.TrimSpace(options.appVersion) == "" {
			return desktopDependencies{}, errors.New("desktop application version is required")
		}
		configurationRoot, err := options.userConfigDir()
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate user configuration directory: %w", err)
		}
		directory, database, err := productionStoragePaths(configurationRoot)
		if err != nil {
			return desktopDependencies{}, err
		}
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return desktopDependencies{}, fmt.Errorf("create desktop data directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			return desktopDependencies{}, fmt.Errorf("secure desktop data directory: %w", err)
		}
		if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{
			AppVersion: options.appVersion,
		}); err != nil {
			return desktopDependencies{}, fmt.Errorf("migrate desktop database: %w", err)
		}
		bundle := options.caseBundle
		if isNilInterface(bundle) {
			bundle = casebundle.Bundle
		}
		executableProvider := options.executablePath
		if executableProvider == nil {
			executableProvider = os.Executable
		}
		executable, err := executableProvider()
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate desktop executable: %w", err)
		}
		executableDirectory := filepath.Dir(executable)
		dataDirectory := filepath.Join(executableDirectory, "data")
		modelFiles, err := modelcatalog.New(filepath.Join(dataDirectory, "models.json"))
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem model catalog: %w", err)
		}
		channelFiles, err := channelcatalog.New(filepath.Join(dataDirectory, "channels.json"))
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem channel catalog: %w", err)
		}
		planFiles, err := plancatalog.New(filepath.Join(dataDirectory, "plans"))
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem plan catalog: %w", err)
		}
		userCaseRoot, err := casecatalog.UserRootForExecutable(executable)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate executable case directory: %w", err)
		}
		caseFiles, err := casecatalog.New(casecatalog.Options{Builtin: bundle, UserRoot: userCaseRoot})
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem case catalog: %w", err)
		}
		suiteBundleFS := options.suiteBundle
		if isNilInterface(suiteBundleFS) {
			suiteBundleFS = suitebundle.Bundle
		}
		userSuiteRoot, err := suitecatalog.UserRootForExecutable(executable)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate executable suite directory: %w", err)
		}
		suiteFiles, err := suitecatalog.New(suitecatalog.Options{Builtin: suiteBundleFS, UserRoot: userSuiteRoot, Cases: caseFiles})
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem suite catalog: %w", err)
		}
		catalogRepository := filesystemCatalogRepository{
			lockPath: filepath.Join(dataDirectory, ".llm-test-studio-authored-catalog.lock"),
			models:   modelFiles, channels: channelFiles,
			cases: caseFiles, suites: suiteFiles, plans: planFiles,
		}
		baseCredentialStore := options.credentialStore
		if isNilInterface(baseCredentialStore) {
			baseCredentialStore = credentials.NewOSStore()
		}
		// Keep the installation scope stable when catalogs move into data so
		// existing keyring entries and cleanup registrations remain accessible.
		credentialScope, err := credentialScopeForRoot(executableDirectory)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("scope credential keyring to authored catalog: %w", err)
		}
		credentialStore, err := credentials.NewScopedStore(baseCredentialStore, credentialScope)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create authored-catalog credential store: %w", err)
		}
		quickTaskScope, err := credentialScopeForRoot(directory)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("scope quick task credentials: %w", err)
		}
		quickTaskCredentials, err := credentials.NewScopedStore(baseCredentialStore, quickTaskScope)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create quick task credential store: %w", err)
		}
		cleanupQueuePath, err := credentialCleanupRegistryPath(directory, executableDirectory)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate credential cleanup queue: %w", err)
		}
		cleanupQueue, err := credentials.NewFileCleanupQueue(cleanupQueuePath)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create credential cleanup queue: %w", err)
		}
		repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("open desktop repository: %w", err)
		}
		if err := runs.RecoverInterrupted(ctx, repository, productionClock{}); err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("recover interrupted desktop runs: %w", err)
		}
		caseTypes := casetypes.MustBuiltinRegistry()
		runtimeRepository := filesystemRuntimeRepository{Repository: repository, catalog: catalogRepository}
		workspaceQuery := workspace.New(runtimeRepository)
		catalogQuery, err := catalog.New(catalog.Dependencies{
			Repository: catalogRepository,
			Clock:      productionClock{},
			CaseTypes:  caseTypes,
			RepositoryErrors: catalog.RepositoryErrorSet{
				NotFound: sqlite.ErrNotFound,
				Conflict: sqlite.ErrConflict,
				Corrupt:  sqlite.ErrCorrupt,
			},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create desktop catalog service: %w", err)
		}
		reportingQuery := reporting.New(repository)
		reportGenerator, err := reporting.NewGenerator(reporting.GeneratorDependencies{
			Repository: repository,
			Clock:      productionClock{},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create report generator: %w", err)
		}
		channelService, err := channelconfig.New(channelconfig.Dependencies{
			Repository: catalogRepository, Credentials: credentialStore, CleanupQueue: cleanupQueue, Clock: productionClock{},
			ReportCleanupFailure: func(err error) {
				if options.reportCredentialCleanup != nil {
					options.reportCredentialCleanup(credentialCleanupDiagnosticError{err: err})
				}
			},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create channel configuration service: %w", err)
		}
		if err := channelService.RetryPendingCredentialCleanup(ctx); err != nil && options.reportCredentialCleanup != nil {
			options.reportCredentialCleanup(credentialCleanupDiagnosticError{err: err})
		}
		runService, err := runs.New(runs.Dependencies{
			QuickTaskCredentials: quickTaskCredentials,
			Repository:           runtimeRepository,
			QuickTasks:           catalogRepository,
			CaseTypes:            caseTypes,
			Credentials:          credentialStore,
			Executor: runs.MustExecutorRouter(caseTypes, map[domain.CaseType]runs.Executor{
				casetypes.TypeLegacyAPIAudit:     runs.NewLegacyAPIAuditExecutor(nil),
				casetypes.TypeRequestSingle:      runs.NewLoadExecutor(nil),
				casetypes.TypeResponseProbe:      runs.NewResponseProbeExecutor(nil),
				casetypes.TypeInputLatencyLadder: runs.NewInputLatencyLadderExecutor(nil),
			}),
			Clock:            productionClock{},
			Reporter:         reportGenerator,
			ReportDiagnostic: options.reportRunDiagnostic,
			Environment: func() domain.EnvironmentSnapshot {
				return domain.EnvironmentSnapshot{
					OS: runtime.GOOS, Arch: runtime.GOARCH, Region: "local",
					NetworkEgress: "direct", AppVersion: options.appVersion, EngineVersion: "go-core-v1",
				}
			},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create run application service: %w", err)
		}
		comparisonService, err := comparisons.New(comparisons.Dependencies{
			Repository: runtimeRepository, Runner: runService, Clock: productionClock{},
		})
		if err != nil {
			_ = runService.Close()
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create comparison application service: %w", err)
		}
		gate := &productionServiceGate{}
		quickPerformanceArchive := serializedQuickPerformanceArchive{gate: gate, archive: repository}
		catalogCommands := catalogQuery
		serializedCatalog := serializedCatalogService{
			gate: gate, query: catalogQuery, commands: catalogCommands, channels: channelService,
		}
		return desktopDependencies{
			query:           serializedWorkspaceQuery{gate: gate, query: workspaceQuery},
			catalog:         serializedCatalog,
			catalogCommands: serializedCatalog,
			reports:         serializedReportingQuery{gate: gate, query: reportingQuery},
			commands:        runService,
			comparisons:     comparisonService,
			quickTests: quicktest.New(quicktest.Dependencies{
				TaskCredential: func(ctx context.Context, runID, baseURL string) (*credentials.Lease, error) {
					return runService.LeaseQuickTaskCredential(ctx, runID, baseURL, domain.ProtocolOpenAIChat)
				},
				TaskPath: func(ctx context.Context, task quicktest.TaskReference, model string) (string, error) {
					return runService.QuickTaskPerformancePath(ctx, runs.QuickTaskCommand{SuiteID: task.SuiteID, SuiteRevision: task.SuiteRevision, SourceRunID: task.SourceRunID, Model: model})
				},
				Archive:            quickPerformanceArchive,
				Clock:              productionClock{},
				ChannelConnections: quicktest.NewStoredChannelConnectionResolver(catalogRepository, credentialStore),
			}),
			close: func() error {
				return errors.Join(runService.Close(), repository.Close())
			},
		}, nil
	}
}

func productionStoragePaths(configurationRoot string) (directory string, database string, err error) {
	if configurationRoot == "" || configurationRoot != strings.TrimSpace(configurationRoot) {
		return "", "", errors.New("user configuration directory must be a non-blank absolute path")
	}
	root := filepath.Clean(configurationRoot)
	if !filepath.IsAbs(root) {
		return "", "", errors.New("user configuration directory must be an absolute path")
	}
	directory = filepath.Join(root, "llm-test-studio")
	database = filepath.Join(directory, "llm-test-studio.db")
	relative, err := filepath.Rel(root, database)
	if err != nil {
		return "", "", fmt.Errorf("validate desktop database path: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("desktop database path escapes user configuration directory")
	}
	return directory, database, nil
}

func credentialCleanupRegistryPath(configurationDirectory, authoredCatalogRoot string) (string, error) {
	configurationDirectory = filepath.Clean(strings.TrimSpace(configurationDirectory))
	if configurationDirectory == "." || !filepath.IsAbs(configurationDirectory) {
		return "", errors.New("credential cleanup registry paths must be absolute")
	}
	scope, err := credentialScopeForRoot(authoredCatalogRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(configurationDirectory, "credential-keyring-registries", scope+".json"), nil
}

func credentialScopeForRoot(root string) (string, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || !filepath.IsAbs(root) {
		return "", errors.New("credential storage root must be an absolute path")
	}
	canonicalRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		canonicalRoot = filepath.Clean(resolved)
	}
	if runtime.GOOS == "windows" {
		canonicalRoot = strings.ToLower(canonicalRoot)
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonicalRoot)))
	return hex.EncodeToString(digest[:]), nil
}
