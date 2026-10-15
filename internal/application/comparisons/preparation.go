package comparisons

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

// PreparationRepository reads catalog definitions without creating execution facts.
type PreparationRepository interface {
	GetPlan(context.Context, string) (domain.Plan, error)
	ResolvePlanTargetSelection(
		context.Context,
		domain.Plan,
		string,
		string,
	) (domain.Model, domain.Channel, domain.ChannelModel, error)
}

type TargetPreparer interface {
	BuildTarget(context.Context, runs.StartCommand) (runs.PreparedRun, *credentials.Lease, error)
}

type PreparationDependencies struct {
	Repository  PreparationRepository
	Targets     TargetPreparer
	Clock       Clock
	MetaFactory MetaFactory
}

type Preparer struct {
	repository  PreparationRepository
	targets     TargetPreparer
	clock       Clock
	metaFactory MetaFactory
}

// PreparedComparison freezes the whole group before any child is accepted.
type PreparedComparison struct {
	Meta  domain.EntityMeta        `json:"meta"`
	Plan  domain.EntityRevisionRef `json:"plan"`
	Model domain.EntityRevisionRef `json:"model"`
	Runs  []runs.PreparedRun       `json:"runs"`
}

func NewPreparer(dependencies PreparationDependencies) (*Preparer, error) {
	missingReader := nilInterface(dependencies.Repository) || nilInterface(dependencies.Targets)
	if missingReader || nilInterface(dependencies.Clock) {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	return &Preparer{
		repository: dependencies.Repository, targets: dependencies.Targets,
		clock: dependencies.Clock, metaFactory: factory,
	}, nil
}

func (preparer *Preparer) Build(
	ctx context.Context,
	command StartCommand,
) (PreparedComparison, map[string]*credentials.Lease, error) {
	if preparer == nil || ctx == nil {
		return PreparedComparison{}, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return PreparedComparison{}, nil, err
	}
	validIDs := domain.IsUUID(command.PlanID) && domain.IsUUID(command.ModelID)
	if !validIDs || len(command.ChannelIDs) < 2 {
		return PreparedComparison{}, nil, ErrInvalid
	}
	seen := make(map[string]struct{}, len(command.ChannelIDs))
	for _, id := range command.ChannelIDs {
		if !domain.IsUUID(id) {
			return PreparedComparison{}, nil, ErrInvalid
		}
		if _, duplicate := seen[id]; duplicate {
			return PreparedComparison{}, nil, ErrInvalid
		}
		seen[id] = struct{}{}
	}
	plan, err := preparer.repository.GetPlan(ctx, command.PlanID)
	if err != nil {
		return PreparedComparison{}, nil, err
	}
	channelRefs := make([]domain.EntityRevisionRef, len(command.ChannelIDs))
	var modelRevision uint64
	for index, channelID := range command.ChannelIDs {
		model, channel, mapping, resolveErr := preparer.repository.ResolvePlanTargetSelection(
			ctx,
			plan,
			command.ModelID,
			channelID,
		)
		if err := classifyComparisonTarget(command.ModelID, plan, model, channel, mapping, resolveErr); err != nil {
			return PreparedComparison{}, nil, err
		}
		if index == 0 {
			modelRevision = model.Revision
		} else if model.Revision != modelRevision {
			return PreparedComparison{}, nil, ErrNotReady
		}
		channelRefs[index] = domain.EntityRevisionRef{ID: channel.ID, Revision: channel.Revision}
	}
	meta, err := preparer.metaFactory(preparer.clock.Now())
	if err != nil {
		return PreparedComparison{}, nil, err
	}
	prepared := PreparedComparison{
		Meta: meta, Plan: domain.EntityRevisionRef{ID: plan.ID, Revision: plan.Revision},
		Model: domain.EntityRevisionRef{ID: command.ModelID, Revision: modelRevision},
		Runs:  make([]runs.PreparedRun, 0, len(command.ChannelIDs)),
	}
	leases := make(map[string]*credentials.Lease, len(command.ChannelIDs))
	transferred := false
	defer func() {
		if !transferred {
			closePreparedLeases(leases)
		}
	}()
	for index, channelID := range command.ChannelIDs {
		input, lease, err := preparer.targets.BuildTarget(ctx, runs.StartCommand{
			PlanID: command.PlanID, ModelID: command.ModelID, ChannelID: channelID,
		})
		if err != nil {
			_ = lease.Close()
			return PreparedComparison{}, nil, fmt.Errorf("prepare comparison channel: %w", err)
		}
		if lease == nil {
			return PreparedComparison{}, nil, ErrNotReady
		}
		if input.Snapshot.Channel.EntityRevisionRef != channelRefs[index] {
			_ = lease.Close()
			return PreparedComparison{}, nil, ErrNotReady
		}
		if _, duplicate := leases[input.Meta.ID]; duplicate {
			_ = lease.Close()
			return PreparedComparison{}, nil, ErrNotReady
		}
		frozen, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
		if err != nil {
			_ = lease.Close()
			return PreparedComparison{}, nil, err
		}
		input.Snapshot = frozen.Snapshot()
		leases[input.Meta.ID] = lease
		prepared.Runs = append(prepared.Runs, input)
	}
	if _, err := prepared.Comparison(); err != nil {
		return PreparedComparison{}, nil, err
	}
	transferred = true
	return prepared, leases, nil
}

func (prepared PreparedComparison) Comparison() (domain.Comparison, error) {
	refs := make([]domain.ComparisonRunRef, 0, len(prepared.Runs))
	var definitions []byte
	for _, input := range prepared.Runs {
		run, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
		if err != nil {
			return domain.Comparison{}, fmt.Errorf("invalid prepared comparison run: %w", err)
		}
		snapshot := run.Snapshot()
		wrongScope := snapshot.Plan != prepared.Plan || snapshot.Model.EntityRevisionRef != prepared.Model
		if wrongScope || snapshot.QuickTask != nil {
			return domain.Comparison{}, ErrNotReady
		}
		frozen, err := json.Marshal(struct {
			Plan    *domain.Plan              `json:"plan"`
			Model   domain.ModelSnapshot      `json:"model"`
			Entries []domain.RunEntrySnapshot `json:"entries"`
		}{Plan: snapshot.PlanDocument, Model: snapshot.Model, Entries: snapshot.Entries})
		if err != nil {
			return domain.Comparison{}, err
		}
		if definitions != nil && !bytes.Equal(definitions, frozen) {
			return domain.Comparison{}, ErrNotReady
		}
		definitions = frozen
		refs = append(refs, domain.ComparisonRunRef{
			Channel: snapshot.Channel.EntityRevisionRef, RunID: input.Meta.ID,
		})
	}
	return domain.NewComparison(prepared.Meta, prepared.Plan, prepared.Model, refs)
}

func closePreparedLeases(leases map[string]*credentials.Lease) {
	for _, lease := range leases {
		_ = lease.Close()
	}
}
