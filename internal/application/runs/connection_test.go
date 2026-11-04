package runs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPlanTemporaryConnectionFreezesEveryEntryWithoutSavingTargetsOrSecrets(t *testing.T) {
	fixture := newRunFixture(t)
	second := fixture.plan.Entries[0]
	second.EntryID = "77777777-7777-4777-8777-777777777777"
	second.OnFailure = "stop"
	fixture.plan.Entries = append(fixture.plan.Entries, second)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	preparer, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: readOnlyPreparationRepository{delegate: repository}, Credentials: store,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preparer.Close)
	connection := runs.ConnectionCommand{
		Protocol: fixture.plan.Protocol, Model: "temporary-upstream-model",
		BaseURL: "https://temporary.example.test/v1/chat/completions", APIKey: "temporary-plan-secret",
	}
	command := runs.StartCommand{PlanID: fixture.plan.ID, Connection: &connection}
	prepared, lease, err := preparer.BuildTarget(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if prepared.PlanID != fixture.plan.ID || prepared.Snapshot.QuickTask != nil || len(prepared.Snapshot.Entries) != 2 {
		t.Fatal("temporary connection did not preserve the complete authored plan")
	}
	if !reflect.DeepEqual(prepared.Snapshot.PlanDocument.Entries, fixture.plan.Entries) {
		t.Fatal("preparation changed authored entry options")
	}
	if prepared.Snapshot.Channel.UpstreamModelName != connection.Model || prepared.Snapshot.Channel.BaseURL != connection.BaseURL {
		t.Fatal("snapshot lost its temporary connection")
	}
	if repository.run.Meta().ID != "" || repository.fixture.model.ID != fixture.model.ID || repository.fixture.channel.ID != fixture.channel.ID {
		t.Fatal("preparation wrote a run or changed an authored target")
	}
	document, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(document), connection.APIKey) || strings.Contains(fmt.Sprintf("%v %#v", command, connection), connection.APIKey) {
		t.Fatal("temporary credential escaped into prepared data or diagnostic formatting")
	}
	secret, err := lease.Bytes()
	if err != nil || string(secret) != connection.APIKey {
		t.Fatalf("separate temporary lease unavailable: %v", err)
	}
	clear(secret)
	if err := prepared.Snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanConnectionUsesSavedChannelWithoutRequiringAnAuthoredModel(t *testing.T) {
	fixture := newRunFixture(t)
	store := credentials.NewMemoryStore()
	ref, _ := credentials.StoreRefFromCredential(fixture.credential)
	if err := store.Set(context.Background(), ref, []byte("saved-plan-secret")); err != nil {
		t.Fatal(err)
	}
	repository := &fakeRepository{fixture: fixture}
	preparer, err := runs.NewPreparer(runs.PreparationDependencies{
		Repository: readOnlyPreparationRepository{delegate: repository}, Credentials: store,
		QuickTasks:  quickTaskCatalog{channel: fixture.channel, mappings: []domain.ChannelModel{fixture.mapping}},
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(preparer.Close)
	prepared, lease, err := preparer.BuildTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID,
		Connection: &runs.ConnectionCommand{
			Protocol: fixture.plan.Protocol, Model: "free-form-upstream-model", ChannelID: fixture.channel.ID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if prepared.Snapshot.Channel.ID != fixture.channel.ID || prepared.Snapshot.Model.Name != "free-form-upstream-model" {
		t.Fatal("saved channel or free-form model was replaced")
	}
	secret, err := lease.Bytes()
	if err != nil || string(secret) != "saved-plan-secret" {
		t.Fatalf("saved channel lease unavailable: %v", err)
	}
	clear(secret)
}

func TestPlanConnectionRejectsConflictsAndInvalidTargetsBeforePersisting(t *testing.T) {
	fixture := newRunFixture(t)
	valid := runs.ConnectionCommand{Protocol: fixture.plan.Protocol, Model: "temporary-model", BaseURL: "https://temporary.example.test/v1", APIKey: "temporary-key"}
	tests := []struct {
		name   string
		change func(*runs.StartCommand)
	}{
		{name: "mixed catalog selection", change: func(command *runs.StartCommand) { command.ModelID = fixture.model.ID }},
		{name: "wrong protocol", change: func(command *runs.StartCommand) { command.Connection.Protocol = domain.ProtocolAnthropicMessages }},
		{name: "missing key", change: func(command *runs.StartCommand) { command.Connection.APIKey = "" }},
		{name: "secret in address", change: func(command *runs.StartCommand) { command.Connection.BaseURL = "https://user:password@example.test/v1" }},
		{name: "model control character", change: func(command *runs.StartCommand) { command.Connection.Model = "model\nsecret" }},
		{name: "saved channel and temporary key", change: func(command *runs.StartCommand) { command.Connection.ChannelID = fixture.channel.ID }},
		{name: "invalid remembered credential", change: func(command *runs.StartCommand) { command.Connection.CredentialRunID = "invalid" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &fakeRepository{fixture: fixture}
			preparer, err := runs.NewPreparer(runs.PreparationDependencies{
				Repository: readOnlyPreparationRepository{delegate: repository}, Credentials: credentials.NewMemoryStore(),
				Clock:       &stepClock{next: fixture.now},
				Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(preparer.Close)
			connection := valid
			command := runs.StartCommand{PlanID: fixture.plan.ID, Connection: &connection}
			test.change(&command)
			_, lease, err := preparer.BuildTarget(context.Background(), command)
			if err == nil || lease != nil || repository.run.Meta().ID != "" {
				t.Fatal("invalid connection admitted or persisted")
			}
		})
	}
}
