package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type sharedConnectionProbe struct {
	active  atomic.Int32
	overlap atomic.Bool
}

func (probe *sharedConnectionProbe) call() func() {
	if probe.active.Add(1) > 1 {
		probe.overlap.Store(true)
	}
	time.Sleep(20 * time.Millisecond)
	return func() { probe.active.Add(-1) }
}

type probedWorkspaceService struct{ probe *sharedConnectionProbe }

func (service probedWorkspaceService) Snapshot(context.Context) (workspace.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return workspace.Snapshot{}, nil
}

type probedCatalogService struct{ probe *sharedConnectionProbe }

func (service probedCatalogService) Snapshot(context.Context) (catalog.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return catalog.Snapshot{}, nil
}

type probedReportingService struct{ probe *sharedConnectionProbe }

func (service probedReportingService) Snapshot(context.Context) (reporting.Snapshot, error) {
	release := service.probe.call()
	defer release()
	return reporting.Snapshot{}, nil
}

type probedQuickPerformanceArchive struct{ probe *sharedConnectionProbe }

func (archive probedQuickPerformanceArchive) SaveQuickPerformanceReport(context.Context, quicktest.PerformanceReport) error {
	release := archive.probe.call()
	defer release()
	return nil
}

func TestProductionServiceGateSerializesSharedConnectionCalls(t *testing.T) {
	probe := &sharedConnectionProbe{}
	gate := &productionServiceGate{}
	workspaceQuery := serializedWorkspaceQuery{gate: gate, query: probedWorkspaceService{probe: probe}}
	catalogQuery := serializedCatalogService{gate: gate, query: probedCatalogService{probe: probe}}
	reportingQuery := serializedReportingQuery{gate: gate, query: probedReportingService{probe: probe}}
	quickArchive := serializedQuickPerformanceArchive{gate: gate, archive: probedQuickPerformanceArchive{probe: probe}}

	start := make(chan struct{})
	errors := make(chan error, 4)
	for _, call := range []func() error{
		func() error { _, err := workspaceQuery.Snapshot(context.Background()); return err },
		func() error { _, err := catalogQuery.Snapshot(context.Background()); return err },
		func() error { _, err := reportingQuery.Snapshot(context.Background()); return err },
		func() error {
			return quickArchive.SaveQuickPerformanceReport(context.Background(), quicktest.PerformanceReport{})
		},
	} {
		go func(call func() error) {
			<-start
			errors <- call()
		}(call)
	}
	close(start)
	for range 3 {
		if err := <-errors; err != nil {
			t.Fatalf("serialized service call: %v", err)
		}
	}
	if probe.overlap.Load() {
		t.Fatal("production services overlapped on their shared connection")
	}
}

type staticCatalogQuery struct {
	snapshot catalog.Snapshot
}

func (query staticCatalogQuery) Snapshot(context.Context) (catalog.Snapshot, error) {
	return query.snapshot, nil
}

