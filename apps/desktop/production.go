package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	casebundle "github.com/894x/llm-test-studio/cases"
	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	suitebundle "github.com/894x/llm-test-studio/suites"
)

var desktopApplicationVersion = "dev"

type productionOptions struct {
	userConfigDir       func() (string, error)
	appVersion          string
	caseBundle          fs.FS
	suiteBundle         fs.FS
	executablePath      func() (string, error)
	reportRunDiagnostic func(runs.Diagnostic)
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
		if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{AppVersion: options.appVersion}); err != nil {
			return desktopDependencies{}, fmt.Errorf("migrate desktop database: %w", err)
		}
		repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("open desktop repository: %w", err)
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
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("locate desktop executable: %w", err)
		}
		userCaseRoot, err := casecatalog.UserRootForExecutable(executable)
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("locate executable case directory: %w", err)
		}
		caseFiles, err := casecatalog.New(casecatalog.Options{Builtin: bundle, UserRoot: userCaseRoot})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create filesystem case catalog: %w", err)
		}
		suiteBundleFS := options.suiteBundle
		if isNilInterface(suiteBundleFS) {
			suiteBundleFS = suitebundle.Bundle
		}
		userSuiteRoot, err := suitecatalog.UserRootForExecutable(executable)
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("locate executable suite directory: %w", err)
		}
		suiteFiles, err := suitecatalog.New(suitecatalog.Options{Builtin: suiteBundleFS, UserRoot: userSuiteRoot, Cases: caseFiles})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create filesystem suite catalog: %w", err)
		}
		if err := runs.RecoverInterrupted(ctx, repository, productionClock{}); err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("recover interrupted desktop runs: %w", err)
		}
		workspaceQuery := workspace.New(repository)
		caseTypes := casetypes.MustBuiltinRegistry()
		catalogRepository := filesystemCatalogRepository{Repository: repository, cases: caseFiles, suites: suiteFiles}
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
		credentialStore := credentials.NewOSStore()
		channelService, err := channelconfig.New(channelconfig.Dependencies{
			Repository: repository, Credentials: credentialStore, Clock: productionClock{},
		})
		if err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create channel configuration service: %w", err)
		}
		runService, err := runs.New(runs.Dependencies{
			Repository:  repository,
			Credentials: credentialStore,
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
			Repository: repository, Runner: runService, Clock: productionClock{},
		})
		if err != nil {
			_ = runService.Close()
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("create comparison application service: %w", err)
		}
		gate := &productionServiceGate{}
		quickPerformanceArchive := serializedQuickPerformanceArchive{gate: gate, archive: repository}
		serializedCatalog := serializedCatalogService{
			gate: gate, query: catalogQuery, commands: catalogQuery, channels: channelService,
			caseTypes: caseTypes, caseFiles: caseFiles, suiteFiles: suiteFiles, caseSnapshots: repository,
		}
		return desktopDependencies{
			query:           serializedWorkspaceQuery{gate: gate, query: workspaceQuery},
			catalog:         serializedCatalog,
			catalogCommands: serializedCatalog,
			reports:         serializedReportingQuery{gate: gate, query: reportingQuery},
			commands:        runService,
			comparisons:     comparisonService,
			quickTests: quicktest.New(quicktest.Dependencies{
				Archive:            quickPerformanceArchive,
				Clock:              productionClock{},
				ChannelConnections: quicktest.NewStoredChannelConnectionResolver(repository, credentialStore),
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
