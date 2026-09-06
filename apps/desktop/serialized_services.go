package main

import (
	"context"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/domain"
)

// productionServiceGate serializes desktop workflows whose projections span
// the operational SQLite repository and file-backed authored catalog.
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

func (query serializedReportingQuery) Detail(ctx context.Context, reportID string) (reporting.Detail, error) {
	release := query.gate.enter()
	defer release()
	documents, ok := query.query.(interface {
		Detail(context.Context, string) (reporting.Detail, error)
	})
	if !ok || isNilInterface(documents) {
		return reporting.Detail{}, ErrReportingUnavailable
	}
	return documents.Detail(ctx, reportID)
}

func (query serializedReportingQuery) Export(ctx context.Context, reportID string, format reporting.ExportFormat, watermark string) (reporting.ExportedDocument, error) {
	release := query.gate.enter()
	defer release()
	documents, ok := query.query.(interface {
		Export(context.Context, string, reporting.ExportFormat, string) (reporting.ExportedDocument, error)
	})
	if !ok || isNilInterface(documents) {
		return reporting.ExportedDocument{}, ErrReportingUnavailable
	}
	return documents.Export(ctx, reportID, format, watermark)
}

type serializedQuickPerformanceArchive struct {
	gate    *productionServiceGate
	archive quicktest.PerformanceArchive
}

func (archive serializedQuickPerformanceArchive) SaveQuickPerformanceReport(ctx context.Context, report quicktest.PerformanceReport) error {
	release := archive.gate.enter()
	defer release()
	return archive.archive.SaveQuickPerformanceReport(ctx, report)
}

type serializedCatalogService struct {
	gate     *productionServiceGate
	query    CatalogQuery
	commands CatalogCommands
	channels interface {
		Create(context.Context, channelconfig.CreateCommand) (channelconfig.MutationResult, error)
		Update(context.Context, channelconfig.UpdateCommand) (channelconfig.MutationResult, error)
		Delete(context.Context, string, uint64) error
	}
}

type planDocumentReader interface {
	GetPlanDocument(context.Context, string) (plancatalog.Document, error)
}

type releasedCredentialCleaner interface {
	ScheduleCredentialCleanup(context.Context, ...string) error
	CleanupCredentialIfUnreferenced(string)
}

// catalogCommandsWithPlanDocuments keeps the public command surface narrow
// while giving the desktop coordinator read access to the pre-mutation Plan
// bindings required for safe keyring cleanup.
type catalogCommandsWithPlanDocuments struct {
	CatalogCommands
	documents planDocumentReader
}

func (commands catalogCommandsWithPlanDocuments) GetPlanDocument(ctx context.Context, id string) (plancatalog.Document, error) {
	return commands.documents.GetPlanDocument(ctx, id)
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
	if service.channels != nil {
		result, err := service.channels.Create(ctx, channelconfig.CreateCommand{
			Name: command.Name, BaseURL: command.BaseURL, APIKey: command.APIKey,
			Protocol: command.Protocol, Enabled: command.Enabled,
		})
		return catalog.MutationResult{ID: result.ChannelID, Revision: result.ChannelRevision}, err
	}
	return service.commands.CreateChannel(ctx, command)
}

func (service serializedCatalogService) UpdateChannel(ctx context.Context, command catalog.UpdateChannelCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	if service.channels != nil {
		result, err := service.channels.Update(ctx, channelconfig.UpdateCommand{
			ID: command.ID, ExpectedRevision: command.ExpectedRevision,
			Name: command.Name, BaseURL: command.BaseURL, APIKey: command.APIKey,
			Protocol: command.Protocol, Enabled: command.Enabled,
		})
		return catalog.MutationResult{ID: result.ChannelID, Revision: result.ChannelRevision}, err
	}
	return service.commands.UpdateChannel(ctx, command)
}

func (service serializedCatalogService) DeleteChannel(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	if service.channels != nil {
		return service.channels.Delete(ctx, command.ID, command.ExpectedRevision)
	}
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
	result, err := service.commands.CreateTestCase(ctx, command)
	if err != nil {
		return result, err
	}
	return service.canonicalCaseMutationResult(ctx, command.Protocol, command.Key, result)
}

func (service serializedCatalogService) UpdateTestCase(ctx context.Context, command catalog.UpdateTestCaseCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	result, err := service.commands.UpdateTestCase(ctx, command)
	if err != nil {
		return result, err
	}
	return service.canonicalCaseMutationResult(ctx, command.Protocol, command.Key, result)
}

func (service serializedCatalogService) DeleteTestCase(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteTestCase(ctx, command)
}

