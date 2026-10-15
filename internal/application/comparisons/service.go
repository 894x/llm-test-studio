// Package comparisons coordinates fair same-model, multi-channel evaluations.
package comparisons

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrInvalid         = errors.New("comparisons: invalid input")
	ErrNotReady        = errors.New("comparisons: selected channels are not comparable")
	ErrTargetUnmapped  = errors.New("comparisons: model is not mapped to a selected channel")
	ErrChannelNotReady = errors.New("comparisons: a selected channel is not ready")
)

type Repository interface {
	PreparationRepository
	CreateComparison(context.Context, domain.Comparison) error
	GetComparison(context.Context, string) (domain.Comparison, error)
	UpdateComparison(context.Context, uint64, domain.Comparison) error
}

type Runner interface {
	TargetPreparer
	AcceptPrepared(context.Context, runs.PreparedRun, *credentials.Lease) (string, error)
	ActivateRun(context.Context, string) error
	GetRun(context.Context, string) (domain.Run, error)
	CancelRun(context.Context, string) error
}

type readRepository interface {
	ListComparisons(context.Context) ([]domain.Comparison, error)
	ListReportsForRuns(context.Context, []string) ([]domain.Report, error)
}

type Clock interface{ Now() time.Time }
type MetaFactory func(time.Time) (domain.EntityMeta, error)

type Dependencies struct {
	Repository  Repository
	Runner      Runner
	Clock       Clock
	MetaFactory MetaFactory
}

type Service struct {
	*Preparer
	repository Repository
	runner     Runner
	clock      Clock
}

type StartCommand struct {
	PlanID     string   `json:"plan_id"`
	ModelID    string   `json:"model_id"`
	ChannelIDs []string `json:"channel_ids"`
}

const CurrentSnapshotSchemaVersion = 1

type Snapshot struct {
	SchemaVersion int       `json:"schema_version"`
	Comparisons   []Summary `json:"comparisons"`
}

type Summary struct {
	ID        string          `json:"id"`
	CreatedAt time.Time       `json:"created_at"`
	Status    string          `json:"status"`
	PlanID    string          `json:"plan_id"`
	PlanName  string          `json:"plan_name"`
	ModelID   string          `json:"model_id"`
	ModelName string          `json:"model_name"`
	Channels  []ChannelResult `json:"channels"`
}

type ChannelResult struct {
	ChannelID   string                        `json:"channel_id"`
	ChannelName string                        `json:"channel_name"`
	RunID       string                        `json:"run_id"`
	RunStatus   domain.RunStatus              `json:"run_status"`
	ReportReady bool                          `json:"report_ready"`
	Passed      bool                          `json:"passed"`
	Verdict     string                        `json:"verdict"`
	Metrics     map[string]domain.MetricValue `json:"metrics"`
}

