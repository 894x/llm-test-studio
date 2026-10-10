package runs_test

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestRunUsesMappingProtocolRatherThanChannelDefault(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.model.Protocols = []domain.Protocol{domain.ProtocolOpenAIResponses, domain.ProtocolOpenAIChat}
	fixture.mapping.Protocols = []domain.Protocol{domain.ProtocolOpenAIChat}
	fixture.channel.Protocol = domain.ProtocolOpenAIResponses
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	ref, _ := credentials.StoreRefFromCredential(fixture.credential)
	if err := store.Set(context.Background(), ref, []byte("stored-secret")); err != nil {
		t.Fatal(err)
	}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: &recordingExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	id, err := service.PrepareTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := repository.run.Snapshot()
	if snapshot.Channel.Protocol != fixture.plan.Protocol || snapshot.Model.Protocol != fixture.plan.Protocol {
		t.Fatal("run used channel default or the first model protocol")
	}
	if err := service.CancelRun(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitForStatus(t, repository, domain.RunCancelled)
}

func TestQuickTaskValidatesMappedProtocolsAndOverridesChannelDefault(t *testing.T) {
	for _, supported := range []bool{false, true} {
		fixture := newRunFixture(t)
		fixture.channel.Protocol = domain.ProtocolOpenAIResponses
		fixture.mapping.UpstreamModelName = "test-model"
		fixture.mapping.Protocols = []domain.Protocol{domain.ProtocolOpenAIResponses}
		if supported {
			fixture.mapping.Protocols = append(fixture.mapping.Protocols, domain.ProtocolOpenAIChat)
		}
		repository := &fakeRepository{fixture: fixture}
		suite := quickTaskSuite(fixture)
		store := credentials.NewMemoryStore()
		ref, _ := credentials.StoreRefFromCredential(fixture.credential)
		if err := store.Set(context.Background(), ref, []byte("stored-secret")); err != nil {
			t.Fatal(err)
		}
		service, err := runs.New(runs.Dependencies{
			Repository:  repository,
			QuickTasks:  quickTaskCatalog{suite: suite, channel: fixture.channel, mappings: []domain.ChannelModel{fixture.mapping}},
			Credentials: store, Executor: &recordingExecutor{}, Clock: &stepClock{next: fixture.now},
			Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = service.Close() })
		id, err := service.PrepareQuickTask(context.Background(), runs.QuickTaskCommand{
			SuiteID: suite.ID, Model: "test-model", ChannelID: fixture.channel.ID,
		})
		if !supported {
			if !errors.Is(err, runs.ErrNotRunnable) || id != "" {
				t.Fatalf("unsupported mapped protocol = %q, %v", id, err)
			}
			continue
		}
		if err != nil || repository.run.Snapshot().Channel.Protocol != suite.Protocol {
			t.Fatalf("supported mapped protocol = %q, %v", id, err)
		}
		if err := service.CancelRun(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		waitForStatus(t, repository, domain.RunCancelled)
	}
}
