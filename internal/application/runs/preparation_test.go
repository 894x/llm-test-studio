package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type readOnlyPreparationRepository struct{ delegate *fakeRepository }

type missingRunPreparationRepository struct {
	readOnlyPreparationRepository
	missing error
}

func (repository missingRunPreparationRepository) GetRun(context.Context, string) (domain.Run, error) {
	return domain.Run{}, repository.missing
}

func (repository readOnlyPreparationRepository) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	return repository.delegate.GetPlan(ctx, id)
}

func (repository readOnlyPreparationRepository) ResolvePlanTargetSelection(
	ctx context.Context,
	plan domain.Plan,
	modelID string,
	channelID string,
) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	return repository.delegate.ResolvePlanTargetSelection(ctx, plan, modelID, channelID)
}

func (repository readOnlyPreparationRepository) ListTestCases(ctx context.Context) ([]domain.TestCase, error) {
	return repository.delegate.ListTestCases(ctx)
}

func (repository readOnlyPreparationRepository) ListSuites(ctx context.Context) ([]domain.Suite, error) {
	return repository.delegate.ListSuites(ctx)
}

func (repository readOnlyPreparationRepository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	return repository.delegate.GetRun(ctx, id)
}

func TestPreparerFreezesTargetWithoutPersistingOrExecuting(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	ref, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), ref, []byte("prepared-secret")); err != nil {
		t.Fatal(err)
	}
	preparer, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: readOnlyPreparationRepository{delegate: repository}, Credentials: store,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preparer.Close)
	prepared, lease, err := preparer.BuildTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	before, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if repository.run.Meta().ID != "" || len(repository.results) != 0 {
		t.Fatal("preparation persisted operational data")
	}
	if strings.Contains(string(before), "prepared-secret") {
		t.Fatal("prepared transport value contains its credential")
	}
	// Mutate the catalog's shared raw bytes, rather than only a struct copy.
	raw := repository.fixture.testCase.Definitions[fixture.plan.Protocol]
	raw[0] = 'x'
	after, err := json.Marshal(prepared)
	if err != nil || string(after) != string(before) {
		t.Fatalf("prepared snapshot changed with catalog storage: %v", err)
	}
	secret, err := lease.Bytes()
	if err != nil || string(secret) != "prepared-secret" {
		t.Fatalf("caller did not receive its separate lease: %v", err)
	}
	clear(secret)
}

func TestPreparerBuildsQuickTaskWithoutCreatingRunOrKeyringEntry(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	suite := quickTaskSuite(fixture)
	preparer, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: readOnlyPreparationRepository{delegate: repository},
		QuickTasks: quickTaskCatalog{suite: suite}, Credentials: store,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preparer.Close)
	prepared, lease, err := preparer.BuildQuickTask(context.Background(), runs.QuickTaskCommand{
		SuiteID: suite.ID, Seed: 4, RequestTimeoutSeconds: 1,
		Model: "temporary-model", BaseURL: "http://example.test/v1", APIKey: "transient-secret",
		Inputs: map[string]json.RawMessage{"prompt": json.RawMessage(`"frozen prompt"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if repository.run.Meta().ID != "" {
		t.Fatal("quick task preparation created a Run")
	}
	if prepared.Meta.ID != prepared.PlanID || prepared.Snapshot.QuickTask == nil {
		t.Fatal("prepared quick task lost its standalone identity")
	}
	input := prepared.Snapshot.Entries[0].CaseInputs[fixture.testCase.ID]["prompt"]
	if string(input) != `"frozen prompt"` {
		t.Fatalf("bound input = %s", input)
	}
	ref, err := credentials.NewStoreRef(domain.CredentialQuickTaskAPIKey, prepared.Meta.ID)
	if err != nil {
		t.Fatal(err)
	}
		if err := store.Test(context.Background(), ref); err == nil {
			t.Fatal("transient preparation wrote a keyring entry")
		}
}

func TestPreparerDegradesQuickTaskWithMissingSourceRun(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	suite := quickTaskSuite(fixture)
	missingRun := errors.New("repository record not found")
	command := runs.QuickTaskCommand{
		SuiteID: suite.ID, Seed: 4, RequestTimeoutSeconds: 1,
		Model: "temporary-model", BaseURL: "http://example.test/v1", APIKey: "transient-secret",
		SourceRunID: "99999999-9999-4999-8999-999999999999",
		Inputs:      map[string]json.RawMessage{"prompt": json.RawMessage(`"frozen prompt"`)},
	}

	declared, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: missingRunPreparationRepository{readOnlyPreparationRepository{delegate: repository}, missingRun},
		QuickTasks: quickTaskCatalog{suite: suite}, Credentials: store,
		MissingRun:  missingRun,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(declared.Close)
	prepared, lease, err := declared.BuildQuickTask(context.Background(), command)
	if err != nil {
		t.Fatalf("BuildQuickTask(missing source) error = %v, want a fresh start", err)
	}
	_ = lease.Close()
	if prepared.Meta.ID == "" || prepared.Snapshot.QuickTask == nil {
		t.Fatalf("prepared = %#v, want a standalone quick task", prepared)
	}

	undeclared, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: missingRunPreparationRepository{readOnlyPreparationRepository{delegate: repository}, missingRun},
		QuickTasks: quickTaskCatalog{suite: suite}, Credentials: store,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(undeclared.Close)
	if _, _, err := undeclared.BuildQuickTask(context.Background(), command); err == nil {
		t.Fatal("BuildQuickTask(missing source) error = nil, want the repository error without a declared sentinel")
	}
}
