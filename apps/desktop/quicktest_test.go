package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/quicktest"
	"github.com/894x/llm-studio/internal/domain"
)

type recordingQuickTestRunner struct {
	command quicktest.Command
	result  quicktest.Result
	err     error
	calls   int
	ctx     context.Context
}

func (runner *recordingQuickTestRunner) Run(ctx context.Context, command quicktest.Command) (quicktest.Result, error) {
	runner.calls++
	runner.ctx = ctx
	runner.command = command
	return runner.result, runner.err
}

type sequenceCatalogQuery struct {
	snapshots []catalog.Snapshot
	err       error
	calls     int
}

func (query *sequenceCatalogQuery) Snapshot(context.Context) (catalog.Snapshot, error) {
	query.calls++
	if query.err != nil {
		return catalog.Snapshot{}, query.err
	}
	if len(query.snapshots) == 0 {
		return catalog.Snapshot{}, nil
	}
	index := query.calls - 1
	if index >= len(query.snapshots) {
		index = len(query.snapshots) - 1
	}
	return query.snapshots[index], nil
}

type quickTestSaveCommands struct {
	*recordingCatalogCommands
	modelCommand         catalog.CreateModelCommand
	channelCommand       catalog.CreateChannelCommand
	mappingCommand       catalog.CreateChannelModelCommand
	deleteModelCommand   catalog.DeleteCommand
	deleteChannelCommand catalog.DeleteCommand
	modelResult          catalog.MutationResult
	channelResult        catalog.MutationResult
	mappingResult        catalog.MutationResult
	modelErr             error
	channelErr           error
	mappingErr           error
	deleteModelErr       error
	deleteChannelErr     error
}

func newQuickTestSaveCommands() *quickTestSaveCommands {
	return &quickTestSaveCommands{
		recordingCatalogCommands: &recordingCatalogCommands{},
		modelResult:              catalog.MutationResult{ID: "11111111-1111-4111-8111-111111111111", Revision: 1},
		channelResult:            catalog.MutationResult{ID: "22222222-2222-4222-8222-222222222222", Revision: 1},
		mappingResult:            catalog.MutationResult{ID: "33333333-3333-4333-8333-333333333333", Revision: 1},
	}
}

func (commands *quickTestSaveCommands) CreateModel(_ context.Context, command catalog.CreateModelCommand) (catalog.MutationResult, error) {
	commands.calls = append(commands.calls, "create_model")
	commands.modelCommand = command
	return commands.modelResult, commands.modelErr
}

func (commands *quickTestSaveCommands) CreateChannel(_ context.Context, command catalog.CreateChannelCommand) (catalog.MutationResult, error) {
	commands.calls = append(commands.calls, "create_channel")
	commands.channelCommand = command
	return commands.channelResult, commands.channelErr
}

func (commands *quickTestSaveCommands) CreateChannelModel(_ context.Context, command catalog.CreateChannelModelCommand) (catalog.MutationResult, error) {
	commands.calls = append(commands.calls, "create_channel_model")
	commands.mappingCommand = command
	return commands.mappingResult, commands.mappingErr
}

func (commands *quickTestSaveCommands) DeleteModel(_ context.Context, command catalog.DeleteCommand) error {
	commands.calls = append(commands.calls, "delete_model")
	commands.deleteModelCommand = command
	return commands.deleteModelErr
}

func (commands *quickTestSaveCommands) DeleteChannel(_ context.Context, command catalog.DeleteCommand) error {
	commands.calls = append(commands.calls, "delete_channel")
	commands.deleteChannelCommand = command
	return commands.deleteChannelErr
}

