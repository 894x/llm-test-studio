package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/modelcatalog"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/suitecatalog"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestFilesystemCatalogRepositoryAllowsMappingDeletionForTargetlessPlan(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	channel, mapping, plan := filesystemCatalogMappingFixtures(true)
	if err := channels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if err := plans.Create(ctx, plan); err != nil {
		t.Fatal(err)
	}

	repository := filesystemCatalogRepository{lockPath: filepath.Join(root, "catalog.lock"), channels: channels, plans: plans}
	if err := repository.DeleteChannelModel(ctx, mapping.ID, mapping.Revision); err != nil {
		t.Fatalf("DeleteChannelModel() error = %v", err)
	}
	if _, err := channels.GetMapping(ctx, mapping.ID); !errors.Is(err, channelcatalog.ErrNotFound) {
		t.Fatalf("GetMapping(deleted) error = %v, want %v", err, channelcatalog.ErrNotFound)
	}
}

func TestFilesystemCatalogRepositoryDefersPlanTargetValidation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	models, err := modelcatalog.New(filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), models: models, channels: channels,
		cases: cases, suites: suites, plans: plans,
	}
	channel, mapping, plan := filesystemCatalogMappingFixtures(false)
	if err := repository.CreateModel(ctx, filesystemCatalogModelFixture(mapping.ModelID, mapping.Protocols[0])); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateChannelModel(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	testCase := filesystemCatalogTestCase(1)
	if err := repository.CreateTestCase(ctx, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("case Entries() = %#v, %v", caseEntries, err)
	}
	testCase = caseEntries[0].TestCase
	suite := filesystemCatalogSuiteFixture(testCase)
	if err := repository.CreateSuite(ctx, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("suite Entries() = %#v, %v", suiteEntries, err)
	}
	suite = suiteEntries[0].Suite
	filesystemCatalogSetPlanSuites(&plan, suite)
	if err := repository.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("CreatePlan() must save references: %v", err)
	}
}

func TestFilesystemCatalogRepositoryRefusesToDeleteChannelWithMapping(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channelPath := filepath.Join(root, "channels.json")
	channels, err := channelcatalog.New(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	channel, mapping, _ := filesystemCatalogMappingFixtures(false)
	if err := channels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), channels: channels, plans: plans,
	}
	if err := repository.DeleteChannel(ctx, channel.ID, channel.Revision); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("DeleteChannel() error = %v, want %v", err, catalog.ErrConflict)
	}
	after, err := os.ReadFile(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("channels.json changed after a rejected channel deletion")
	}
}

func TestFilesystemCatalogRepositoryFindsCurrentChannelCredentialReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	channel, _, _ := filesystemCatalogMappingFixtures(false)
	channel.CredentialID = "62000000-0000-4000-8000-000000000010"
	if err := channels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{channels: channels, plans: plans}

	referenced, err := repository.IsCredentialReferenced(ctx, channel.CredentialID)
	if err != nil {
		t.Fatalf("IsCredentialReferenced() error = %v", err)
	}
	if !referenced {
		t.Fatal("IsCredentialReferenced() = false, want true for current channel")
	}
}