func (service serializedCatalogService) CreateSuite(ctx context.Context, command catalog.CreateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	result, err := service.commands.CreateSuite(ctx, command)
	if err != nil {
		return result, err
	}
	return service.canonicalSuiteMutationResult(ctx, command.Protocol, command.Key, result)
}

func (service serializedCatalogService) UpdateSuite(ctx context.Context, command catalog.UpdateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	result, err := service.commands.UpdateSuite(ctx, command)
	if err != nil {
		return result, err
	}
	return service.canonicalSuiteMutationResult(ctx, command.Protocol, command.Key, result)
}

func (service serializedCatalogService) canonicalCaseMutationResult(
	ctx context.Context,
	protocol domain.Protocol,
	key string,
	fallback catalog.MutationResult,
) (catalog.MutationResult, error) {
	snapshot, err := service.query.Snapshot(ctx)
	if err != nil {
		return fallback, nil
	}
	for _, testCase := range snapshot.TestCases {
		if testCase.Protocol == protocol && testCase.Key == key {
			return catalog.MutationResult{ID: testCase.ID, Revision: testCase.Revision}, nil
		}
	}
	return fallback, nil
}

func (service serializedCatalogService) canonicalSuiteMutationResult(
	ctx context.Context,
	protocol domain.Protocol,
	key string,
	fallback catalog.MutationResult,
) (catalog.MutationResult, error) {
	snapshot, err := service.query.Snapshot(ctx)
	if err != nil {
		return fallback, nil
	}
	for _, suite := range snapshot.Suites {
		if suite.Protocol == protocol && suite.Key == key {
			return catalog.MutationResult{ID: suite.ID, Revision: suite.Revision}, nil
		}
	}
	return fallback, nil
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
	credentials, err := service.planCredentialCleanupCandidates(ctx, command.ID)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	if err := service.scheduleReleasedPlanCredentials(ctx, credentials); err != nil {
		return catalog.MutationResult{}, err
	}
	result, err := service.commands.UpdatePlan(ctx, command)
	service.cleanupReleasedPlanCredentials(credentials)
	return result, err
}

func (service serializedCatalogService) DeletePlan(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	credentials, err := service.planCredentialCleanupCandidates(ctx, command.ID)
	if err != nil {
		return err
	}
	if err := service.scheduleReleasedPlanCredentials(ctx, credentials); err != nil {
		return err
	}
	if err := service.commands.DeletePlan(ctx, command); err != nil {
		service.cleanupReleasedPlanCredentials(credentials)
		return err
	}
	service.cleanupReleasedPlanCredentials(credentials)
	return nil
}

func (service serializedCatalogService) planCredentialCleanupCandidates(ctx context.Context, planID string) ([]string, error) {
	if _, ok := service.channels.(releasedCredentialCleaner); !ok {
		return nil, nil
	}
	documents, ok := service.commands.(planDocumentReader)
	if !ok || isNilInterface(documents) {
		return nil, nil
	}
	document, err := documents.GetPlanDocument(ctx, planID)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(document.TargetBindings))
	credentials := make([]string, 0, len(document.TargetBindings))
	for _, binding := range document.TargetBindings {
		credentialID := binding.Channel.CredentialID
		if !domain.IsUUID(credentialID) {
			continue
		}
		if _, exists := seen[credentialID]; exists {
			continue
		}
		seen[credentialID] = struct{}{}
		credentials = append(credentials, credentialID)
	}
	return credentials, nil
}

func (service serializedCatalogService) scheduleReleasedPlanCredentials(ctx context.Context, credentials []string) error {
	cleaner, ok := service.channels.(releasedCredentialCleaner)
	if !ok || isNilInterface(cleaner) || len(credentials) == 0 {
		return nil
	}
	return cleaner.ScheduleCredentialCleanup(ctx, credentials...)
}

func (service serializedCatalogService) cleanupReleasedPlanCredentials(credentials []string) {
	cleaner, ok := service.channels.(releasedCredentialCleaner)
	if !ok || isNilInterface(cleaner) {
		return
	}
	for _, credentialID := range credentials {
		cleaner.CleanupCredentialIfUnreferenced(credentialID)
	}
}

func filesystemCaseDirectory(key string) string {
	var result strings.Builder
	for _, character := range key {
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9', character == '-', character == '_', character == '.':
			result.WriteRune(character)
		default:
			result.WriteByte('-')
		}
	}
	value := strings.Trim(result.String(), "-.")
	if value == "" {
		return "case"
	}
	return value
}

func filesystemSuiteDirectory(key string) string {
	return filesystemCaseDirectory(key)
}