func TestRunQuickTestDelegatesThroughLifecycleContext(t *testing.T) {
	command := quicktest.Command{
		AddressMode: quicktest.AddressModeBaseURL,
		URL:         "https://api.example.test/v1",
		APIKey:      "sk-ephemeral",
		ModelID:     "upstream-model",
	}
	runner := &recordingQuickTestRunner{result: quicktest.Result{
		SchemaVersion: 1,
		Success:       true,
		AddressMode:   quicktest.AddressModeBaseURL,
		BaseURL:       "https://api.example.test/v1",
		Endpoint:      "https://api.example.test/v1/chat/completions",
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	lifecycle := context.Background()
	app.onStartup(lifecycle)

	result, err := app.RunQuickTest(command)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.Endpoint != runner.result.Endpoint {
		t.Fatalf("result = %#v", result)
	}
	if runner.calls != 1 || runner.command != command {
		t.Fatalf("runner calls = %d, command = %#v", runner.calls, runner.command)
	}
	if runner.ctx == nil || runner.ctx != app.ctx {
		t.Fatal("quick test did not receive the desktop lifecycle context")
	}
}

func TestRunQuickTestReturnsStableErrorsWithoutLeakingRunnerDetails(t *testing.T) {
	const sensitive = "sk-should-never-be-reported"
	runner := &recordingQuickTestRunner{err: errors.New(sensitive)}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{quickTests: runner}, nil
	})
	var reported error
	app.setErrorReporter(func(err error) { reported = err })
	app.onStartup(context.Background())

	_, err := app.RunQuickTest(quicktest.Command{})
	assertBindingErrorCode(t, err, desktopCodeOperationFailed)
	if reported == nil || strings.Contains(reported.Error(), sensitive) {
		t.Fatalf("reported error leaked runner detail: %v", reported)
	}

	missing := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{}, nil
	})
	missing.onStartup(context.Background())
	_, err = missing.RunQuickTest(quicktest.Command{})
	assertBindingErrorCode(t, err, desktopCodeQuickTestMissing)
}

func TestSaveQuickTestConnectionCreatesAllEntitiesAndReturnsAuthoritativeCatalog(t *testing.T) {
	preflight := catalog.Snapshot{SchemaVersion: 1}
	authoritative := catalog.Snapshot{SchemaVersion: 1, Models: []catalog.ModelSummary{{ID: "11111111-1111-4111-8111-111111111111", Name: "Display model"}}}
	query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{preflight, authoritative}}
	commands := newQuickTestSaveCommands()
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
	})
	app.onStartup(context.Background())

	got, err := app.SaveQuickTestConnection(validSaveQuickTestConnectionCommand())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].Name != "Display model" || query.calls != 2 {
		t.Fatalf("authoritative catalog = %#v, query calls = %d", got, query.calls)
	}
	if strings.Join(commands.calls, ",") != "create_model,create_channel,create_channel_model" {
		t.Fatalf("command order = %v", commands.calls)
	}
	if commands.modelCommand.Name != "Display model" || commands.modelCommand.Protocol != domain.ProtocolOpenAIChat || len(commands.modelCommand.Capabilities) != 1 || commands.modelCommand.Capabilities[0] != "chat" {
		t.Fatalf("model command = %#v", commands.modelCommand)
	}
	if commands.channelCommand.Name != "Direct API" || commands.channelCommand.BaseURL != "https://api.example.test/v1" || commands.channelCommand.APIKey != "sk-save-only-on-click" || commands.channelCommand.Protocol != domain.ProtocolOpenAIChat || !commands.channelCommand.Enabled {
		t.Fatalf("channel command = %#v", commands.channelCommand)
	}
	if commands.mappingCommand.ChannelID != commands.channelResult.ID || commands.mappingCommand.ModelID != commands.modelResult.ID || commands.mappingCommand.UpstreamModelName != "upstream-model" {
		t.Fatalf("mapping command = %#v", commands.mappingCommand)
	}
}

func TestSaveQuickTestConnectionReusesSelectedCatalogModel(t *testing.T) {
	const existingModelID = "44444444-4444-4444-8444-444444444444"
	preflight := catalog.Snapshot{SchemaVersion: 1, Models: []catalog.ModelSummary{{
		ID: existingModelID, Name: "Existing model", Protocol: domain.ProtocolOpenAIChat,
	}}}
	authoritative := catalog.Snapshot{SchemaVersion: 1, Models: preflight.Models}
	query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{preflight, authoritative}}
	commands := newQuickTestSaveCommands()
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
	})
	app.onStartup(context.Background())
	command := validSaveQuickTestConnectionCommand()
	command.ExistingModelID = existingModelID
	command.ModelName = ""

	got, err := app.SaveQuickTestConnection(command)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 1 || got.Models[0].ID != existingModelID {
		t.Fatalf("authoritative catalog = %#v", got)
	}
	if strings.Join(commands.calls, ",") != "create_channel,create_channel_model" {
		t.Fatalf("command order = %v", commands.calls)
	}
	if commands.mappingCommand.ModelID != existingModelID {
		t.Fatalf("mapping model id = %q, want %q", commands.mappingCommand.ModelID, existingModelID)
	}
}