func TestFilesystemCredentialMutationLockPreventsStartupRetryFromDeletingAProvisionalKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), channels: channels,
	}
	baseStore := credentials.NewMemoryStore()
	store := &blockingAfterSetCredentialStore{
		Store: baseStore, setDone: make(chan struct{}), release: make(chan struct{}),
	}
	queuePath := filepath.Join(root, "credential-registry.json")
	queueA, err := credentials.NewFileCleanupQueue(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	queueB, err := credentials.NewFileCleanupQueue(queuePath)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"62100000-0000-4000-8000-000000000001",
		"62100000-0000-4000-8000-000000000002",
	}
	creator, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, CleanupQueue: queueA, Clock: productionClock{},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{
				ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1,
				CreatedAt: at, UpdatedAt: at,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	replayer, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, CleanupQueue: queueB, Clock: productionClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	createDone := make(chan error, 1)
	go func() {
		_, err := creator.Create(ctx, channelconfig.CreateCommand{
			Name: "concurrent", BaseURL: "https://api.example.test/v1", APIKey: "plain-provisional-secret",
			Enabled: true,
		})
		createDone <- err
	}()
	select {
	case <-store.setDone:
	case <-time.After(5 * time.Second):
		t.Fatal("Create did not reach the provisional keyring write")
	}
	retryDone := make(chan error, 1)
	go func() { retryDone <- replayer.RetryPendingCredentialCleanup(ctx) }()
	select {
	case err := <-retryDone:
		t.Fatalf("startup retry escaped the credential mutation lock before Channel commit: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(store.release)
	if err := <-createDone; err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := <-retryDone; err != nil {
		t.Fatalf("RetryPendingCredentialCleanup() error = %v", err)
	}
	credentialRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, "62100000-0000-4000-8000-000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if err := baseStore.Test(ctx, credentialRef); err != nil {
		t.Fatalf("committed Channel credential was deleted by concurrent startup retry: %v", err)
	}
	if ids, err := queueB.List(ctx); err != nil || len(ids) != 1 || ids[0] != credentialRef.ID() {
		t.Fatalf("credential registry = %#v, %v; want active credential retained", ids, err)
	}
}

func TestFilesystemCredentialMutationLockDoesNotSwallowCancellationBeforeAction(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{lockPath: filepath.Join(root, "catalog.lock"), channels: channels}
	store := credentials.NewMemoryStore()
	queue := credentials.NewMemoryCleanupQueue()
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, CleanupQueue: queue, Clock: productionClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Create(ctx, channelconfig.CreateCommand{
		Name: "cancelled", BaseURL: "https://api.example.test/v1", APIKey: "plain-cancelled-secret",
		Enabled: true,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create(cancelled) = %#v, %v; want context cancellation", result, err)
	}
	if result != (channelconfig.MutationResult{}) {
		t.Fatalf("Create(cancelled) result = %#v, want zero", result)
	}
	if ids, err := queue.List(context.Background()); err != nil || len(ids) != 0 {
		t.Fatalf("credential registry after cancelled Create = %#v, %v; want empty", ids, err)
	}
}

func TestFilesystemCredentialRegistryCleansKeyAfterDirectChannelFileEdit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{lockPath: filepath.Join(root, "catalog.lock"), channels: channels, plans: plans}
	store := credentials.NewMemoryStore()
	queue, err := credentials.NewFileCleanupQueue(filepath.Join(root, "credential-registry.json"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{
		"62200000-0000-4000-8000-000000000001",
		"62200000-0000-4000-8000-000000000002",
	}
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, CleanupQueue: queue, Clock: productionClock{},
		MetaFactory: func(at time.Time) (domain.EntityMeta, error) {
			id := ids[0]
			ids = ids[1:]
			return domain.EntityMeta{
				ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1,
				CreatedAt: at, UpdatedAt: at,
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, channelconfig.CreateCommand{
		Name: "direct-edit", BaseURL: "https://api.example.test/v1", APIKey: "plain-direct-edit-secret",
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := channels.DeleteChannel(ctx, created.ChannelID, created.ChannelRevision); err != nil {
		t.Fatalf("direct Channel file edit: %v", err)
	}
	if err := service.RetryPendingCredentialCleanup(ctx); err != nil {
		t.Fatalf("RetryPendingCredentialCleanup() error = %v", err)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, created.CredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Test(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("credential after direct Channel deletion = %v, want not found", err)
	}
	if registry, err := queue.List(ctx); err != nil || len(registry) != 0 {
		t.Fatalf("credential registry after direct deletion = %#v, %v; want empty", registry, err)
	}
}

type blockingAfterSetCredentialStore struct {
	credentials.Store
	setDone chan struct{}
	release chan struct{}
	once    sync.Once
}

func (store *blockingAfterSetCredentialStore) Set(ctx context.Context, ref credentials.StoreRef, secret []byte) error {
	if err := store.Store.Set(ctx, ref, secret); err != nil {
		return err
	}
	store.once.Do(func() { close(store.setDone) })
	select {
	case <-store.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestFilesystemCatalogRepositoryPlansDoNotRetainHistoricalCredentials(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	channel, mapping, plan := filesystemCatalogMappingFixtures(false)
	oldCredentialID := "62000000-0000-4000-8000-000000000010"
	channel.CredentialID = oldCredentialID
	if err := channels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}
	if err := channels.CreateMapping(ctx, mapping); err != nil {
		t.Fatal(err)
	}
	if err := plans.CreateDocument(ctx, plancatalog.Document{
		FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
		Plan:              plan,
	}); err != nil {
		t.Fatal(err)
	}
	updatedChannel := channel
	updatedChannel.Revision++
	updatedChannel.UpdatedAt = updatedChannel.UpdatedAt.Add(time.Minute)
	updatedChannel.CredentialID = "62000000-0000-4000-8000-000000000011"
	if err := channels.UpdateChannel(ctx, channel.Revision, updatedChannel); err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{channels: channels, plans: plans}

	referenced, err := repository.IsCredentialReferenced(ctx, oldCredentialID)
	if err != nil {
		t.Fatalf("IsCredentialReferenced() error = %v", err)
	}
	if referenced {
		t.Fatal("Plan reference must not retain a historical credential")
	}
}

func TestFilesystemCatalogRepositoryReportsUnreferencedCredential(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{channels: channels, plans: plans}

	referenced, err := repository.IsCredentialReferenced(ctx, "62000000-0000-4000-8000-000000000012")
	if err != nil {
		t.Fatalf("IsCredentialReferenced() error = %v", err)
	}
	if referenced {
		t.Fatal("IsCredentialReferenced() = true, want false")
	}
}

func TestFilesystemCatalogRepositoryPlanReferencesRemainUnchangedAfterExternalEdits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot := filepath.Join(root, "cases")
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	directCandidate := filesystemCatalogTestCase(1)
	directCandidate.Key = "direct-plan-case"
	directCandidate.Name = "direct Plan Case"
	suiteCandidate := filesystemCatalogTestCase(1)
	suiteCandidate.ID = "63000000-0000-4000-8000-000000000021"
	suiteCandidate.Key = "suite-plan-case"
	suiteCandidate.Name = "Suite Plan Case"
	for _, candidate := range []domain.TestCase{directCandidate, suiteCandidate} {
		if err := cases.SaveCase(ctx, "openai-chat", candidate.Key, candidate); err != nil {
			t.Fatal(err)
		}
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 2 {
		t.Fatalf("Case Entries() = %#v, %v", caseEntries, err)
	}
	casesByKey := make(map[string]casecatalog.Entry, len(caseEntries))
	for _, entry := range caseEntries {
		casesByKey[entry.TestCase.Key] = entry
	}
	directCase := casesByKey[directCandidate.Key]
	suiteCase := casesByKey[suiteCandidate.Key]

	suiteRoot := filepath.Join(root, "suites")
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: suiteRoot})
	if err != nil {
		t.Fatal(err)
	}
	directSuite := filesystemCatalogSuiteFixture(directCase.TestCase)
	directSuite.ID = "64000000-0000-4000-8000-000000000002"
	directSuite.Key = "direct-plan-suite"
	directSuite.Name = "direct Plan Suite"
	suite := filesystemCatalogSuiteFixture(suiteCase.TestCase)
	for _, candidate := range []domain.Suite{directSuite, suite} {
		if err := suites.SaveSuite(ctx, "openai-chat", candidate.Key, candidate); err != nil {
			t.Fatal(err)
		}
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 2 {
		t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
	}
	var directPinnedSuite, pinnedSuite suitecatalog.Entry
	for _, entry := range suiteEntries {
		switch entry.Suite.Key {
		case directSuite.Key:
			directPinnedSuite = entry
		case suite.Key:
			pinnedSuite = entry
		}
	}
	if directPinnedSuite.Suite.ID == "" || pinnedSuite.Suite.ID == "" {
		t.Fatal("saved Suite entry is missing")
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, plan := filesystemCatalogMappingFixtures(true)
	filesystemCatalogSetPlanSuites(&plan, directPinnedSuite.Suite, pinnedSuite.Suite)
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: cases, suites: suites, plans: plans,
	}
	if err := repository.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}

	// Bypass the repository to model direct edits to the shareable files. The
	// Plan stores references without copying the referenced contents.
	for _, pinned := range []casecatalog.Entry{directCase, suiteCase} {
		edited := pinned.TestCase
		edited.Name += " externally edited"
		raw, err := casecodec.EncodeFilesystemCase(edited)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(caseRoot, pinned.Group, pinned.Directory, "case.json")
		if err := os.WriteFile(target, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	suiteTarget := filepath.Join(suiteRoot, pinnedSuite.Group, pinnedSuite.Directory, "suite.json")
	suiteRaw, err := os.ReadFile(suiteTarget)
	if err != nil {
		t.Fatal(err)
	}
	editedSuiteRaw := bytes.Replace(suiteRaw, []byte(`"name": "file suite"`), []byte(`"name": "externally edited suite"`), 1)
	if bytes.Equal(editedSuiteRaw, suiteRaw) {
		t.Fatalf("suite fixture did not contain the expected name: %s", suiteRaw)
	}
	if err := os.WriteFile(suiteTarget, editedSuiteRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	stored, err := plans.Get(ctx, plan.ID)
	if err != nil || !reflect.DeepEqual(stored, plan) {
		t.Fatalf("external edits changed saved Plan references: %v", err)
	}
}

func TestFilesystemCatalogRepositoryPlanSaveDoesNotMaterializeCaseRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, mutation := range []string{"create", "update"} {
		mutation := mutation
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
			if err != nil {
				t.Fatal(err)
			}
			candidate := filesystemCatalogTestCase(1)
			if err := cases.SaveCase(ctx, "openai-chat", candidate.Key, candidate); err != nil {
				t.Fatal(err)
			}
			entries, err := cases.Entries(ctx)
			if err != nil || len(entries) != 1 {
				t.Fatalf("Entries() = %#v, %v", entries, err)
			}
			suites, err := suitecatalog.New(suitecatalog.Options{
				Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites"),
			})
			if err != nil {
				t.Fatal(err)
			}
			suite := filesystemCatalogSuiteFixture(entries[0].TestCase)
			if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
				t.Fatal(err)
			}
			suiteEntries, err := suites.Entries(ctx)
			if err != nil || len(suiteEntries) != 1 {
				t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
			}
			suite = suiteEntries[0].Suite
			plans, err := plancatalog.New(filepath.Join(root, "plans"))
			if err != nil {
				t.Fatal(err)
			}
			_, _, plan := filesystemCatalogMappingFixtures(true)
			filesystemCatalogSetPlanSuites(&plan, suite)
			if mutation == "update" {
				if err := plans.Create(ctx, plan); err != nil {
					t.Fatal(err)
				}
				plan.Name = "updated Plan must not commit"
				plan.Revision++
				plan.UpdatedAt = plan.UpdatedAt.Add(time.Minute)
			}
			want := fs.ErrPermission
			repository := filesystemCatalogRepository{
				lockPath: filepath.Join(root, "catalog.lock"),
				cases:    &failingReadCaseCatalog{Service: cases, err: want},
				suites:   suites,
				plans:    plans,
			}
			var mutationErr error
			if mutation == "create" {
				mutationErr = repository.CreatePlan(ctx, plan)
			} else {
				mutationErr = repository.UpdatePlan(ctx, plan.Revision-1, plan)
			}
			if mutationErr != nil {
				t.Fatalf("Plan save tried to archive Cases: %v", mutationErr)
			}
			stored, err := plans.Get(ctx, plan.ID)
			if err != nil || !reflect.DeepEqual(stored, plan) {
				t.Fatalf("reference plan not saved: %v", err)
			}
		})
	}
}

func TestFilesystemCatalogRepositoryPlanSaveDoesNotMaterializeSuiteRevisions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	candidate := filesystemCatalogTestCase(1)
	if err := cases.SaveCase(ctx, "openai-chat", candidate.Key, candidate); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Case Entries() = %#v, %v", caseEntries, err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	suite := filesystemCatalogSuiteFixture(caseEntries[0].TestCase)
	if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, plan := filesystemCatalogMappingFixtures(true)
	filesystemCatalogSetPlanSuites(&plan, suiteEntries[0].Suite)
	want := fs.ErrPermission
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: cases,
		suites: &failingReadSuiteCatalog{Service: suites, err: want}, plans: plans,
	}
	if err := repository.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("Plan save tried to archive Suite: %v", err)
	}
	if _, err := plans.Get(ctx, plan.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemCatalogRepositoryCaseUpdateLeavesSuiteDocumentUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	candidate := filesystemCatalogTestCase(1)
	if err := cases.SaveCase(ctx, "openai-chat", candidate.Key, candidate); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Case Entries() = %#v, %v", caseEntries, err)
	}
	pinnedCase := caseEntries[0].TestCase
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	suite := filesystemCatalogSuiteFixture(pinnedCase)
	if err := suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
	}
	pinnedSuite := suiteEntries[0].Suite
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: cases, suites: suites,
	}
	updated := pinnedCase
	updated.Revision++
	updated.UpdatedAt = updated.UpdatedAt.Add(time.Minute)
	updated.Name = "updated Case does not change Suite"
	if err := repository.UpdateTestCase(ctx, pinnedCase.Revision, updated); err != nil {
		t.Fatalf("UpdateTestCase() error = %v", err)
	}
	currentSuite, err := suites.Find(ctx, pinnedSuite.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(currentSuite.Suite, pinnedSuite) {
		t.Fatal("Case update changed Suite references or revision")
	}
}

func TestFilesystemCatalogRepositoryAllowsCaseDeletionWithoutChangingReferences(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	firstCandidate := filesystemCatalogTestCase(1)
	firstCandidate.Key = "historical-suite-case"
	firstCandidate.Name = "historical Suite Case"
	secondCandidate := filesystemCatalogTestCase(1)
	secondCandidate.ID = "63000000-0000-4000-8000-000000000011"
	secondCandidate.Key = "current-suite-case"
	secondCandidate.Name = "current Suite Case"
	for _, candidate := range []domain.TestCase{firstCandidate, secondCandidate} {
		if err := cases.SaveCase(ctx, "openai-chat", candidate.Key, candidate); err != nil {
			t.Fatal(err)
		}
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 2 {
		t.Fatalf("Case Entries() = %#v, %v", caseEntries, err)
	}
	byKey := map[string]domain.TestCase{}
	for _, entry := range caseEntries {
		byKey[entry.TestCase.Key] = entry.TestCase
	}
	first := byKey[firstCandidate.Key]
	second := byKey[secondCandidate.Key]

	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	suiteCandidate := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "63000000-0000-4000-8000-000000000012", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 1, CreatedAt: first.CreatedAt, UpdatedAt: first.UpdatedAt,
		},
		Key: "historical-suite", Name: "historical Suite", Protocol: domain.ProtocolOpenAIChat,
		Cases: []domain.CaseRef{{CaseID: first.ID}}, Inputs: []domain.SuiteInput{},
	}
	if err := suites.SaveSuite(ctx, string(suiteCandidate.Protocol), suiteCandidate.Key, suiteCandidate); err != nil {
		t.Fatal(err)
	}
	initialSuite, err := suites.Find(ctx, stableSuiteIDForTest(t, suites))
	if err != nil {
		t.Fatal(err)
	}
	updatedSuite := initialSuite.Suite
	updatedSuite.Name = "current Suite"
	updatedSuite.Cases = []domain.CaseRef{{CaseID: second.ID}}
	if err := suites.SaveSuite(ctx, initialSuite.Group, initialSuite.Directory, updatedSuite); err != nil {
		t.Fatal(err)
	}

	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	plan := domain.Plan{
		EntityMeta: domain.EntityMeta{
			ID: "63000000-0000-4000-8000-000000000013", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 1, CreatedAt: first.CreatedAt, UpdatedAt: first.UpdatedAt,
		},
		Name: "historical Suite Plan",
	}
	filesystemCatalogSetPlanSuites(&plan, initialSuite.Suite)
	if err := plans.Create(ctx, plan); err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: cases, suites: suites, plans: plans,
	}
	if err := repository.DeleteTestCase(ctx, first.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	stored, err := plans.Get(ctx, plan.ID)
	if err != nil || !reflect.DeepEqual(stored, plan) {
		t.Fatalf("deletion changed Plan: %v", err)
	}
}

