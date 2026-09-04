package main

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	casebundle "github.com/894x/llm-test-studio/cases"
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
	suitebundle "github.com/894x/llm-test-studio/suites"
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
			AppVersion: options.appVersion, RetireAuthoredCatalog: false,
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
		modelFiles, err := modelcatalog.New(filepath.Join(executableDirectory, "models.json"))
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem model catalog: %w", err)
		}
		channelFiles, err := channelcatalog.New(filepath.Join(executableDirectory, "channels.json"))
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create filesystem channel catalog: %w", err)
		}
		planFiles, err := plancatalog.New(filepath.Join(executableDirectory, "plans"))
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
			lockPath: filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog.lock"),
			models:   modelFiles, channels: channelFiles,
			cases: caseFiles, suites: suiteFiles, plans: planFiles,
		}
		// Keep the original filename so an existing schema-v1 checkpoint can be
		// discovered and upgraded; the JSON payload carries its own schema version.
		migrationMarker := filepath.Join(executableDirectory, ".llm-test-studio-authored-catalog-v1.json")
		legacyCredentialStore := options.credentialStore
		if isNilInterface(legacyCredentialStore) {
			legacyCredentialStore = credentials.NewOSStore()
		}
		credentialScope, err := credentialScopeForAuthoredCatalogRoot(executableDirectory)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("scope credential keyring to authored catalog: %w", err)
		}
		credentialStore, err := credentials.NewScopedStore(legacyCredentialStore, credentialScope)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create authored-catalog credential store: %w", err)
		}
		cleanupQueuePath, err := credentialCleanupRegistryPath(directory, executableDirectory)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("locate credential cleanup queue: %w", err)
		}
		cleanupQueue, err := credentials.NewFileCleanupQueue(cleanupQueuePath)
		if err != nil {
			return desktopDependencies{}, fmt.Errorf("create credential cleanup queue: %w", err)
		}
		repository, err := openProductionRepositoryAfterCatalogCutover(
			ctx, database, options.appVersion, catalogRepository, migrationMarker,
			legacyCredentialStore, credentialStore, cleanupQueue,
		)
		if err != nil {
			return desktopDependencies{}, err
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
			Repository:  runtimeRepository,
			Credentials: credentialStore,
			Executor: runs.MustExecutorRouter(caseTypes, map[domain.CaseType]runs.Executor{
				casetypes.TypeLegacyAPIAudit:     runs.NewLegacyAPIAuditExecutor(nil),
				casetypes.TypeRequestSingle:      runs.NewLoadExecutor(nil),
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
		catalogCommands := catalogCommandsWithPlanDocuments{CatalogCommands: catalogQuery, documents: catalogRepository}
		serializedCatalog := serializedCatalogService{
			gate: gate, query: catalogQuery, commands: catalogCommands, channels: channelService,
			caseTypes: caseTypes, plansAreFiles: true,
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
				ChannelConnections: quicktest.NewStoredChannelConnectionResolver(catalogRepository, credentialStore),
			}),
			close: func() error {
				return errors.Join(runService.Close(), repository.Close())
			},
		}, nil
	}
}

