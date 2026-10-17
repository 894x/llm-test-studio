package integration

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	bootstrap "github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	"github.com/894x/llm-test-studio/internal/application/channelcatalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	workspacestore "github.com/894x/llm-test-studio/internal/persistence/workspace"
)

func TestFilesystemCredentialMutationLockPreventsStartupRetryFromDeletingAProvisionalKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	channels, err := channelcatalog.New(filepath.Join(root, "channels.json"))
	if err != nil {
		t.Fatal(err)
	}
	repository := workspacestore.Catalog{
		LockPath: filepath.Join(root, "catalog.lock"), Channels: channels,
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
		Repository: repository, Credentials: store, CleanupQueue: queueA, Clock: bootstrap.ProductionClock{},
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
		Repository: repository, Credentials: store, CleanupQueue: queueB, Clock: bootstrap.ProductionClock{},
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
	repository := workspacestore.Catalog{LockPath: filepath.Join(root, "catalog.lock"), Channels: channels}
	store := credentials.NewMemoryStore()
	queue := credentials.NewMemoryCleanupQueue()
	service, err := channelconfig.New(channelconfig.Dependencies{
		Repository: repository, Credentials: store, CleanupQueue: queue, Clock: bootstrap.ProductionClock{},
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
	repository := workspacestore.Catalog{LockPath: filepath.Join(root, "catalog.lock"), Channels: channels, Plans: plans}
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
		Repository: repository, Credentials: store, CleanupQueue: queue, Clock: bootstrap.ProductionClock{},
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