func stableSuiteIDForTest(t *testing.T, suites *suitecatalog.Service) string {
	t.Helper()
	entries, err := suites.Entries(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("Suite Entries() = %#v, %v", entries, err)
	}
	return entries[0].Suite.ID
}

func TestMapFileCatalogErrorDeclaresOnlyARedactedDiagnosticCause(t *testing.T) {
	t.Parallel()
	raw := fmt.Errorf("write channels.json: %w: api_key=sk-sensitive", fs.ErrPermission)
	err := mapFileCatalogError(raw)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("mapFileCatalogError() error = %v, want wrapped %v", err, fs.ErrPermission)
	}
	var diagnostic catalog.SafeDiagnosticCause
	if !errors.As(err, &diagnostic) {
		t.Fatalf("mapFileCatalogError() error type = %T, want catalog.SafeDiagnosticCause", err)
	}
	detail := diagnostic.SafeDiagnosticCause()
	if !strings.Contains(detail, "write channels.json") || strings.Contains(detail, "sk-sensitive") {
		t.Fatalf("safe diagnostic cause = %q, want operation without credential", detail)
	}
	if strings.Contains(err.Error(), "sk-sensitive") {
		t.Fatalf("mapped error leaked credential: %v", err)
	}
}

func TestFilesystemCatalogRepositoryRereadsDesiredStateAfterAmbiguousCommittedWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	models, err := modelcatalog.New(filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	cases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "cases")})
	if err != nil {
		t.Fatal(err)
	}
	suites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: filepath.Join(root, "suites")})
	if err != nil {
		t.Fatal(err)
	}
	plans, err := plancatalog.New(filepath.Join(root, "plans"))
	if err != nil {
		t.Fatal(err)
	}
	repository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"),
		models:   &committedErrorModelCatalog{Service: models},
		channels: &committedErrorChannelCatalog{Service: channels},
		cases:    &committedErrorCaseCatalog{Service: cases},
		suites:   &committedErrorSuiteCatalog{Service: suites},
		plans:    &committedErrorPlanCatalog{Service: plans},
	}

	channel, mapping, plan := filesystemCatalogMappingFixtures(false)
	model := filesystemCatalogModelFixture(mapping.ModelID, mapping.Protocols[0])
	if err := repository.CreateModel(ctx, model); err != nil {
		t.Fatalf("CreateModel() reported an already committed write as failed: %v", err)
	}
	if err := repository.CreateChannel(ctx, channel); err != nil {
		t.Fatalf("CreateChannel() reported an already committed write as failed: %v", err)
	}
	if err := repository.CreateChannelModel(ctx, mapping); err != nil {
		t.Fatalf("CreateChannelModel() reported an already committed write as failed: %v", err)
	}
	testCase := filesystemCatalogTestCase(1)
	if err := repository.CreateTestCase(ctx, testCase); err != nil {
		t.Fatalf("CreateTestCase() reported an already committed write as failed: %v", err)
	}
	caseEntries, err := cases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Entries() = %#v, %v", caseEntries, err)
	}
	suite := filesystemCatalogSuiteFixture(caseEntries[0].TestCase)
	if err := repository.CreateSuite(ctx, suite); err != nil {
		t.Fatalf("CreateSuite() reported an already committed write as failed: %v", err)
	}
	suiteEntries, err := suites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
	}
	filesystemCatalogSetPlanSuites(&plan, suiteEntries[0].Suite)
	if err := repository.CreatePlan(ctx, plan); err != nil {
		t.Fatalf("CreatePlan() reported an already committed write as failed: %v", err)
	}
	if err := repository.DeletePlan(ctx, plan.ID, plan.Revision); err != nil {
		t.Fatalf("DeletePlan() reported an already committed delete as failed: %v", err)
	}
}