func TestSerializedCatalogServiceCleansCredentialAfterPlanUpdateRemovesItsLastReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldCredentialID := "73000000-0000-4000-8000-000000000001"
	state := newPlanCredentialState(oldCredentialID)
	store := credentials.NewMemoryStore()
	oldRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, oldCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(ctx, oldRef, []byte("sk-old-plan-secret")); err != nil {
		t.Fatal(err)
	}
	channelService, err := channelconfig.New(channelconfig.Dependencies{
		Repository: state, Credentials: store, Clock: fixedSerializedClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := &planCleanupCommands{
		recordingCatalogCommands: &recordingCatalogCommands{},
		state:                    state,
	}
	service := serializedCatalogService{
		gate: &productionServiceGate{}, commands: commands, channels: channelService,
	}

	result, err := service.UpdatePlan(ctx, catalog.UpdatePlanCommand{
		ID: state.document.ID, ExpectedRevision: state.document.Revision,
	})
	if err != nil || result.ID == "" {
		t.Fatalf("UpdatePlan() = %#v, %v", result, err)
	}
	if err := store.Test(ctx, oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("old Plan credential remains after its last reference was removed: %v", err)
	}
}

func TestSerializedCatalogServiceReportsPlanCredentialCleanupFailureWithoutRollingBackDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	oldCredentialID := "73000000-0000-4000-8000-000000000011"
	state := newPlanCredentialState(oldCredentialID)
	memory := credentials.NewMemoryStore()
	oldRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, oldCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	if err := memory.Set(ctx, oldRef, []byte("sk-retained-after-delete-failure")); err != nil {
		t.Fatal(err)
	}
	deleteFailure := errors.New("operating-system keyring is unavailable")
	store := &failingDeleteCredentialStore{Store: memory, failID: oldCredentialID, err: deleteFailure}
	queue := credentials.NewMemoryCleanupQueue()
	var reported error
	channelService, err := channelconfig.New(channelconfig.Dependencies{
		Repository: state, Credentials: store, Clock: fixedSerializedClock{},
		CleanupQueue:         queue,
		ReportCleanupFailure: func(err error) { reported = err },
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := &planCleanupCommands{
		recordingCatalogCommands: &recordingCatalogCommands{},
		state:                    state,
	}
	service := serializedCatalogService{
		gate: &productionServiceGate{}, commands: commands, channels: channelService,
	}

	err = service.DeletePlan(ctx, catalog.DeleteCommand{ID: state.document.ID, ExpectedRevision: state.document.Revision})
	if err != nil {
		t.Fatalf("DeletePlan() error = %v; committed Plan deletion must not be rolled back", err)
	}
	if state.planExists {
		t.Fatal("DeletePlan() did not commit before credential cleanup")
	}
	if !errors.Is(reported, deleteFailure) {
		t.Fatalf("reported cleanup error = %v, want wrapped %v", reported, deleteFailure)
	}
	if pending, err := queue.List(ctx); err != nil || len(pending) != 1 || pending[0] != oldCredentialID {
		t.Fatalf("pending Plan credential cleanup = %#v, %v; want durable retry id", pending, err)
	}
	store.err = nil
	if err := channelService.RetryPendingCredentialCleanup(ctx); err != nil {
		t.Fatalf("RetryPendingCredentialCleanup() error = %v", err)
	}
	if pending, err := queue.List(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending cleanup after retry = %#v, %v; want empty", pending, err)
	}
	if err := memory.Test(ctx, oldRef); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatalf("old Plan credential after retry = %v, want not found", err)
	}
}

type fixedSerializedClock struct{}

func (fixedSerializedClock) Now() time.Time {
	return time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
}

type planCredentialState struct {
	channel    domain.Channel
	document   plancatalog.Document
	planExists bool
}

func newPlanCredentialState(oldCredentialID string) *planCredentialState {
	now := time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC)
	channel := domain.Channel{
		EntityMeta: domain.EntityMeta{ID: "73000000-0000-4000-8000-000000000002", SchemaVersion: 1, Revision: 2, CreatedAt: now, UpdatedAt: now},
		Name:       "current", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true,
		CredentialID: "73000000-0000-4000-8000-000000000003",
	}
	plan := domain.Plan{
		EntityMeta: domain.EntityMeta{ID: "73000000-0000-4000-8000-000000000004", SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now},
		Name:       "plan",
	}
	oldChannel := channel
	oldChannel.Revision = 1
	oldChannel.CredentialID = oldCredentialID
	return &planCredentialState{
		channel: channel,
		document: plancatalog.Document{
			FileSchemaVersion: plancatalog.CurrentFileSchemaVersion,
			Plan:              plan,
			TargetBindings:    []plancatalog.TargetBinding{{Channel: oldChannel}},
		},
		planExists: true,
	}
}

func (state *planCredentialState) CreateChannel(context.Context, domain.Channel) error { return nil }

func (state *planCredentialState) GetChannel(context.Context, string) (domain.Channel, error) {
	return state.channel, nil
}

func (state *planCredentialState) UpdateChannel(context.Context, uint64, domain.Channel) error {
	return nil
}

func (state *planCredentialState) DeleteChannel(context.Context, string, uint64) error { return nil }

func (state *planCredentialState) WithCredentialUnreferenced(_ context.Context, credentialID string, action func() error) (bool, error) {
	if state.channel.CredentialID == credentialID {
		return false, nil
	}
	if state.planExists {
		for _, binding := range state.document.TargetBindings {
			if binding.Channel.CredentialID == credentialID {
				return false, nil
			}
		}
	}
	return true, action()
}

type planCleanupCommands struct {
	*recordingCatalogCommands
	state *planCredentialState
}

func (commands *planCleanupCommands) GetPlanDocument(_ context.Context, id string) (plancatalog.Document, error) {
	if !commands.state.planExists || commands.state.document.ID != id {
		return plancatalog.Document{}, catalog.ErrNotFound
	}
	return commands.state.document, nil
}

func (commands *planCleanupCommands) UpdatePlan(context.Context, catalog.UpdatePlanCommand) (catalog.MutationResult, error) {
	commands.state.document.TargetBindings = nil
	return commands.record("update_plan")
}

func (commands *planCleanupCommands) DeletePlan(context.Context, catalog.DeleteCommand) error {
	commands.state.planExists = false
	_, err := commands.record("delete_plan")
	return err
}

type failingDeleteCredentialStore struct {
	credentials.Store
	failID string
	err    error
}

func (store *failingDeleteCredentialStore) Delete(ctx context.Context, ref credentials.StoreRef) error {
	if ref.ID() == store.failID && store.err != nil {
		return store.err
	}
	return store.Store.Delete(ctx, ref)
}
