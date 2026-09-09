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

func TestQuickTaskExplicitCredentialRememberReplayAndForget(t *testing.T) {
	ctx := context.Background()
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	suite := quickTaskSuite(fixture)
	store := credentials.NewMemoryStore()
	service, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{suite: suite}, Credentials: credentials.NewMemoryStore(), QuickTaskCredentials: store, Executor: &controlledExecutor{}, Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	command := runs.QuickTaskCommand{SuiteID: suite.ID, Seed: 1, RequestTimeoutMS: 1000, Model: "model", BaseURL: "https://example.test", APIKey: "temporary-secret"}
	id, err := service.PrepareQuickTask(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CancelRun(ctx, id); err != nil {
		t.Fatal(err)
	}
	originalRun := repository.run
	ref, _ := credentials.NewStoreRef(domain.CredentialQuickTaskAPIKey, id)
	if err := store.Test(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("a normal test remembered a credential implicitly")
	}
	remember := runs.RememberQuickTaskCredentialCommand{RunID: id, BaseURL: command.BaseURL, Protocol: suite.Protocol, APIKey: command.APIKey}
	if err := service.RememberQuickTaskCredential(ctx, remember); err != nil {
		t.Fatal(err)
	}
	if err := service.RememberQuickTaskCredential(ctx, remember); err != nil {
		t.Fatal("retry must be idempotent", err)
	}
	history, err := service.QuickTask(ctx, id)
	if err != nil || history.CredentialRunID != id {
		t.Fatalf("remembered history: %+v, %v", history, err)
	}
	raw, _ := json.Marshal(history)
	if strings.Contains(string(raw), command.APIKey) {
		t.Fatal("history exposed the saved key")
	}
	for _, target := range []struct {
		url      string
		protocol domain.Protocol
	}{{"https://other.test", suite.Protocol}, {command.BaseURL + "/other", suite.Protocol}, {command.BaseURL, domain.ProtocolSeedance}} {
		if lease, err := service.LeaseQuickTaskCredential(ctx, id, target.url, target.protocol); err == nil {
			_ = lease.Close()
			t.Fatal("credential crossed its target boundary")
		}
	}
	// The persisted Run is sufficient after rebuilding the application service.
	reopened, err := runs.New(runs.Dependencies{Repository: repository, QuickTasks: quickTaskCatalog{}, Credentials: credentials.NewMemoryStore(), QuickTaskCredentials: store, Executor: &controlledExecutor{}, Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment }})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	command.SourceRunID, command.CredentialRunID, command.APIKey = id, id, ""
	replayID, err := reopened.PrepareQuickTask(ctx, command)
	if err != nil || replayID == id {
		t.Fatalf("credential replay: %s, %v", replayID, err)
	}
	if repository.run.Snapshot().QuickTask.CredentialRunID != id {
		t.Fatal("replay lost the non-secret reference")
	}
	if err := reopened.CancelRun(ctx, replayID); err != nil {
		t.Fatal(err)
	}
	// The fake repository stores one Run; preserve the original for forgetting.
	repository.run = originalRun
	if err := reopened.ForgetQuickTaskCredential(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ForgetQuickTaskCredential(ctx, id); err != nil {
		t.Fatal("forget should tolerate an already absent key", err)
	}
	if err := store.Test(ctx, ref); !errors.Is(err, credentials.ErrNotFound) {
		t.Fatal("forgotten key remains in the store")
	}
}