func TestFilesystemCatalogRepositorySerializesSameRevisionCaseUpdatesAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot := filepath.Join(root, "cases")
	firstCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	initial := filesystemCatalogTestCase(1)
	if err := firstCases.SaveCase(ctx, "openai-chat", initial.Key, initial); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := firstCases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Entries() = %#v, %v", caseEntries, err)
	}
	entry := caseEntries[0]
	expectedRevision := entry.TestCase.Revision
	firstUpdate := entry.TestCase
	firstUpdate.Revision = expectedRevision + 1
	firstUpdate.UpdatedAt = firstUpdate.UpdatedAt.Add(time.Minute)
	firstUpdate.Name = "first concurrent update"
	secondUpdate := firstUpdate
	secondUpdate.Name = "second concurrent update"

	entered, release := make(chan struct{}), make(chan struct{})
	firstRepository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"),
		cases:    &blockingCaseCatalog{Service: firstCases, entered: entered, release: release},
	}
	secondRepository := filesystemCatalogRepository{lockPath: filepath.Join(root, "catalog.lock"), cases: secondCases}
	firstResult := make(chan error, 1)
	go func() { firstResult <- firstRepository.UpdateTestCase(ctx, expectedRevision, firstUpdate) }()
	<-entered
	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- secondRepository.UpdateTestCase(ctx, expectedRevision, secondUpdate)
	}()
	<-secondStarted
	close(release)

	if err := <-firstResult; err != nil {
		t.Fatalf("first UpdateTestCase() error = %v", err)
	}
	if err := <-secondResult; !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("second UpdateTestCase() error = %v, want %v", err, catalog.ErrConflict)
	}
}

