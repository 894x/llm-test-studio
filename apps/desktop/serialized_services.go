package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/application/casecatalog"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/channelconfig"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
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
	gate      *productionServiceGate
	query     CatalogQuery
	commands  CatalogCommands
	caseTypes *casetypes.Registry
	channels  interface {
		Create(context.Context, channelconfig.CreateCommand) (channelconfig.MutationResult, error)
		Update(context.Context, channelconfig.UpdateCommand) (channelconfig.MutationResult, error)
	}
	caseFiles interface {
		Entries(context.Context) ([]casecatalog.Entry, error)
		Find(context.Context, string) (casecatalog.Entry, error)
		SaveCase(context.Context, string, string, domain.TestCase) error
		Delete(context.Context, string, uint64) error
	}
	caseSnapshots interface {
		EnsureTestCaseSnapshot(context.Context, domain.TestCase) (bool, error)
		DeleteUnreferencedTestCaseSnapshot(context.Context, string, uint64) error
		GetTestCaseRevision(context.Context, string, uint64) (domain.TestCase, error)
		IsTestCaseSnapshotReferenced(context.Context, string, uint64) (bool, error)
	}
}

func (service serializedCatalogService) Snapshot(ctx context.Context) (catalog.Snapshot, error) {
	release := service.gate.enter()
	defer release()
	snapshot, err := service.query.Snapshot(ctx)
	if err != nil || service.caseFiles == nil {
		return snapshot, err
	}
	entries, err := service.caseFiles.Entries(ctx)
	if err != nil {
		return catalog.Snapshot{}, err
	}
	snapshot.TestCases = make([]catalog.TestCaseSummary, len(entries))
	for index, entry := range entries {
		snapshot.TestCases[index] = caseSummary(entry.TestCase)
	}
	return snapshot, nil
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
	if service.caseFiles != nil {
		return service.saveNewFilesystemCase(ctx, command)
	}
	return service.commands.CreateTestCase(ctx, command)
}

func (service serializedCatalogService) UpdateTestCase(ctx context.Context, command catalog.UpdateTestCaseCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	if service.caseFiles != nil {
		entry, err := service.caseFiles.Find(ctx, command.ID)
		if err != nil || entry.TestCase.Revision != command.ExpectedRevision || entry.TestCase.Protocol != command.Protocol || entry.TestCase.Key != command.Key {
			return catalog.MutationResult{}, catalog.ErrInvalid
		}
		updated := filesystemCaseFromCommand(entry.TestCase.EntityMeta, catalog.CreateTestCaseCommand{
			Key: command.Key, Name: command.Name, Dimension: command.Dimension, Protocol: command.Protocol,
			Enabled: command.Enabled, Default: command.Default, Severity: command.Severity,
			ExecutionMode: command.ExecutionMode, DefinitionSchemaVersion: command.DefinitionSchemaVersion,
			Type: command.Type, TypeVersion: command.TypeVersion, Spec: command.Spec,
		})
		if err := service.caseTypeRegistry().Validate(updated.Protocol, updated.Definition); err != nil {
			return catalog.MutationResult{}, catalog.ErrInvalid
		}
		if updated.Definition.Type != entry.TestCase.Definition.Type || updated.Definition.TypeVersion != entry.TestCase.Definition.TypeVersion {
			if descriptor, found := service.caseTypeRegistry().Descriptor(updated.Definition.Type, updated.Definition.TypeVersion); !found || !descriptor.Creatable {
				return catalog.MutationResult{}, catalog.ErrInvalid
			}
		}
		if err := service.caseFiles.SaveCase(ctx, entry.Group, entry.Directory, updated); err != nil {
			return catalog.MutationResult{}, catalog.ErrInvalid
		}
		return service.findFilesystemCaseResult(ctx, command.Protocol, command.Key)
	}
	return service.commands.UpdateTestCase(ctx, command)
}

func (service serializedCatalogService) DeleteTestCase(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	if service.caseFiles != nil {
		if service.caseSnapshots != nil {
			referenced, err := service.caseSnapshots.IsTestCaseSnapshotReferenced(ctx, command.ID, command.ExpectedRevision)
			if err != nil {
				return err
			}
			if referenced {
				return catalog.ErrConflict
			}
		}
		if err := service.caseFiles.Delete(ctx, command.ID, command.ExpectedRevision); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return catalog.ErrNotFound
			}
			return catalog.ErrInvalid
		}
		return nil
	}
	return service.commands.DeleteTestCase(ctx, command)
}

func (service serializedCatalogService) CreateSuite(ctx context.Context, command catalog.CreateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	created, err := service.materializeCases(ctx, command.Cases)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	result, err := service.commands.CreateSuite(ctx, command)
	if err != nil {
		return catalog.MutationResult{}, errors.Join(err, service.cleanupMaterializedCases(created))
	}
	return result, nil
}

func (service serializedCatalogService) UpdateSuite(ctx context.Context, command catalog.UpdateSuiteCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	created, err := service.materializeCases(ctx, command.Cases)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	result, err := service.commands.UpdateSuite(ctx, command)
	if err != nil {
		return catalog.MutationResult{}, errors.Join(err, service.cleanupMaterializedCases(created))
	}
	return result, nil
}

func (service serializedCatalogService) DeleteSuite(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeleteSuite(ctx, command)
}

func (service serializedCatalogService) CreatePlan(ctx context.Context, command catalog.CreatePlanCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	created, err := service.materializeCases(ctx, command.Cases)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	result, err := service.commands.CreatePlan(ctx, command)
	if err != nil {
		return catalog.MutationResult{}, errors.Join(err, service.cleanupMaterializedCases(created))
	}
	return result, nil
}

