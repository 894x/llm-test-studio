package main

import (
	"context"
	"sync"

	"github.com/894x/llm-studio/internal/application/catalog"
	"github.com/894x/llm-studio/internal/application/reporting"
	"github.com/894x/llm-studio/internal/application/workspace"
)

// productionServiceGate protects the single SQLite connection shared by the
// production read models and catalog commands. Desktop bindings themselves
// remain independently concurrent when their backing services allow it.
type productionServiceGate struct {
	mu sync.Mutex
}

func (gate *productionServiceGate) enter() func() {
	gate.mu.Lock()
	return gate.mu.Unlock
}

type serializedWorkspaceQuery struct {
	gate  *productionServiceGate
	query WorkspaceQuery
}

func (query serializedWorkspaceQuery) Snapshot(ctx context.Context) (workspace.Snapshot, error) {
	release := query.gate.enter()
	defer release()
	return query.query.Snapshot(ctx)
}

type serializedReportingQuery struct {
	gate  *productionServiceGate
	query ReportingQuery
}

func (query serializedReportingQuery) Snapshot(ctx context.Context) (reporting.Snapshot, error) {
	release := query.gate.enter()
	defer release()
	return query.query.Snapshot(ctx)
}

type serializedCatalogService struct {
	gate     *productionServiceGate
	query    CatalogQuery
	commands CatalogCommands
}

func (service serializedCatalogService) Snapshot(ctx context.Context) (catalog.Snapshot, error) {
	release := service.gate.enter()
	defer release()
	return service.query.Snapshot(ctx)
}

func (service serializedCatalogService) CreateModel(ctx context.Context, command catalog.CreateModelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreateModel(ctx, command)
}

func (service serializedCatalogService) UpdateModel(ctx context.Context, command catalog.UpdateModelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdateModel(ctx, command)
}

func (service serializedCatalogService) DeleteModel(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteModel(ctx, command)
}

func (service serializedCatalogService) CreateChannel(ctx context.Context, command catalog.CreateChannelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreateChannel(ctx, command)
}

func (service serializedCatalogService) UpdateChannel(ctx context.Context, command catalog.UpdateChannelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdateChannel(ctx, command)
}

func (service serializedCatalogService) DeleteChannel(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteChannel(ctx, command)
}

func (service serializedCatalogService) CreateChannelModel(ctx context.Context, command catalog.CreateChannelModelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreateChannelModel(ctx, command)
}

func (service serializedCatalogService) UpdateChannelModel(ctx context.Context, command catalog.UpdateChannelModelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdateChannelModel(ctx, command)
}

func (service serializedCatalogService) DeleteChannelModel(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteChannelModel(ctx, command)
}

func (service serializedCatalogService) CreateTestCase(ctx context.Context, command catalog.CreateTestCaseCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreateTestCase(ctx, command)
}

func (service serializedCatalogService) UpdateTestCase(ctx context.Context, command catalog.UpdateTestCaseCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdateTestCase(ctx, command)
}

func (service serializedCatalogService) DeleteTestCase(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteTestCase(ctx, command)
}

func (service serializedCatalogService) CreateSuite(ctx context.Context, command catalog.CreateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreateSuite(ctx, command)
}

func (service serializedCatalogService) UpdateSuite(ctx context.Context, command catalog.UpdateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdateSuite(ctx, command)
}

func (service serializedCatalogService) DeleteSuite(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteSuite(ctx, command)
}

func (service serializedCatalogService) CreatePlan(ctx context.Context, command catalog.CreatePlanCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.CreatePlan(ctx, command)
}

func (service serializedCatalogService) UpdatePlan(ctx context.Context, command catalog.UpdatePlanCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	return service.commands.UpdatePlan(ctx, command)
}

func (service serializedCatalogService) DeletePlan(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeletePlan(ctx, command)
}