func TestFilesystemCatalogRepositorySerializesSameRevisionSuiteUpdatesAcrossInstances(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot := filepath.Join(root, "cases")
	firstCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	testCase := filesystemCatalogTestCase(1)
	if err := firstCases.SaveCase(ctx, "openai-chat", testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := firstCases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Entries() = %#v, %v", caseEntries, err)
	}
	caseEntry := caseEntries[0]
	suiteRoot := filepath.Join(root, "suites")
	firstSuites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: suiteRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondSuites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: suiteRoot})
	if err != nil {
		t.Fatal(err)
	}
	initial := filesystemCatalogSuiteFixture(caseEntry.TestCase)
	if err := firstSuites.SaveSuite(ctx, "openai-chat", initial.Key, initial); err != nil {
		t.Fatal(err)
	}
	entries, err := firstSuites.Entries(ctx)
	if err != nil || len(entries) != 1 {
		t.Fatalf("Entries() = %#v, %v", entries, err)
	}
	current := entries[0].Suite
	firstUpdate := current
	firstUpdate.Revision = current.Revision + 1
	firstUpdate.UpdatedAt = firstUpdate.UpdatedAt.Add(time.Minute)
	firstUpdate.Name = "first concurrent suite update"
	secondUpdate := firstUpdate
	secondUpdate.Name = "second concurrent suite update"

	entered, release := make(chan struct{}), make(chan struct{})
	firstRepository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: firstCases,
		suites: &blockingSuiteCatalog{Service: firstSuites, entered: entered, release: release},
	}
	secondRepository := filesystemCatalogRepository{
		lockPath: filepath.Join(root, "catalog.lock"), cases: secondCases, suites: secondSuites,
	}
	firstResult := make(chan error, 1)
	go func() { firstResult <- firstRepository.UpdateSuite(ctx, current.Revision, firstUpdate) }()
	<-entered
	secondResult := make(chan error, 1)
	go func() { secondResult <- secondRepository.UpdateSuite(ctx, current.Revision, secondUpdate) }()
	close(release)

	if err := <-firstResult; err != nil {
		t.Fatalf("first UpdateSuite() error = %v", err)
	}
	if err := <-secondResult; !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("second UpdateSuite() error = %v, want %v", err, catalog.ErrConflict)
	}
}