func TestSaveQuickTestConnectionValidatesSelectedCatalogModelBeforeWrites(t *testing.T) {
	const existingModelID = "44444444-4444-4444-8444-444444444444"
	tests := []struct {
		name     string
		modelID  string
		models   []catalog.ModelSummary
		channels []catalog.ChannelSummary
		wantCode string
	}{
		{name: "invalid id", modelID: "not-a-uuid", wantCode: desktopCodeCatalogInvalid},
		{name: "missing id", modelID: existingModelID, channels: []catalog.ChannelSummary{{Name: "Direct API", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat}}, wantCode: desktopCodeCatalogNotFound},
		{name: "wrong protocol", modelID: existingModelID, models: []catalog.ModelSummary{{ID: existingModelID, Name: "Video", Protocol: domain.ProtocolSeedance}}, wantCode: desktopCodeCatalogInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{{Models: test.models, Channels: test.channels}}}
			commands := newQuickTestSaveCommands()
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{catalog: query, catalogCommands: commands}, nil
			})
			app.onStartup(context.Background())
			command := validSaveQuickTestConnectionCommand()
			command.ExistingModelID = test.modelID

			_, err := app.SaveQuickTestConnection(command)
			assertBindingErrorCode(t, err, test.wantCode)
			if len(commands.calls) != 0 {
				t.Fatalf("invalid selected model wrote catalog: %v", commands.calls)
			}
			if test.name == "invalid id" && query.calls != 0 {
				t.Fatalf("invalid model id queried catalog %d time(s)", query.calls)
			}
		})
	}
}

func TestSaveQuickTestConnectionDoesNotDeleteReusedModelWhenChannelCreationFails(t *testing.T) {
	const existingModelID = "44444444-4444-4444-8444-444444444444"
	query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{{Models: []catalog.ModelSummary{{
		ID: existingModelID, Name: "Existing", Protocol: domain.ProtocolOpenAIChat,
	}}}}}
	commands := newQuickTestSaveCommands()
	commands.channelErr = catalog.ErrConflict
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
	})
	app.onStartup(context.Background())
	command := validSaveQuickTestConnectionCommand()
	command.ExistingModelID = existingModelID

	_, err := app.SaveQuickTestConnection(command)
	assertBindingErrorCode(t, err, desktopCodeCatalogConflict)
	if strings.Join(commands.calls, ",") != "create_channel" {
		t.Fatalf("command order = %v", commands.calls)
	}
}

func TestSaveQuickTestConnectionRejectsInvalidInputBeforeReadingOrWritingCatalog(t *testing.T) {
	tests := []struct {
		name   string
		change func(*SaveQuickTestConnectionCommand)
	}{
		{name: "missing base URL", change: func(command *SaveQuickTestConnectionCommand) { command.BaseURL = "" }},
		{name: "padded base URL", change: func(command *SaveQuickTestConnectionCommand) { command.BaseURL = " https://api.example.test/v1" }},
		{name: "remote plain HTTP", change: func(command *SaveQuickTestConnectionCommand) { command.BaseURL = "http://api.example.test/v1" }},
		{name: "invalid port", change: func(command *SaveQuickTestConnectionCommand) { command.BaseURL = "https://api.example.test:99999/v1" }},
		{name: "URL query", change: func(command *SaveQuickTestConnectionCommand) { command.BaseURL += "?secret=value" }},
		{name: "URL userinfo", change: func(command *SaveQuickTestConnectionCommand) {
			command.BaseURL = "https://user:password@api.example.test/v1"
		}},
		{name: "missing API key", change: func(command *SaveQuickTestConnectionCommand) { command.APIKey = "  " }},
		{name: "missing model id", change: func(command *SaveQuickTestConnectionCommand) { command.ModelID = "" }},
		{name: "padded model name", change: func(command *SaveQuickTestConnectionCommand) { command.ModelName += " " }},
		{name: "missing channel name", change: func(command *SaveQuickTestConnectionCommand) { command.ChannelName = "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &sequenceCatalogQuery{}
			commands := newQuickTestSaveCommands()
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{catalog: query, catalogCommands: commands}, nil
			})
			app.onStartup(context.Background())
			command := validSaveQuickTestConnectionCommand()
			test.change(&command)

			_, err := app.SaveQuickTestConnection(command)
			assertBindingErrorCode(t, err, desktopCodeCatalogInvalid)
			if query.calls != 0 || len(commands.calls) != 0 {
				t.Fatalf("invalid input queried/wrote catalog: query=%d commands=%v", query.calls, commands.calls)
			}
		})
	}
}