func openProductionRepositoryAfterCatalogCutover(
	ctx context.Context,
	database string,
	appVersion string,
	catalogRepository legacyAuthoredCatalogMigrationTarget,
	migrationMarker string,
	legacyCredentialStore credentials.Store,
	credentialStore credentials.Store,
	cleanupQueue credentials.CleanupQueue,
) (*sqlite.Repository, error) {
	if isNilInterface(legacyCredentialStore) || isNilInterface(credentialStore) || isNilInterface(cleanupQueue) {
		return nil, errors.New("credential retirement dependencies are unavailable")
	}
	version, err := sqlite.SchemaVersion(ctx, database)
	if err != nil {
		return nil, fmt.Errorf("inspect desktop database after catalog export migrations: %w", err)
	}
	switch version {
	case sqlite.CatalogExportSchemaVersion:
		legacyRepository, err := sqlite.OpenLegacyCatalogRepository(ctx, database, sqlite.RepositoryOptions{})
		if err != nil {
			return nil, fmt.Errorf("open legacy desktop catalog for file export: %w", err)
		}
		authoredCatalogDigest, snapshotErr := legacyRepository.BeginAuthoredCatalogExport(ctx)
		if snapshotErr != nil {
			closeErr := legacyRepository.Close()
			return nil, fmt.Errorf(
				"begin stable legacy authored catalog file export: %w",
				errors.Join(snapshotErr, closeErr),
			)
		}
		legacyCredentialRefs, credentialSnapshotErr := legacyRepository.ListCredentialRefHistory(ctx)
		if credentialSnapshotErr != nil {
			closeErr := legacyRepository.Close()
			return nil, fmt.Errorf(
				"snapshot legacy credential references before file export: %w",
				errors.Join(credentialSnapshotErr, closeErr),
			)
		}
		legacyClosed := false
		retirementErr := catalogRepository.WithAuthoredCatalogRetirementLock(ctx, func(lockedCtx context.Context) error {
			// Keep cooperating authored-file writers blocked across the complete
			// export, receipt write, receipt validation, and SQLite DROP. Repository
			// mutations reuse the lock token carried by lockedCtx.
			if err := migrateLegacyAuthoredCatalog(lockedCtx, legacyRepository, catalogRepository, migrationMarker); err != nil {
				return fmt.Errorf("migrate legacy authored catalog to files: %w", err)
			}
			// Recompute the marker's logical source fingerprint while the exact
			// BEGIN IMMEDIATE export snapshot is still held. The SQLite retirement
			// digest below is intentionally separate: it guards every retired table,
			// while this digest selects the matching source->target file receipt.
			stableSnapshot, err := loadLegacyAuthoredCatalogSnapshot(lockedCtx, legacyRepository)
			if err != nil {
				return fmt.Errorf("fingerprint stable legacy authored catalog after file export: %w", err)
			}
			migrationSourceDigest, err := digestLegacyAuthoredCatalogSnapshot(stableSnapshot)
			if err != nil {
				return fmt.Errorf("fingerprint stable legacy authored catalog after file export: %w", err)
			}

			// Release the source snapshot only after its target receipt exists and
			// while the target lock remains held. Any later SQLite write is caught
			// by migration 0011's digest recheck.
			legacyClosed = true
			if err := legacyRepository.Close(); err != nil {
				return fmt.Errorf("close stable legacy desktop catalog after file export: %w", err)
			}
			if err := validateCompletedLegacyAuthoredCatalogMigrationForSource(
				lockedCtx, catalogRepository, migrationMarker, migrationSourceDigest,
			); err != nil {
				return fmt.Errorf("validate legacy authored catalog file export receipt: %w", err)
			}
			if err := sqlite.Migrate(lockedCtx, database, sqlite.MigrateOptions{
				AppVersion:                    appVersion,
				RetireAuthoredCatalog:         true,
				ExpectedAuthoredCatalogDigest: authoredCatalogDigest,
				BeforeAuthoredCatalogRetirement: func(retirementCtx context.Context) error {
					// Consume the crash-window receipt before DROP. If retirement
					// later fails, v10 remains authoritative and the next startup
					// safely replays the idempotent export instead of reusing a
					// receipt across a different operational database lifetime.
					if err := consumeLegacyAuthoredCatalogMigrationMarker(retirementCtx, migrationMarker); err != nil {
						return err
					}
					return retireLegacyCredentialReferences(
						retirementCtx, legacyCredentialRefs, catalogRepository,
						legacyCredentialStore, credentialStore, cleanupQueue,
					)
				},
			}); err != nil {
				return fmt.Errorf("retire authored sqlite catalog: %w", err)
			}
			return nil
		})
		if !legacyClosed {
			closeErr := legacyRepository.Close()
			if closeErr != nil {
				closeErr = fmt.Errorf("close legacy desktop catalog after retirement lock failure: %w", closeErr)
			}
			retirementErr = errors.Join(retirementErr, closeErr)
		}
		if retirementErr != nil {
			return nil, retirementErr
		}
	case sqlite.AuthoredCatalogRetirementSchemaVersion:
		// A committed v11 migration already proves that the crash-window
		// receipt was validated. Current files may now be edited normally; only
		// structural validation applies, and stale pre-v3 receipts are consumed.
		if err := catalogRepository.WithAuthoredCatalogRetirementLock(ctx, func(lockedCtx context.Context) error {
			if err := validateMigratedFileCatalog(lockedCtx, catalogRepository); err != nil {
				return err
			}
			if err := migrateUnscopedReferencedCredentials(
				lockedCtx, catalogRepository, legacyCredentialStore, credentialStore, cleanupQueue,
			); err != nil {
				return fmt.Errorf("migrate earlier unscoped channel credentials: %w", err)
			}
			return consumeLegacyAuthoredCatalogMigrationMarker(lockedCtx, migrationMarker)
		}); err != nil {
			return nil, fmt.Errorf("validate retired authored catalog files: %w", err)
		}
	default:
		return nil, fmt.Errorf(
			"desktop database schema version %d is not ready for authored catalog cutover",
			version,
		)
	}

	repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		return nil, fmt.Errorf("open desktop repository: %w", err)
	}
	return repository, nil
}