func TestFilesystemCatalogRepositorySerializesModelDeleteAgainstMappingCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	modelPath, channelPath, planRoot := filepath.Join(root, "models.json"), filepath.Join(root, "channels.json"), filepath.Join(root, "plans")
	firstModels, err := modelcatalog.New(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	secondModels, err := modelcatalog.New(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	firstChannels, err := channelcatalog.New(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	secondChannels, err := channelcatalog.New(channelPath)
	if err != nil {
		t.Fatal(err)
	}
	secondPlans, err := plancatalog.New(planRoot)
	if err != nil {
		t.Fatal(err)
	}
	channel, mapping, _ := filesystemCatalogMappingFixtures(false)
	model := filesystemCatalogModelFixture(mapping.ModelID, mapping.Protocols[0])
	if err := firstModels.Create(ctx, model); err != nil {
		t.Fatal(err)
	}
	if err := firstChannels.CreateChannel(ctx, channel); err != nil {
		t.Fatal(err)
	}

	entered, release := make(chan struct{}), make(chan struct{})
	lockPath := filepath.Join(root, "catalog.lock")
	createRepository := filesystemCatalogRepository{
		lockPath: lockPath, models: firstModels,
		channels: &blockingChannelCatalog{Service: firstChannels, entered: entered, release: release},
	}
	deleteRepository := filesystemCatalogRepository{
		lockPath: lockPath, models: secondModels, channels: secondChannels, plans: secondPlans,
	}
	createResult := make(chan error, 1)
	go func() { createResult <- createRepository.CreateChannelModel(ctx, mapping) }()
	<-entered
	deleteResult := make(chan error, 1)
	go func() { deleteResult <- deleteRepository.DeleteModel(ctx, model.ID, model.Revision) }()
	close(release)

	if err := <-createResult; err != nil {
		t.Fatalf("CreateChannelModel() error = %v", err)
	}
	if err := <-deleteResult; !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("DeleteModel() error = %v, want %v", err, catalog.ErrConflict)
	}
	if _, err := secondModels.Get(ctx, model.ID); err != nil {
		t.Fatalf("Get(model) error = %v; model was orphaned", err)
	}
	if _, err := secondChannels.GetMapping(ctx, mapping.ID); err != nil {
		t.Fatalf("GetMapping() error = %v", err)
	}
}

func TestFilesystemCatalogRepositorySerializesCaseDeleteAgainstPlanCreate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	caseRoot, suiteRoot, planRoot := filepath.Join(root, "cases"), filepath.Join(root, "suites"), filepath.Join(root, "plans")
	firstCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondCases, err := casecatalog.New(casecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: caseRoot})
	if err != nil {
		t.Fatal(err)
	}
	testCase := filesystemCatalogTestCase(1)
	if err := firstCases.SaveCase(ctx, "openai-chat", testCase.Key, testCase); err != nil {
		t.Fatal(err)
	}
	caseEntries, err := firstCases.Entries(ctx)
	if err != nil || len(caseEntries) != 1 {
		t.Fatalf("Entries() = %#v, %v", caseEntries, err)
	}
	caseEntry := caseEntries[0]
	firstSuites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: suiteRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondSuites, err := suitecatalog.New(suitecatalog.Options{Builtin: fstest.MapFS{}, UserRoot: suiteRoot})
	if err != nil {
		t.Fatal(err)
	}
	firstPlans, err := plancatalog.New(planRoot)
	if err != nil {
		t.Fatal(err)
	}
	secondPlans, err := plancatalog.New(planRoot)
	if err != nil {
		t.Fatal(err)
	}
	suite := filesystemCatalogSuiteFixture(caseEntry.TestCase)
	if err := firstSuites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suiteEntries, err := firstSuites.Entries(ctx)
	if err != nil || len(suiteEntries) != 1 {
		t.Fatalf("Suite Entries() = %#v, %v", suiteEntries, err)
	}
	suite = suiteEntries[0].Suite
	_, _, plan := filesystemCatalogMappingFixtures(true)
	filesystemCatalogSetPlanSuites(&plan, suite)

	entered, release := make(chan struct{}), make(chan struct{})
	lockPath := filepath.Join(root, "catalog.lock")
	createRepository := filesystemCatalogRepository{
		lockPath: lockPath, cases: firstCases, suites: firstSuites,
		plans: &blockingPlanCatalog{Service: firstPlans, entered: entered, release: release},
	}
	deleteRepository := filesystemCatalogRepository{
		lockPath: lockPath, cases: secondCases, suites: secondSuites, plans: secondPlans,
	}
	createResult := make(chan error, 1)
	go func() { createResult <- createRepository.CreatePlan(ctx, plan) }()
	<-entered
	deleteResult := make(chan error, 1)
	go func() {
		deleteResult <- deleteRepository.DeleteTestCase(ctx, caseEntry.TestCase.ID, caseEntry.TestCase.Revision)
	}()
	close(release)

	if err := <-createResult; err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if err := <-deleteResult; err != nil {
		t.Fatal(err)
	}
	if _, err := secondCases.Find(ctx, caseEntry.TestCase.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted Case still found: %v", err)
	}
	if _, err := secondPlans.Get(ctx, plan.ID); err != nil {
		t.Fatalf("Get(plan) error = %v", err)
	}
}

