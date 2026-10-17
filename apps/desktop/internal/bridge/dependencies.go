package bridge

import (
	"context"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/workspace"
)

// WorkspaceQuery is the presentation-neutral Application query exposed to
// the desktop adapter. Implementations must not return credentials or raw
// provider configuration.
type WorkspaceQuery interface {
	Snapshot(context.Context) (workspace.Snapshot, error)
}

// CatalogQuery exposes the secret-free catalog projection used by the
// desktop presentation layer.
type CatalogQuery interface {
	Snapshot(context.Context, catalog.Scope) (catalog.Snapshot, error)
}

// CatalogCommands mutates the six versioned catalog entity kinds. The
// desktop adapter refreshes CatalogQuery after every successful command.
type CatalogCommands interface {
	CreateModel(context.Context, catalog.CreateModelCommand) (catalog.MutationResult, error)
	UpdateModel(context.Context, catalog.UpdateModelCommand) (catalog.MutationResult, error)
	DeleteModel(context.Context, catalog.DeleteCommand) error
	CreateChannel(context.Context, catalog.CreateChannelCommand) (catalog.MutationResult, error)
	UpdateChannel(context.Context, catalog.UpdateChannelCommand) (catalog.MutationResult, error)
	DeleteChannel(context.Context, catalog.DeleteCommand) error
	CreateChannelModel(context.Context, catalog.CreateChannelModelCommand) (catalog.MutationResult, error)
	UpdateChannelModel(context.Context, catalog.UpdateChannelModelCommand) (catalog.MutationResult, error)
	DeleteChannelModel(context.Context, catalog.DeleteCommand) error
	CreateTestCase(context.Context, catalog.CreateTestCaseCommand) (catalog.MutationResult, error)
	UpdateTestCase(context.Context, catalog.UpdateTestCaseCommand) (catalog.MutationResult, error)
	DeleteTestCase(context.Context, catalog.DeleteCommand) error
	CreateSuite(context.Context, catalog.CreateSuiteCommand) (catalog.MutationResult, error)
	UpdateSuite(context.Context, catalog.UpdateSuiteCommand) (catalog.MutationResult, error)
	DeleteSuite(context.Context, catalog.DeleteCommand) error
	CreatePlan(context.Context, catalog.CreatePlanCommand) (catalog.MutationResult, error)
	UpdatePlan(context.Context, catalog.UpdatePlanCommand) (catalog.MutationResult, error)
	DeletePlan(context.Context, catalog.DeleteCommand) error
}

// ReportingQuery exposes bounded report summaries rather than complete
// evidence or provider payload documents.
type ReportingQuery interface {
	Snapshot(context.Context) (reporting.Snapshot, error)
}

type ReportGenerationSource interface {
	GenerationSnapshot() reporting.GenerationSnapshot
	SubscribeProgress(func(reporting.GenerationProgress)) func()
}

type ReportDocumentQuery interface {
	ReportingQuery
	Detail(context.Context, string) (reporting.Detail, error)
	Export(context.Context, string, reporting.ExportFormat, string) (reporting.ExportedDocument, error)
}

type LocalizedReportExporter interface {
	ExportLocalized(context.Context, string, reporting.ExportFormat, string, string) (reporting.ExportedDocument, error)
}

// QuickTaskService retains saved-task queries and credential management.
type QuickTaskService interface {
	QuickTaskHistoryQuery
	QuickTaskCredentialCommands
}

type ComparisonService interface {
	Snapshot(context.Context) (comparisons.Snapshot, error)
}

type desktopEventEmitter func(context.Context, string, ...interface{})

type Dependencies struct {
	Query                  WorkspaceQuery
	Catalog                CatalogQuery
	CatalogCommands        CatalogCommands
	Reports                ReportingQuery
	ReportGeneration       ReportGenerationSource
	Commands               QuickTaskService
	Comparisons            ComparisonService
	PerformancePreparation jobs.PerformancePreparation
	JobQuery               jobs.Query
	ControlRecords         jobs.ControlRepository
	RemoteNodes            *remotenodes.Manager
	RemoteSync             *remotesync.Watcher
	Jobs                   *jobs.Coordinator
	Launcher               JobLauncher
	Controls               JobController
	Confirmations          *jobs.ConfirmationWatcher
	ControlDelivery        *jobs.ControlDeliveryWatcher
	Close                  func() error
}

type Initializer func(context.Context) (Dependencies, error)
