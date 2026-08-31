package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"time"

	casebundle "github.com/894x/llm-studio/cases"
	"github.com/894x/llm-studio/internal/application/casecatalog"
	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/channelconfig"
	"github.com/894x/llm-studio/internal/application/comparisons"
	"github.com/894x/llm-studio/internal/application/quicktest"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/application/workspace"
	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/persistence/sqlite"
)

var desktopApplicationVersion = "dev"

type productionOptions struct {
	userConfigDir       func() (string, error)
	appVersion          string
	caseBundle          fs.FS
	executablePath      func() (string, error)
	reportCaseConflicts func(int)
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
		executablePath: os.Executable,
		reportCaseConflicts: func(count int) {
			log.Printf("llm-studio: %d built-in case update conflict(s) retained user revisions", count)
		},
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
		if err := cutoverLegacyCaseCatalog(ctx, repository, caseFiles, options.reportCaseConflicts); err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("cut over filesystem case catalog: %w", err)
		}
		if err := runs.RecoverInterrupted(ctx, repository, productionClock{}); err != nil {
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("recover interrupted desktop runs: %w", err)
		}
		workspaceQuery := workspace.New(repository)
		catalogQuery, err := catalog.New(catalog.Dependencies{
			Repository: repository,
			Clock:      productionClock{},
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
			Executor: runs.NewExecutorRouter(
				runs.NewLegacyAPIAuditExecutor(nil),
				runs.NewLoadExecutor(nil),
			),
			Clock:       productionClock{},
			Reporter:    reportGenerator,
			ReportError: func(err error) { log.Printf("llm-studio: generate run report: %v", err) },
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
			caseFiles: caseFiles, caseSnapshots: repository,
		}
		return desktopDependencies{
			query:           serializedWorkspaceQuery{gate: gate, query: workspaceQuery},
			catalog:         serializedCatalog,
			catalogCommands: serializedCatalog,
			reports:         serializedReportingQuery{gate: gate, query: reportingQuery},
			commands:        runService,
			comparisons:     comparisonService,
			quickTests: quicktest.New(quicktest.Dependencies{
				Archive: quickPerformanceArchive,
				Clock:   productionClock{},
			}),
			close: func() error {
				return errors.Join(runService.Close(), repository.Close())
			},
		}, nil
	}
}

type legacyCaseCutoverRepository interface {
	ListTestCases(context.Context) ([]domain.TestCase, error)
	CaseCatalogCutoverCompleted(context.Context) (bool, error)
	CompleteCaseCatalogCutover(context.Context, time.Time) error
	PruneUnreferencedTestCaseSnapshots(context.Context) error
}

func cutoverLegacyCaseCatalog(ctx context.Context, repository legacyCaseCutoverRepository, files *casecatalog.Service, report func(int)) error {
	completed, err := repository.CaseCatalogCutoverCompleted(ctx)
	if err != nil {
		return err
	}
	exported := 0
	if !completed {
		legacyCases, err := repository.ListTestCases(ctx)
		if err != nil {
			return err
		}
		entries, err := files.Entries(ctx)
		if err != nil {
			return err
		}
		byID := make(map[string]casecatalog.Entry, len(entries))
		for _, entry := range entries {
			byID[entry.TestCase.ID] = entry
		}
		for _, testCase := range legacyCases {
			entry, found := byID[testCase.ID]
			if found && reflect.DeepEqual(entry.TestCase, testCase) {
				continue
			}
			directory := filesystemCaseDirectory(testCase.Key)
			if found {
				directory = entry.Directory
			}
			if err := files.SaveCase(ctx, string(testCase.Protocol), directory, testCase); err != nil {
				return fmt.Errorf("export legacy case %s: %w", testCase.ID, err)
			}
			exported++
		}
		if err := repository.CompleteCaseCatalogCutover(ctx, time.Now().UTC()); err != nil {
			return err
		}
	}
	if err := repository.PruneUnreferencedTestCaseSnapshots(ctx); err != nil {
		return err
	}
	if exported != 0 && report != nil {
		report(exported)
	}
	return nil
}

func productionStoragePaths(configurationRoot string) (directory string, database string, err error) {
	if configurationRoot == "" || configurationRoot != strings.TrimSpace(configurationRoot) {
		return "", "", errors.New("user configuration directory must be a non-blank absolute path")
	}
	root := filepath.Clean(configurationRoot)
	if !filepath.IsAbs(root) {
		return "", "", errors.New("user configuration directory must be an absolute path")
	}
	directory = filepath.Join(root, "llm-studio")
	database = filepath.Join(directory, "llm-studio.db")
	relative, err := filepath.Rel(root, database)
	if err != nil {
		return "", "", fmt.Errorf("validate desktop database path: %w", err)
	}
	if relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("desktop database path escapes user configuration directory")
	}
	return directory, database, nil
}