func consumeLegacyAuthoredCatalogMigrationMarker(ctx context.Context, markerPath string) error {
	if ctx == nil || strings.TrimSpace(markerPath) == "" || !filepath.IsAbs(markerPath) {
		return errLegacyAuthoredCatalogMigrationInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(filepath.Clean(markerPath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("consume legacy authored catalog migration marker: %w", err)
	}
	return nil
}

func retireLegacyCredentialReferences(
	ctx context.Context,
	legacyRefs []domain.CredentialRef,
	target legacyAuthoredCatalogMigrationTarget,
	legacyStore credentials.Store,
	scopedStore credentials.Store,
	queue credentials.CleanupQueue,
) error {
	if isNilInterface(legacyStore) || isNilInterface(scopedStore) || isNilInterface(queue) {
		return errors.New("legacy credential retirement dependencies are unavailable")
	}
	channels, err := target.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list migrated channels before credential retirement: %w", err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list migrated plans before credential retirement: %w", err)
	}
	referenced := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if domain.IsUUID(channel.CredentialID) {
			referenced[channel.CredentialID] = struct{}{}
		}
	}
	for _, document := range documents {
		for _, binding := range document.TargetBindings {
			if domain.IsUUID(binding.Channel.CredentialID) {
				referenced[binding.Channel.CredentialID] = struct{}{}
			}
		}
	}
	registryIDs := make([]string, 0, len(referenced))
	for credentialID := range referenced {
		registryIDs = append(registryIDs, credentialID)
	}
	sort.Strings(registryIDs)
	type obsoleteCredential struct {
		credential domain.CredentialRef
		ref        credentials.StoreRef
	}
	retainedByStoreRef := make(map[string]obsoleteCredential, len(referenced))
	retainedIDs := make(map[string]struct{}, len(referenced))
	legacyRefsToDelete := make(map[string]credentials.StoreRef, len(legacyRefs))
	obsoleteChannelIDs := make(map[string]struct{})
	for _, credential := range legacyRefs {
		ref, err := credentials.StoreRefFromCredential(credential)
		if err != nil {
			return fmt.Errorf("validate legacy credential reference %s: %w", credential.ID, err)
		}
		_, retainedID := referenced[credential.ID]
		retained := retainedID && credential.Purpose == domain.CredentialChannelAPIKey
		if retained {
			expected, expectedErr := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credential.ID)
			retained = expectedErr == nil && ref == expected
		}
		if retained {
			retainedByStoreRef[ref.Value()] = obsoleteCredential{credential: credential, ref: ref}
			retainedIDs[credential.ID] = struct{}{}
		}
		// No authored root can prove that a legacy channel key is obsolete in
		// every other root created by an unscoped build. Preserve all v1 channel
		// entries as migration sources; non-channel history has no file consumer.
		if credential.Purpose != domain.CredentialChannelAPIKey {
			legacyRefsToDelete[ref.Value()] = ref
		}
		if !retained && credential.Purpose == domain.CredentialChannelAPIKey && !retainedID {
			obsoleteChannelIDs[credential.ID] = struct{}{}
		}
	}
	retainedStoreRefs := make([]string, 0, len(retainedByStoreRef))
	for storeRef := range retainedByStoreRef {
		retainedStoreRefs = append(retainedStoreRefs, storeRef)
	}
	sort.Strings(retainedStoreRefs)
	obsoleteIDs := make([]string, 0, len(obsoleteChannelIDs))
	for credentialID := range obsoleteChannelIDs {
		obsoleteIDs = append(obsoleteIDs, credentialID)
	}
	sort.Strings(obsoleteIDs)
	journalIDs := append(append([]string(nil), registryIDs...), obsoleteIDs...)
	if err := queue.Enqueue(ctx, journalIDs...); err != nil {
		return fmt.Errorf("record legacy credential retirement retry list: %w", err)
	}
	// Copy every still-referenced v1 credential before deleting any legacy
	// keyring entries. A retry observes an existing scoped entry and continues,
	// so crashes cannot require the source key to remain after it was copied.
	for _, storeRef := range retainedStoreRefs {
		retained := retainedByStoreRef[storeRef]
		if err := copyLegacyCredentialToScopedStore(ctx, legacyStore, scopedStore, retained.credential); err != nil {
			return fmt.Errorf("scope retained legacy credential %s: %w", retained.credential.ID, err)
		}
	}
	// Existing authored files can contain credentials not present in this
	// SQLite database (for example, a portable catalog beside a fresh DB).
	// Reconcile those canonical v1 entries without a legacy fingerprint.
	for _, credentialID := range registryIDs {
		ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialID)
		if err != nil {
			return fmt.Errorf("create referenced credential %s: %w", credentialID, err)
		}
		if _, hasLegacyMetadata := retainedIDs[credentialID]; hasLegacyMetadata {
			continue
		}
		if err := reconcileLegacyCredentialWithScopedStore(ctx, legacyStore, scopedStore, ref, ""); err != nil {
			return fmt.Errorf("scope file-referenced credential %s: %w", credentialID, err)
		}
	}
	// Global v1 channel entries are deliberately retained as a read-only
	// migration bridge. Older unscoped builds could have shared one key across
	// multiple authored roots, and no single root can prove that an unreferenced
	// key is also unused by every other root. New writes use only v2 entries.
	legacyStoreRefs := make([]string, 0, len(legacyRefsToDelete))
	for storeRef := range legacyRefsToDelete {
		legacyStoreRefs = append(legacyStoreRefs, storeRef)
	}
	sort.Strings(legacyStoreRefs)
	for _, storeRef := range legacyStoreRefs {
		ref := legacyRefsToDelete[storeRef]
		if err := legacyStore.Delete(ctx, ref); err != nil && !errors.Is(err, credentials.ErrNotFound) {
			return fmt.Errorf("delete legacy credential %s: %w", ref.ID(), err)
		}
	}
	if err := queue.Remove(ctx, obsoleteIDs...); err != nil {
		return fmt.Errorf("finish legacy credential retirement retry list: %w", err)
	}
	return nil
}