func TestSaveQuickTestConnectionRejectsExistingNamesOrConnectionBeforeWrites(t *testing.T) {
	tests := []struct {
		name     string
		snapshot catalog.Snapshot
	}{
		{name: "model name", snapshot: catalog.Snapshot{Models: []catalog.ModelSummary{{Name: "display MODEL", Protocol: domain.ProtocolOpenAIChat}}}},
		{name: "channel name", snapshot: catalog.Snapshot{Channels: []catalog.ChannelSummary{{Name: "direct api", BaseURL: "https://other.example/v1", Protocol: domain.ProtocolOpenAIChat}}}},
		{name: "connection", snapshot: catalog.Snapshot{Channels: []catalog.ChannelSummary{{Name: "Other", BaseURL: "https://api.example.test/v1", Protocol: domain.ProtocolOpenAIChat}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{test.snapshot}}
			commands := newQuickTestSaveCommands()
			app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
				return desktopDependencies{catalog: query, catalogCommands: commands}, nil
			})
			app.onStartup(context.Background())

			_, err := app.SaveQuickTestConnection(validSaveQuickTestConnectionCommand())
			assertBindingErrorCode(t, err, desktopCodeCatalogConflict)
			if len(commands.calls) != 0 {
				t.Fatalf("conflict wrote catalog: %v", commands.calls)
			}
		})
	}
}

func TestSaveQuickTestConnectionCompensatesModelWhenChannelCreationFails(t *testing.T) {
	commands := newQuickTestSaveCommands()
	commands.channelErr = catalog.ErrConflict
	query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{{}}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
	})
	app.onStartup(context.Background())

	_, err := app.SaveQuickTestConnection(validSaveQuickTestConnectionCommand())
	assertBindingErrorCode(t, err, desktopCodeCatalogConflict)
	if strings.Join(commands.calls, ",") != "create_model,create_channel,delete_model" {
		t.Fatalf("command order = %v", commands.calls)
	}
	wantDelete := catalog.DeleteCommand{ID: commands.modelResult.ID, ExpectedRevision: commands.modelResult.Revision}
	if commands.deleteModelCommand != wantDelete {
		t.Fatalf("delete model command = %#v, want %#v", commands.deleteModelCommand, wantDelete)
	}
}

func TestSaveQuickTestConnectionReportsPartialSaveWithoutDeletingCredentialOwner(t *testing.T) {
	const sensitive = "sk-save-only-on-click"
	commands := newQuickTestSaveCommands()
	commands.mappingErr = errors.New(sensitive)
	query := &sequenceCatalogQuery{snapshots: []catalog.Snapshot{{}}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{catalog: query, catalogCommands: commands}, nil
	})
	var reported error
	app.setErrorReporter(func(err error) { reported = err })
	app.onStartup(context.Background())

	_, err := app.SaveQuickTestConnection(validSaveQuickTestConnectionCommand())
	assertBindingErrorCode(t, err, desktopCodeQuickTestSavePartial)
	if strings.Join(commands.calls, ",") != "create_model,create_channel,create_channel_model" {
		t.Fatalf("command order = %v", commands.calls)
	}
	if commands.deleteModelCommand.ID != "" || commands.deleteChannelCommand.ID != "" {
		t.Fatalf("unsafe compensation attempted: model=%#v channel=%#v", commands.deleteModelCommand, commands.deleteChannelCommand)
	}
	if reported == nil || strings.Contains(reported.Error(), sensitive) || strings.Contains(err.Error(), sensitive) {
		t.Fatalf("partial-save error leaked credential: returned=%v reported=%v", err, reported)
	}
}

func validSaveQuickTestConnectionCommand() SaveQuickTestConnectionCommand {
	return SaveQuickTestConnectionCommand{
		BaseURL:     "https://api.example.test/v1",
		APIKey:      "sk-save-only-on-click",
		ModelID:     "upstream-model",
		ModelName:   "Display model",
		ChannelName: "Direct API",
	}
}
