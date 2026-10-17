package jobs

import (
	"context"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type LaunchKind string

const (
	LaunchPlan        LaunchKind = "plan"
	LaunchQuickTask   LaunchKind = "quick_task"
	LaunchPerformance LaunchKind = "performance"
	LaunchComparison  LaunchKind = "comparison"
)

// LaunchCommand selects exactly one authored preparation and an explicit
// location. Temporary credentials are consumed without entering the outbox.
type LaunchCommand struct {
	Location    Location                      `json:"location"`
	Kind        LaunchKind                    `json:"kind"`
	Plan        *runs.StartCommand            `json:"plan"`
	QuickTask   *runs.QuickTaskCommand        `json:"quick_task"`
	Performance *quicktest.PerformanceCommand `json:"performance"`
	Comparison  *comparisons.StartCommand     `json:"comparison"`
}

func (command LaunchCommand) String() string   { return "LaunchCommand{redacted}" }
func (command LaunchCommand) GoString() string { return command.String() }

func (command LaunchCommand) Validate() error {
	if command.Location.Validate() != nil {
		return ErrInvalid
	}
	count := 0
	for _, present := range []bool{
		command.Plan != nil, command.QuickTask != nil, command.Performance != nil, command.Comparison != nil,
	} {
		if present {
			count++
		}
	}
	if count != 1 {
		return ErrInvalid
	}
	switch command.Kind {
	case LaunchPlan:
		if command.Plan != nil {
			return nil
		}
	case LaunchQuickTask:
		if command.QuickTask != nil {
			return nil
		}
	case LaunchPerformance:
		if command.Performance != nil {
			return nil
		}
	case LaunchComparison:
		if command.Comparison != nil && len(command.Comparison.ChannelIDs) <= jobcontract.MaxSecrets {
			return nil
		}
	}
	return ErrInvalid
}

type RunPreparation interface {
	BuildTarget(context.Context, runs.StartCommand) (runs.PreparedRun, *credentials.Lease, error)
	BuildQuickTask(context.Context, runs.QuickTaskCommand) (runs.PreparedRun, *credentials.Lease, error)
}

type ComparisonPreparation interface {
	Build(context.Context, comparisons.StartCommand) (comparisons.PreparedComparison, map[string]*credentials.Lease, error)
}

type PerformancePreparation interface {
	BuildPerformance(
		context.Context,
		quicktest.PerformanceCommand,
	) (quicktest.PreparedPerformance, *credentials.Lease, domain.ErrorCode)
}

type LauncherDependencies struct {
	Coordinator *Coordinator
	Runs        RunPreparation
	Comparisons ComparisonPreparation
	Performance PerformancePreparation
	Now         func() time.Time
	MetaFactory func(time.Time) (domain.EntityMeta, error)
}

// Launcher composes the existing read-only preparers with the single complete
// job delivery path. It owns neither execution nor another goroutine.
type Launcher struct {
	coordinator *Coordinator
	runs        RunPreparation
	comparisons ComparisonPreparation
	performance PerformancePreparation
	now         func() time.Time
	metaFactory func(time.Time) (domain.EntityMeta, error)
}

func NewLauncher(dependencies LauncherDependencies) (*Launcher, error) {
	missingPreparation := nilPort(dependencies.Runs) || nilPort(dependencies.Comparisons) ||
		nilPort(dependencies.Performance)
	if dependencies.Coordinator == nil || missingPreparation || dependencies.Now == nil {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	return &Launcher{coordinator: dependencies.Coordinator, runs: dependencies.Runs,
		comparisons: dependencies.Comparisons, performance: dependencies.Performance,
		now: dependencies.Now, metaFactory: factory}, nil
}

type PreparationError struct{ Code domain.ErrorCode }

func (err PreparationError) Error() string { return string(err.Code) }
func (err PreparationError) Unwrap() error { return ErrInvalid }

func (launcher *Launcher) Launch(ctx context.Context, command LaunchCommand) (SubmissionRecord, error) {
	if launcher == nil || ctx == nil || command.Validate() != nil {
		return SubmissionRecord{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return SubmissionRecord{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	meta, err := launcher.metaFactory(launcher.now().UTC())
	if err != nil {
		return SubmissionRecord{}, err
	}
	if meta.Validate() != nil || meta.Revision != 1 {
		return SubmissionRecord{}, ErrInvalid
	}
	leases := map[string]*credentials.Lease{}
	defer func() { closeLeases(leases) }()
	prepared := PrepareCommand{Location: command.Location, JobID: meta.ID, Assets: []jobcontract.Asset{}}
	switch command.Kind {
	case LaunchPlan, LaunchQuickTask:
		var input runs.PreparedRun
		var lease *credentials.Lease
		if command.Kind == LaunchPlan {
			input, lease, err = launcher.runs.BuildTarget(ctx, *command.Plan)
		} else {
			input, lease, err = launcher.runs.BuildQuickTask(ctx, *command.QuickTask)
		}
		leases[input.Meta.ID] = lease
		prepared.Kind, prepared.Payload.Run = jobcontract.KindRun, &input
	case LaunchComparison:
		var input comparisons.PreparedComparison
		input, leases, err = launcher.comparisons.Build(ctx, *command.Comparison)
		prepared.Kind, prepared.Payload.Comparison = jobcontract.KindComparison, &input
	case LaunchPerformance:
		input, lease, code := launcher.performance.BuildPerformance(ctx, *command.Performance)
		leases[input.ReportID] = lease
		if code != "" {
			if code.Validate() != nil {
				code = quicktest.ErrorInvalidRequest
			}
			err = PreparationError{Code: code}
		}
		prepared.Kind, prepared.Payload.Performance = jobcontract.KindPerformance, &input
	}
	if canceled := ctx.Err(); canceled != nil {
		return SubmissionRecord{}, canceled
	}
	if err != nil {
		return SubmissionRecord{}, err
	}
	record, err := launcher.coordinator.Prepare(ctx, prepared)
	if err != nil {
		return record, err
	}
	return launcher.coordinator.Submit(ctx, record.Prepared.Spec.JobID, leases)
}
