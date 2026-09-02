// Package comparisons coordinates fair same-model, multi-channel evaluations.
package comparisons

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrInvalid  = errors.New("comparisons: invalid input")
	ErrNotReady = errors.New("comparisons: selected channels are not comparable")
)

type Repository interface {
	GetPlan(context.Context, string) (domain.Plan, error)
	GetPlanRevision(context.Context, string, uint64) (domain.Plan, error)
	ResolvePlanTargetSelection(context.Context, domain.Plan, string, string) (domain.Model, domain.Channel, domain.ChannelModel, error)
	CreateComparison(context.Context, domain.Comparison) error
	GetComparison(context.Context, string) (domain.Comparison, error)
	UpdateComparison(context.Context, uint64, domain.Comparison) error
}

type Runner interface {
	PrepareTarget(context.Context, runs.StartCommand) (string, error)
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
	repository  Repository
	runner      Runner
	clock       Clock
	metaFactory MetaFactory
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
	return &Service{repository: dependencies.Repository, runner: dependencies.Runner, clock: dependencies.Clock, metaFactory: factory}, nil
}

func (service *Service) Start(ctx context.Context, command StartCommand) (string, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.PlanID) || !domain.IsUUID(command.ModelID) || len(command.ChannelIDs) < 2 {
		return "", ErrInvalid
	}
	seen := make(map[string]struct{}, len(command.ChannelIDs))
	for _, channelID := range command.ChannelIDs {
		if !domain.IsUUID(channelID) {
			return "", ErrInvalid
		}
		if _, duplicate := seen[channelID]; duplicate {
			return "", ErrInvalid
		}
		seen[channelID] = struct{}{}
	}
	plan, err := service.repository.GetPlan(ctx, command.PlanID)
	if err != nil {
		return "", err
	}
	if !contains(plan.ModelIDs, command.ModelID) {
		return "", ErrNotReady
	}
	channelRefs := make([]domain.EntityRevisionRef, len(command.ChannelIDs))
	var modelRevision uint64
	var protocol domain.Protocol
	for index, channelID := range command.ChannelIDs {
		if !contains(plan.ChannelIDs, channelID) {
			return "", ErrNotReady
		}
		model, channel, mapping, resolveErr := service.repository.ResolvePlanTargetSelection(ctx, plan, command.ModelID, channelID)
		if resolveErr != nil {
			return "", resolveErr
		}
		if model.ID != command.ModelID || !channel.Enabled || channel.CredentialID == "" ||
			mapping.ChannelID != channel.ID || mapping.ModelID != model.ID || channel.Protocol != model.Protocol {
			return "", ErrNotReady
		}
		if index == 0 {
			modelRevision, protocol = model.Revision, model.Protocol
		} else if model.Revision != modelRevision || model.Protocol != protocol {
			return "", ErrNotReady
		}
		channelRefs[index] = domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision}
	}

	started := make([]domain.ComparisonRunRef, 0, len(command.ChannelIDs))
	for index, channelID := range command.ChannelIDs {
		runID, startErr := service.runner.PrepareTarget(ctx, runs.StartCommand{PlanID: command.PlanID, ModelID: command.ModelID, ChannelID: channelID})
		if startErr != nil {
			for _, item := range started {
				_ = service.runner.CancelRun(context.Background(), item.RunID)
			}
			return "", fmt.Errorf("start comparison channel: %w", startErr)
		}
		started = append(started, domain.ComparisonRunRef{Channel: channelRefs[index], RunID: runID})
	}
	meta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		service.cancelStarted(started)
		return "", err
	}
	comparison, err := domain.NewComparison(
		meta,
		domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
		domain.EntityRevisionRef{ID: command.ModelID, Revision: modelRevision},
		started,
	)
	if err != nil {
		service.cancelStarted(started)
		return "", err
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
		plan, err := service.repository.GetPlanRevision(ctx, comparison.Plan().ID, comparison.Plan().Revision)
		if err != nil {
			return Snapshot{}, ErrNotReady
		}
		summary := Summary{
			ID: comparison.Meta().ID, CreatedAt: comparison.Meta().CreatedAt, Status: string(comparison.Status()),
			PlanID: plan.ID, PlanName: plan.Name, ModelID: comparison.Model().ID, Channels: []ChannelResult{},
		}
		for _, item := range comparison.Runs() {
			run, err := service.runner.GetRun(ctx, item.RunID)
			if err != nil {
				return Snapshot{}, err
			}
			runSnapshot := run.Snapshot()
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
