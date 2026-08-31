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
	"sort"
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

const (
	builtinKimiK3SeedKey   = "kimi-k3-official-v1"
	builtinKimiK3SuiteName = "内置 · Kimi K3 官方兼容性"
	builtinKimiK3PlanName  = "内置 · Kimi K3 官方兼容性验证"
	builtinKimiK3SuiteID   = "6b934c5b-76ab-4bd5-a06a-000000000001"
	builtinKimiK3PlanID    = "6b934c5b-76ab-4bd5-a06a-000000000002"
)

type productionOptions struct {
	userConfigDir       func() (string, error)
	appVersion          string
	caseBundle          fs.FS
	executablePath      func() (string, error)
	reportCaseConflicts func(int)
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
			caseFiles: caseFiles, caseSnapshots: repository,
		}
		if err := seedBuiltinKimiK3Catalog(ctx, repository, caseFiles, serializedCatalog); err != nil {
			_ = runService.Close()
			_ = repository.Close()
			return desktopDependencies{}, fmt.Errorf("seed built-in Kimi K3 catalog: %w", err)
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

type builtinCatalogSeedRepository interface {
	BuiltinCatalogSeedCompleted(context.Context, string) (bool, error)
	CompleteBuiltinCatalogSeed(context.Context, string, time.Time) error
	CreateSuite(context.Context, domain.Suite) error
	CreatePlan(context.Context, domain.Plan) error
}

func seedBuiltinKimiK3Catalog(ctx context.Context, repository builtinCatalogSeedRepository, files *casecatalog.Service, service serializedCatalogService) error {
	completed, err := repository.BuiltinCatalogSeedCompleted(ctx, builtinKimiK3SeedKey)
	if err != nil || completed {
		return err
	}
	entries, err := files.Entries(ctx)
	if err != nil {
		return err
	}
	refs := make([]catalog.CaseRevisionInput, 0)
	for _, entry := range entries {
		testCase := entry.TestCase
		if testCase.Protocol == domain.ProtocolKimiK3 && testCase.Enabled && testCase.ExecutionMode == domain.CaseExecutionAutomatic {
			refs = append(refs, catalog.CaseRevisionInput{CaseID: testCase.ID, Revision: testCase.Revision})
		}
	}
	sort.Slice(refs, func(left, right int) bool { return refs[left].CaseID < refs[right].CaseID })
	if len(refs) == 0 {
		return repository.CompleteBuiltinCatalogSeed(ctx, builtinKimiK3SeedKey, time.Now().UTC())
	}

	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		return err
	}
	suite := catalog.MutationResult{}
	suiteCreated := false
	for _, candidate := range snapshot.Suites {
		if candidate.Name == builtinKimiK3SuiteName && reflect.DeepEqual(candidate.Cases, refs) {
			suite = catalog.MutationResult{ID: candidate.ID, Revision: candidate.Revision}
			break
		}
	}
	if suite.ID == "" {
		createdCases, materializeErr := service.materializeCases(ctx, refs)
		if materializeErr != nil {
			return materializeErr
		}
		now := time.Now().UTC()
		seedSuite := domain.Suite{
			EntityMeta: domain.EntityMeta{ID: builtinKimiK3SuiteID, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now},
			Name:       builtinKimiK3SuiteName,
			Cases:      toDomainCaseRefs(refs),
		}
		err = repository.CreateSuite(ctx, seedSuite)
		if errors.Is(err, sqlite.ErrConflict) {
			snapshot, err = service.Snapshot(ctx)
			if err == nil {
				for _, candidate := range snapshot.Suites {
					if candidate.ID == builtinKimiK3SuiteID && candidate.Name == builtinKimiK3SuiteName && reflect.DeepEqual(candidate.Cases, refs) {
						suite = catalog.MutationResult{ID: candidate.ID, Revision: candidate.Revision}
						break
					}
				}
			}
			if err != nil || suite.ID == "" {
				return errors.Join(err, service.cleanupMaterializedCases(createdCases))
			}
		} else if err != nil {
			return errors.Join(err, service.cleanupMaterializedCases(createdCases))
		} else {
			suite = catalog.MutationResult{ID: seedSuite.ID, Revision: seedSuite.Revision}
			suiteCreated = true
		}
	}
	for _, candidate := range snapshot.Plans {
		if builtinKimiK3PlanMatches(candidate, suite, refs) {
			return repository.CompleteBuiltinCatalogSeed(ctx, builtinKimiK3SeedKey, time.Now().UTC())
		}
	}
	now := time.Now().UTC()
	seedPlan := domain.Plan{
		EntityMeta:    domain.EntityMeta{ID: builtinKimiK3PlanID, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:          builtinKimiK3PlanName,
		SuiteID:       suite.ID,
		SuiteRevision: suite.Revision,
		Cases:         toDomainCaseRefs(refs),
		Load:          domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: uint64(len(refs)), RequestTimeoutMS: 300_000},
		SLA:           domain.SLAProfile{Thresholds: map[string]float64{"success_rate": 1}},
	}
	err = repository.CreatePlan(ctx, seedPlan)
	plan := catalog.MutationResult{ID: seedPlan.ID, Revision: seedPlan.Revision}
	planCreated := err == nil
	if errors.Is(err, sqlite.ErrConflict) {
		snapshot, err = service.Snapshot(ctx)
		found := false
		if err == nil {
			for _, candidate := range snapshot.Plans {
				if candidate.ID == builtinKimiK3PlanID && builtinKimiK3PlanMatches(candidate, suite, refs) {
					plan = catalog.MutationResult{ID: candidate.ID, Revision: candidate.Revision}
					found = true
					break
				}
			}
		}
		if err == nil && found {
			err = nil
		} else if err == nil {
			err = sqlite.ErrConflict
		}
	}
	if err != nil {
		var cleanupErr error
		if suiteCreated {
			cleanupErr = service.DeleteSuite(context.Background(), catalog.DeleteCommand{ID: suite.ID, ExpectedRevision: suite.Revision})
			cleanupErr = errors.Join(cleanupErr, service.cleanupMaterializedCases(toDomainCaseRefs(refs)))
		}
		return errors.Join(err, cleanupErr)
	}
	if err := repository.CompleteBuiltinCatalogSeed(ctx, builtinKimiK3SeedKey, time.Now().UTC()); err != nil {
		var planCleanup error
		if planCreated {
			planCleanup = service.DeletePlan(context.Background(), catalog.DeleteCommand{ID: plan.ID, ExpectedRevision: plan.Revision})
		}
		var suiteCleanup, caseCleanup error
		if suiteCreated {
			suiteCleanup = service.DeleteSuite(context.Background(), catalog.DeleteCommand{ID: suite.ID, ExpectedRevision: suite.Revision})
			caseCleanup = service.cleanupMaterializedCases(toDomainCaseRefs(refs))
		}
		return errors.Join(err, planCleanup, suiteCleanup, caseCleanup)
	}
	return nil
}

func builtinKimiK3PlanMatches(plan catalog.PlanSummary, suite catalog.MutationResult, refs []catalog.CaseRevisionInput) bool {
	return plan.Name == builtinKimiK3PlanName && plan.SuiteID == suite.ID && plan.SuiteRevision == suite.Revision &&
		len(plan.ModelIDs) == 0 && len(plan.ChannelIDs) == 0 && reflect.DeepEqual(plan.Cases, refs) &&
		plan.LoadMode == domain.LoadFixedConcurrency && plan.Concurrency == 1 && plan.RequestCount == uint64(len(refs)) &&
		plan.RatePerSecond == 0 && plan.DurationMS == 0 && plan.RequestTimeoutMS == 300_000 &&
		reflect.DeepEqual(plan.SLAThresholds, map[string]float64{"success_rate": 1})
}

func toDomainCaseRefs(refs []catalog.CaseRevisionInput) []domain.CaseRevisionRef {
	result := make([]domain.CaseRevisionRef, len(refs))
	for index, ref := range refs {
		result[index] = domain.CaseRevisionRef{CaseID: ref.CaseID, Revision: ref.Revision}
	}
	return result
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