func copyLegacyCredentialToScopedStore(
	ctx context.Context,
	legacyStore credentials.Store,
	scopedStore credentials.Store,
	credential domain.CredentialRef,
) error {
	ref, err := credentials.StoreRefFromCredential(credential)
	if err != nil {
		return fmt.Errorf("validate legacy credential metadata: %w", err)
	}
	return reconcileLegacyCredentialWithScopedStore(
		ctx, legacyStore, scopedStore, ref, credential.Fingerprint,
	)
}

func migrateUnscopedReferencedCredentials(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	legacyStore credentials.Store,
	scopedStore credentials.Store,
	queue credentials.CleanupQueue,
) error {
	channels, err := target.ListChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels before unscoped credential migration: %w", err)
	}
	documents, err := target.ListPlanDocuments(ctx)
	if err != nil {
		return fmt.Errorf("list plans before unscoped credential migration: %w", err)
	}
	referenced := make(map[string]struct{}, len(channels))
	for _, channel := range channels {
		if domain.IsUUID(channel.CredentialID) {
			referenced[channel.CredentialID] = struct{}{}
		}
	}
	for _, document := range documents {
		for _, binding := range document.TargetBindings {
			if domain.IsUUID(binding.Channel.CredentialID) {
				referenced[binding.Channel.CredentialID] = struct{}{}
			}
		}
	}
	ids := make([]string, 0, len(referenced))
	for id := range referenced {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if err := queue.Enqueue(ctx, ids...); err != nil {
		return fmt.Errorf("record scoped credential registry: %w", err)
	}
	for _, id := range ids {
		ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, id)
		if err != nil {
			return fmt.Errorf("create unscoped credential reference %s: %w", id, err)
		}
		if err := reconcileLegacyCredentialWithScopedStore(ctx, legacyStore, scopedStore, ref, ""); err != nil {
			return fmt.Errorf("scope credential %s: %w", id, err)
		}
	}
	// Keep referenced v1 entries as a read-only migration bridge for other
	// authored roots created by earlier unscoped builds. New channel mutations
	// never write this namespace.
	return nil
}

