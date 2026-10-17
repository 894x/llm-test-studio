package bootstrap

import (
	"context"
	"errors"
	"testing"

	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/catalog"
)

type recordingCatalogCommands struct {
	calls []string
	err   error
}

func (commands *recordingCatalogCommands) record(name string) (catalog.MutationResult, error) {
	commands.calls = append(commands.calls, name)
	return catalog.MutationResult{ID: "11111111-1111-4111-8111-111111111111", Revision: 1}, commands.err
}

func (commands *recordingCatalogCommands) CreateModel(context.Context, catalog.CreateModelCommand) (catalog.MutationResult, error) {
	return commands.record("create_model")
}

func (commands *recordingCatalogCommands) UpdateModel(context.Context, catalog.UpdateModelCommand) (catalog.MutationResult, error) {
	return commands.record("update_model")
}

func (commands *recordingCatalogCommands) DeleteModel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_model")
	return err
}

func (commands *recordingCatalogCommands) CreateChannel(context.Context, catalog.CreateChannelCommand) (catalog.MutationResult, error) {
	return commands.record("create_channel")
}

func (commands *recordingCatalogCommands) UpdateChannel(context.Context, catalog.UpdateChannelCommand) (catalog.MutationResult, error) {
	return commands.record("update_channel")
}

func (commands *recordingCatalogCommands) DeleteChannel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_channel")
	return err
}

func (commands *recordingCatalogCommands) CreateChannelModel(context.Context, catalog.CreateChannelModelCommand) (catalog.MutationResult, error) {
	return commands.record("create_channel_model")
}

func (commands *recordingCatalogCommands) UpdateChannelModel(context.Context, catalog.UpdateChannelModelCommand) (catalog.MutationResult, error) {
	return commands.record("update_channel_model")
}

func (commands *recordingCatalogCommands) DeleteChannelModel(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_channel_model")
	return err
}

func (commands *recordingCatalogCommands) CreateTestCase(context.Context, catalog.CreateTestCaseCommand) (catalog.MutationResult, error) {
	return commands.record("create_test_case")
}

func (commands *recordingCatalogCommands) UpdateTestCase(context.Context, catalog.UpdateTestCaseCommand) (catalog.MutationResult, error) {
	return commands.record("update_test_case")
}

func (commands *recordingCatalogCommands) DeleteTestCase(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_test_case")
	return err
}

func (commands *recordingCatalogCommands) CreateSuite(context.Context, catalog.CreateSuiteCommand) (catalog.MutationResult, error) {
	return commands.record("create_suite")
}

func (commands *recordingCatalogCommands) UpdateSuite(context.Context, catalog.UpdateSuiteCommand) (catalog.MutationResult, error) {
	return commands.record("update_suite")
}

func (commands *recordingCatalogCommands) DeleteSuite(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_suite")
	return err
}

func (commands *recordingCatalogCommands) CreatePlan(context.Context, catalog.CreatePlanCommand) (catalog.MutationResult, error) {
	return commands.record("create_plan")
}

func (commands *recordingCatalogCommands) UpdatePlan(context.Context, catalog.UpdatePlanCommand) (catalog.MutationResult, error) {
	return commands.record("update_plan")
}

func (commands *recordingCatalogCommands) DeletePlan(context.Context, catalog.DeleteCommand) error {
	_, err := commands.record("delete_plan")
	return err
}

func assertBindingErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("binding error = nil, want code %q", want)
	}
	var bindingError bridge.DesktopBindingError
	if !errors.As(err, &bindingError) {
		t.Fatalf("binding error type = %T, want DesktopBindingError", err)
	}
	if bindingError.Code != want {
		t.Fatalf("binding error code = %q, want %q", bindingError.Code, want)
	}
}