func (service serializedCatalogService) UpdatePlan(ctx context.Context, command catalog.UpdatePlanCommand) (catalog.MutationResult, error) {
	release := service.gate.enter()
	defer release()
	created, err := service.materializeCases(ctx, command.Cases)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	result, err := service.commands.UpdatePlan(ctx, command)
	if err != nil {
		return catalog.MutationResult{}, errors.Join(err, service.cleanupMaterializedCases(created))
	}
	return result, nil
}

func (service serializedCatalogService) saveNewFilesystemCase(ctx context.Context, command catalog.CreateTestCaseCommand) (catalog.MutationResult, error) {
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return catalog.MutationResult{}, catalog.ErrInvalid
	}
	testCase := filesystemCaseFromCommand(meta, command)
	if err := testCase.Validate(); err != nil {
		return catalog.MutationResult{}, catalog.ErrInvalid
	}
	registry := service.caseTypeRegistry()
	if err := registry.Validate(testCase.Protocol, testCase.Definition); err != nil {
		return catalog.MutationResult{}, catalog.ErrInvalid
	}
	if descriptor, found := registry.Descriptor(testCase.Definition.Type, testCase.Definition.TypeVersion); !found || !descriptor.Creatable {
		return catalog.MutationResult{}, catalog.ErrInvalid
	}
	directory := filesystemCaseDirectory(command.Key)
	entries, err := service.caseFiles.Entries(ctx)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	for _, entry := range entries {
		if entry.TestCase.Protocol == command.Protocol && entry.TestCase.Key == command.Key ||
			entry.Group == string(command.Protocol) && entry.Directory == directory {
			return catalog.MutationResult{}, catalog.ErrConflict
		}
	}
	if err := service.caseFiles.SaveCase(ctx, string(command.Protocol), directory, testCase); err != nil {
		return catalog.MutationResult{}, catalog.ErrInvalid
	}
	return service.findFilesystemCaseResult(ctx, command.Protocol, command.Key)
}

func (service serializedCatalogService) caseTypeRegistry() *casetypes.Registry {
	if service.caseTypes != nil {
		return service.caseTypes
	}
	return casetypes.MustBuiltinRegistry()
}

func (service serializedCatalogService) findFilesystemCaseResult(ctx context.Context, protocol domain.Protocol, key string) (catalog.MutationResult, error) {
	entries, err := service.caseFiles.Entries(ctx)
	if err != nil {
		return catalog.MutationResult{}, err
	}
	for _, entry := range entries {
		if entry.TestCase.Protocol == protocol && entry.TestCase.Key == key {
			return catalog.MutationResult{ID: entry.TestCase.ID, Revision: entry.TestCase.Revision}, nil
		}
	}
	return catalog.MutationResult{}, catalog.ErrNotFound
}

func (service serializedCatalogService) materializeCases(ctx context.Context, refs []catalog.CaseRevisionInput) ([]domain.CaseRevisionRef, error) {
	if service.caseFiles == nil || service.caseSnapshots == nil {
		return nil, nil
	}
	entries, err := service.caseFiles.Entries(ctx)
	if err != nil {
		return nil, err
	}
	current := make(map[string]domain.TestCase, len(entries))
	for _, entry := range entries {
		current[entry.TestCase.ID] = entry.TestCase
	}
	created := make([]domain.CaseRevisionRef, 0, len(refs))
	for _, ref := range refs {
		testCase, found := current[ref.CaseID]
		if found && testCase.Revision == ref.Revision {
			inserted, err := service.caseSnapshots.EnsureTestCaseSnapshot(ctx, testCase)
			if err != nil {
				return nil, errors.Join(err, service.cleanupMaterializedCases(created))
			}
			if inserted {
				created = append(created, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
			}
			continue
		}
		if _, err := service.caseSnapshots.GetTestCaseRevision(ctx, ref.CaseID, ref.Revision); err != nil {
			return nil, errors.Join(catalog.ErrInvalid, service.cleanupMaterializedCases(created))
		}
	}
	return created, nil
}

func (service serializedCatalogService) cleanupMaterializedCases(refs []domain.CaseRevisionRef) error {
	var cleanupErrors []error
	for _, ref := range refs {
		if err := service.caseSnapshots.DeleteUnreferencedTestCaseSnapshot(context.Background(), ref.CaseID, ref.Revision); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func filesystemCaseFromCommand(meta domain.EntityMeta, command catalog.CreateTestCaseCommand) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: command.Key, Name: command.Name, Dimension: command.Dimension, Protocol: command.Protocol,
		Enabled: command.Enabled, Default: command.Default, Severity: command.Severity, ExecutionMode: command.ExecutionMode,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: command.DefinitionSchemaVersion,
			Type:          command.Type,
			TypeVersion:   command.TypeVersion,
			Spec:          append(json.RawMessage(nil), command.Spec...),
		},
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

func caseSummary(testCase domain.TestCase) catalog.TestCaseSummary {
	return catalog.TestCaseSummary{
		ID: testCase.ID, Revision: testCase.Revision, Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
		Protocol: testCase.Protocol, Enabled: testCase.Enabled, Default: testCase.Default, Severity: testCase.Severity,
		ExecutionMode: testCase.ExecutionMode, DefinitionSchemaVersion: testCase.Definition.SchemaVersion,
		Type: testCase.Definition.Type, TypeVersion: testCase.Definition.TypeVersion,
		Spec: append(json.RawMessage(nil), testCase.Definition.Spec...),
	}
}

func (service serializedCatalogService) DeletePlan(ctx context.Context, command catalog.DeleteCommand) error {
	release := service.gate.enter()
	defer release()
	return service.commands.DeletePlan(ctx, command)
}