func reconcileLegacyCredentialWithScopedStore(
	ctx context.Context,
	legacyStore credentials.Store,
	scopedStore credentials.Store,
	ref credentials.StoreRef,
	expectedFingerprint string,
) error {
	legacySecret, legacyFound, err := readCredentialSecret(ctx, legacyStore, ref)
	if err != nil {
		return fmt.Errorf("read legacy credential: %w", err)
	}
	defer clear(legacySecret)
	scopedSecret, scopedFound, err := readCredentialSecret(ctx, scopedStore, ref)
	if err != nil {
		return fmt.Errorf("read scoped credential: %w", err)
	}
	defer clear(scopedSecret)
	if expectedFingerprint != "" {
		if legacyFound && !credentialMatchesFingerprint(legacySecret, expectedFingerprint) {
			return errors.New("legacy credential does not match its stored fingerprint")
		}
		if scopedFound && !credentialMatchesFingerprint(scopedSecret, expectedFingerprint) {
			return errors.New("scoped credential does not match the legacy fingerprint")
		}
	}
	if legacyFound && scopedFound {
		if !credentialSecretsEqual(legacySecret, scopedSecret) {
			return errors.New("legacy and scoped credential values conflict")
		}
		return nil
	}
	if !legacyFound {
		// Both missing preserves the pre-cutover missing-key state. Scoped-only
		// is the expected state after a crash between v1 deletion and SQLite DROP.
		return nil
	}

	setErr := scopedStore.Set(ctx, ref, legacySecret)
	storedSecret, stored, readErr := readCredentialSecret(ctx, scopedStore, ref)
	defer clear(storedSecret)
	if readErr != nil {
		return errors.Join(fmt.Errorf("verify scoped credential write: %w", readErr), setErr)
	}
	if !stored || !credentialSecretsEqual(legacySecret, storedSecret) {
		if setErr == nil {
			setErr = errors.New("scoped credential write did not preserve the legacy value")
		}
		return fmt.Errorf("write scoped credential: %w", setErr)
	}
	// A platform adapter can commit the keyring write and still return an
	// uncertain error. Matching read-back proves this operation completed.
	return nil
}

func readCredentialSecret(
	ctx context.Context,
	store credentials.Store,
	ref credentials.StoreRef,
) ([]byte, bool, error) {
	lease, err := store.Get(ctx, ref)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	secret, bytesErr := lease.Bytes()
	closeErr := lease.Close()
	if bytesErr != nil || closeErr != nil {
		clear(secret)
		return nil, false, errors.Join(bytesErr, closeErr)
	}
	return secret, true, nil
}

func credentialSecretsEqual(left, right []byte) bool {
	leftDigest := sha256.Sum256(left)
	rightDigest := sha256.Sum256(right)
	return subtle.ConstantTimeCompare(leftDigest[:], rightDigest[:]) == 1
}

func credentialMatchesFingerprint(secret []byte, fingerprint string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(fingerprint, prefix) {
		return false
	}
	expected, err := hex.DecodeString(strings.TrimPrefix(fingerprint, prefix))
	if err != nil || len(expected) != sha256.Size {
		clear(expected)
		return false
	}
	actual := sha256.Sum256(secret)
	matched := subtle.ConstantTimeCompare(actual[:], expected) == 1
	clear(expected)
	return matched
}

func validateCompletedLegacyAuthoredCatalogMigrationForSource(
	ctx context.Context,
	target legacyAuthoredCatalogMigrationTarget,
	markerPath string,
	sourceDigest string,
) error {
	marker, complete, err := legacyAuthoredCatalogMigrationComplete(ctx, markerPath)
	if err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("%w: completion marker is missing", errLegacyAuthoredCatalogMigrationMarker)
	}
	expectedTargetDigest, exists := marker.TargetDigestsBySource[sourceDigest]
	if !exists {
		return fmt.Errorf("%w: completion marker has no target receipt for source %s", errLegacyAuthoredCatalogMigrationMarker, sourceDigest)
	}
	actualTargetDigest, err := digestMigratedFileCatalog(ctx, target)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("%w: fingerprint completed target manifest: %v", errLegacyAuthoredCatalogMigrationConflict, err)
	}
	if actualTargetDigest != expectedTargetDigest {
		return fmt.Errorf(
			"%w: completed target manifest changed (got %s, want %s)",
			errLegacyAuthoredCatalogMigrationConflict,
			actualTargetDigest,
			expectedTargetDigest,
		)
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
	scope, err := credentialScopeForAuthoredCatalogRoot(authoredCatalogRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(configurationDirectory, "credential-keyring-registries", scope+".json"), nil
}

func credentialScopeForAuthoredCatalogRoot(authoredCatalogRoot string) (string, error) {
	authoredCatalogRoot = filepath.Clean(strings.TrimSpace(authoredCatalogRoot))
	if authoredCatalogRoot == "." || !filepath.IsAbs(authoredCatalogRoot) {
		return "", errors.New("authored catalog root must be an absolute path")
	}
	canonicalRoot := authoredCatalogRoot
	if resolved, err := filepath.EvalSymlinks(authoredCatalogRoot); err == nil {
		canonicalRoot = filepath.Clean(resolved)
	}
	if runtime.GOOS == "windows" {
		canonicalRoot = strings.ToLower(canonicalRoot)
	}
	digest := sha256.Sum256([]byte(filepath.ToSlash(canonicalRoot)))
	return hex.EncodeToString(digest[:]), nil
}