type failingReadCaseCatalog struct {
	*casecatalog.Service
	err error
}

func (catalog *failingReadCaseCatalog) Entries(context.Context) ([]casecatalog.Entry, error) {
	return nil, catalog.err
}

type failingReadSuiteCatalog struct {
	*suitecatalog.Service
	err error
}

func (catalog *failingReadSuiteCatalog) Entries(context.Context) ([]suitecatalog.Entry, error) {
	return nil, catalog.err
}

type blockingCaseCatalog struct {
	*casecatalog.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (catalog *blockingCaseCatalog) SaveCase(ctx context.Context, group, directory string, testCase domain.TestCase) error {
	catalog.once.Do(func() {
		close(catalog.entered)
		<-catalog.release
	})
	return catalog.Service.SaveCase(ctx, group, directory, testCase)
}

type blockingSuiteCatalog struct {
	*suitecatalog.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (catalog *blockingSuiteCatalog) SaveSuite(ctx context.Context, group, directory string, suite domain.Suite) error {
	catalog.once.Do(func() {
		close(catalog.entered)
		<-catalog.release
	})
	return catalog.Service.SaveSuite(ctx, group, directory, suite)
}

type blockingChannelCatalog struct {
	*channelcatalog.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (catalog *blockingChannelCatalog) CreateMapping(ctx context.Context, mapping domain.ChannelModel) error {
	catalog.once.Do(func() {
		close(catalog.entered)
		<-catalog.release
	})
	return catalog.Service.CreateMapping(ctx, mapping)
}

type blockingPlanCatalog struct {
	*plancatalog.Service
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (catalog *blockingPlanCatalog) Create(ctx context.Context, plan domain.Plan) error {
	catalog.once.Do(func() {
		close(catalog.entered)
		<-catalog.release
	})
	return catalog.Service.Create(ctx, plan)
}

func (catalog *blockingPlanCatalog) CreateDocument(ctx context.Context, document plancatalog.Document) error {
	catalog.once.Do(func() {
		close(catalog.entered)
		<-catalog.release
	})
	return catalog.Service.CreateDocument(ctx, document)
}

type committedErrorModelCatalog struct {
	*modelcatalog.Service
}

func (catalog *committedErrorModelCatalog) Create(ctx context.Context, model domain.Model) error {
	if err := catalog.Service.Create(ctx, model); err != nil {
		return err
	}
	return fs.ErrPermission
}

type committedErrorChannelCatalog struct {
	*channelcatalog.Service
}

func (catalog *committedErrorChannelCatalog) CreateChannel(ctx context.Context, channel domain.Channel) error {
	if err := catalog.Service.CreateChannel(ctx, channel); err != nil {
		return err
	}
	return fs.ErrPermission
}

func (catalog *committedErrorChannelCatalog) CreateMapping(ctx context.Context, mapping domain.ChannelModel) error {
	if err := catalog.Service.CreateMapping(ctx, mapping); err != nil {
		return err
	}
	return fs.ErrPermission
}

type committedErrorCaseCatalog struct {
	*casecatalog.Service
}

func (catalog *committedErrorCaseCatalog) SaveCase(ctx context.Context, group, directory string, testCase domain.TestCase) error {
	if err := catalog.Service.SaveCase(ctx, group, directory, testCase); err != nil {
		return err
	}
	return fs.ErrPermission
}

type committedErrorSuiteCatalog struct {
	*suitecatalog.Service
}

func (catalog *committedErrorSuiteCatalog) SaveSuite(ctx context.Context, group, directory string, suite domain.Suite) error {
	if err := catalog.Service.SaveSuite(ctx, group, directory, suite); err != nil {
		return err
	}
	return fs.ErrPermission
}

type committedErrorPlanCatalog struct {
	*plancatalog.Service
}

func (catalog *committedErrorPlanCatalog) Create(ctx context.Context, plan domain.Plan) error {
	if err := catalog.Service.Create(ctx, plan); err != nil {
		return err
	}
	return fs.ErrPermission
}

func (catalog *committedErrorPlanCatalog) CreateDocument(ctx context.Context, document plancatalog.Document) error {
	if err := catalog.Service.CreateDocument(ctx, document); err != nil {
		return err
	}
	return fs.ErrPermission
}

func (catalog *committedErrorPlanCatalog) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if err := catalog.Service.Delete(ctx, id, expectedRevision); err != nil {
		return err
	}
	return fs.ErrPermission
}

func filesystemCatalogMappingFixtures(targetless bool) (domain.Channel, domain.ChannelModel, domain.Plan) {
	now := time.Date(2026, 9, 4, 15, 0, 0, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	channel := domain.Channel{
		EntityMeta: meta("62000000-0000-4000-8000-000000000001"),
		Name:       "channel", BaseURL: "https://api.example.test/v1", Enabled: true,
	}
	mapping := domain.ChannelModel{Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
		EntityMeta: meta("62000000-0000-4000-8000-000000000002"),
		ChannelID:  channel.ID, ModelID: "62000000-0000-4000-8000-000000000003", UpstreamModelName: "upstream-model",
	}
	plan := domain.Plan{
		EntityMeta: meta("62000000-0000-4000-8000-000000000004"),
		Name:       "plan", Protocol: domain.ProtocolOpenAIChat,
	}
	filesystemCatalogSetPlanSuites(&plan, domain.Suite{
		EntityMeta: meta("62000000-0000-4000-8000-000000000006"),
		Cases:      []domain.CaseRef{{CaseID: "62000000-0000-4000-8000-000000000005"}},
	})
	return channel, mapping, plan
}

func filesystemCatalogSetPlanSuites(plan *domain.Plan, suites ...domain.Suite) {
	plan.Protocol = domain.ProtocolOpenAIChat
	plan.Entries = make([]domain.PlanEntry, len(suites))
	for index, suite := range suites {
		plan.Entries[index] = domain.PlanEntry{
			EntryID:    fmt.Sprintf("65000000-0000-4000-8000-%012d", index+1),
			TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
			Parameters: map[string]json.RawMessage{},
			Load: domain.LoadProfile{
				Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
			},
			SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 3_000}},
		}
	}
}

func filesystemCatalogModelFixture(id string, protocol domain.Protocol) domain.Model {
	now := time.Date(2026, 9, 4, 15, 0, 0, 0, time.UTC)
	return domain.Model{
		EntityMeta: domain.EntityMeta{
			ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Name: "model", Protocols: []domain.Protocol{protocol},
	}
}

func filesystemCatalogSuiteFixture(testCase domain.TestCase) domain.Suite {
	now := time.Date(2026, 9, 4, 15, 45, 0, 0, time.UTC)
	return domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "64000000-0000-4000-8000-000000000001", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		},
		Key: "suite-1", Name: "file suite", Protocol: domain.ProtocolOpenAIChat,
		Cases: []domain.CaseRef{{CaseID: testCase.ID}}, Inputs: []domain.SuiteInput{},
	}
}

func filesystemCatalogTestCase(revision uint64) domain.TestCase {
	now := time.Date(2026, 9, 4, 15, 30, 0, 0, time.UTC)
	return domain.TestCase{
		EntityMeta: domain.EntityMeta{
			ID: "63000000-0000-4000-8000-000000000001", SchemaVersion: domain.CurrentEntitySchemaVersion,
			Revision: revision, CreatedAt: now, UpdatedAt: now.Add(time.Duration(revision-1) * time.Minute),
		},
		Key: "T980", Name: "file case", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic, Definitions: domain.ProtocolDefinitions{domain.Protocol(domain.CaseType("openai-chat")): []byte(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"assertions":[]}`)},
	}
}