func New(dependencies Dependencies) (*Service, error) {
	if nilInterface(dependencies.Repository) || nilInterface(dependencies.Runner) || nilInterface(dependencies.Clock) {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	preparer, err := NewPreparer(PreparationDependencies{
		Repository: dependencies.Repository, Targets: dependencies.Runner,
		Clock: dependencies.Clock, MetaFactory: factory,
	})
	if err != nil {
		return nil, err
	}
	return &Service{
		Preparer: preparer, repository: dependencies.Repository, runner: dependencies.Runner,
		clock: dependencies.Clock,
	}, nil
}

func (service *Service) Start(ctx context.Context, command StartCommand) (string, error) {
	if service == nil {
		return "", ErrInvalid
	}
	prepared, leases, err := service.Build(ctx, command)
	if err != nil {
		return "", err
	}
	defer closePreparedLeases(leases)
	comparison, err := prepared.Comparison()
	if err != nil {
		return "", err
	}
	started := make([]domain.ComparisonRunRef, 0, len(prepared.Runs))
	for _, input := range prepared.Runs {
		lease := leases[input.Meta.ID]
		delete(leases, input.Meta.ID)
		runID, startErr := service.runner.AcceptPrepared(ctx, input, lease)
		if startErr != nil {
			service.cancelStarted(started)
			return "", fmt.Errorf("accept comparison channel: %w", startErr)
		}
		started = append(started, domain.ComparisonRunRef{Channel: input.Snapshot.Channel.EntityRevisionRef, RunID: runID})
	}
	if err := service.repository.CreateComparison(ctx, comparison); err != nil {
		service.cancelStarted(started)
		return "", err
	}
	for _, item := range started {
		if err := service.runner.ActivateRun(ctx, item.RunID); err != nil {
			service.cancelStarted(started)
			return comparison.Meta().ID, fmt.Errorf("activate comparison run: %w", err)
		}
	}
	return comparison.Meta().ID, nil
}

func classifyComparisonTarget(modelID string, plan domain.Plan, model domain.Model, channel domain.Channel, mapping domain.ChannelModel, resolveErr error) error {
	if resolveErr != nil {
		switch {
		case errors.Is(resolveErr, catalog.ErrPlanProtocolMismatch):
			return resolveErr
		case errors.Is(resolveErr, catalog.ErrNotFound):
			return ErrTargetUnmapped
		case errors.Is(resolveErr, catalog.ErrInvalid):
			return ErrInvalid
		default:
			return resolveErr
		}
	}
	bindingMatches := model.ID == modelID && mapping.ChannelID == channel.ID && mapping.ModelID == model.ID &&
		model.SupportsProtocol(plan.Protocol) && mapping.SupportsProtocol(plan.Protocol)
	if !bindingMatches {
		return ErrNotReady
	}
	if !channel.Enabled || channel.CredentialID == "" {
		return ErrChannelNotReady
	}
	return nil
}

func (service *Service) cancelStarted(started []domain.ComparisonRunRef) {
	for _, item := range started {
		_ = service.runner.CancelRun(context.Background(), item.RunID)
	}
}

func (service *Service) Refresh(ctx context.Context, comparisonID string) (domain.Comparison, error) {
	if service == nil || ctx == nil || !domain.IsUUID(comparisonID) {
		return domain.Comparison{}, ErrInvalid
	}
	comparison, err := service.repository.GetComparison(ctx, comparisonID)
	if err != nil || comparison.Status() != domain.ComparisonRunning {
		return comparison, err
	}
	terminal := domain.ComparisonCompleted
	for _, item := range comparison.Runs() {
		run, runErr := service.runner.GetRun(ctx, item.RunID)
		if runErr != nil {
			return domain.Comparison{}, runErr
		}
		snapshot := run.Snapshot()
		if snapshot.Plan != comparison.Plan() || snapshot.Model.EntityRevisionRef != comparison.Model() || snapshot.Channel.EntityRevisionRef != item.Channel {
			return domain.Comparison{}, ErrNotReady
		}
		switch run.Status() {
		case domain.RunQueued, domain.RunStarting, domain.RunRunning, domain.RunDraining:
			return comparison, nil
		case domain.RunFailed:
			terminal = domain.ComparisonFailed
		case domain.RunCancelled:
			if terminal != domain.ComparisonFailed {
				terminal = domain.ComparisonCancelled
			}
		case domain.RunCompleted:
		default:
			return domain.Comparison{}, ErrNotReady
		}
	}
	updated, err := comparison.Transition(terminal, service.clock.Now())
	if err != nil {
		return domain.Comparison{}, err
	}
	if err := service.repository.UpdateComparison(ctx, comparison.Meta().Revision, updated); err != nil {
		// Snapshot polling may race with another refresher. If that caller already
		// committed the terminal transition, return the durable winner.
		current, reloadErr := service.repository.GetComparison(ctx, comparisonID)
		if reloadErr == nil && current.Status() != domain.ComparisonRunning {
			return current, nil
		}
		return domain.Comparison{}, err
	}
	return updated, nil
}

func (service *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	if service == nil || ctx == nil {
		return Snapshot{}, ErrInvalid
	}
	reader, ok := service.repository.(readRepository)
	if !ok {
		return Snapshot{}, ErrNotReady
	}
	comparisons, err := reader.ListComparisons(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	for index := range comparisons {
		if comparisons[index].Status() == domain.ComparisonRunning {
			refreshed, refreshErr := service.Refresh(ctx, comparisons[index].Meta().ID)
			if refreshErr != nil {
				return Snapshot{}, refreshErr
			}
			comparisons[index] = refreshed
		}
	}
	runIDs := make([]string, 0)
	for _, comparison := range comparisons {
		for _, item := range comparison.Runs() {
			runIDs = append(runIDs, item.RunID)
		}
	}
	reports, err := reader.ListReportsForRuns(ctx, runIDs)
	if err != nil {
		return Snapshot{}, err
	}
	reportsByRun := make(map[string]domain.Report, len(reports))
	for _, report := range reports {
		reportsByRun[report.RunID] = report
	}
	summaries := make([]Summary, 0, len(comparisons))
	for _, comparison := range comparisons {
		summary := Summary{
			ID: comparison.Meta().ID, CreatedAt: comparison.Meta().CreatedAt, Status: string(comparison.Status()),
			PlanID: comparison.Plan().ID, ModelID: comparison.Model().ID, Channels: []ChannelResult{},
		}
		for _, item := range comparison.Runs() {
			run, err := service.runner.GetRun(ctx, item.RunID)
			if err != nil {
				return Snapshot{}, err
			}
			runSnapshot := run.Snapshot()
			plan := runSnapshot.PlanDocument
			if plan == nil {
				return Snapshot{}, fmt.Errorf(
					"%w: run %s is missing its frozen plan document; start a new comparison using current definitions",
					ErrNotReady,
					item.RunID,
				)
			}
			if plan.ID != comparison.Plan().ID || plan.Revision != comparison.Plan().Revision {
				return Snapshot{}, ErrNotReady
			}
			if summary.PlanName == "" {
				summary.PlanName = plan.Name
			} else if summary.PlanName != plan.Name {
				return Snapshot{}, ErrNotReady
			}
			if summary.ModelName == "" {
				summary.ModelName = runSnapshot.Model.Name
			}
			result := ChannelResult{
				ChannelID: item.Channel.ID, ChannelName: runSnapshot.Channel.Name,
				RunID: item.RunID, RunStatus: run.Status(), Metrics: map[string]domain.MetricValue{},
			}
			if report, found := reportsByRun[item.RunID]; found {
				result.ReportReady = true
				result.Passed = report.Conclusion.Passed
				result.Verdict = report.Conclusion.Verdict
				result.Metrics = report.Metrics
			}
			summary.Channels = append(summary.Channels, result)
		}
		summaries = append(summaries, summary)
	}
	sort.Slice(summaries, func(left, right int) bool {
		if summaries[left].CreatedAt.Equal(summaries[right].CreatedAt) {
			return summaries[left].ID > summaries[right].ID
		}
		return summaries[left].CreatedAt.After(summaries[right].CreatedAt)
	})
	return Snapshot{SchemaVersion: CurrentSnapshotSchemaVersion, Comparisons: summaries}, nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
