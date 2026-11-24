package comparisons

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

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
	Meta               domain.EntityMeta        `json:"meta"`
	Plan               domain.EntityRevisionRef `json:"plan"`
	Model              domain.EntityRevisionRef `json:"model"`
	Runs               []runs.PreparedRun       `json:"runs"`
	ProtocolComparison bool                     `json:"protocol_comparison,omitempty"`
	ProtocolSuite      bool                     `json:"protocol_suite,omitempty"`
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
	if command.Protocols != nil {
		return preparer.buildProtocols(ctx, command)
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
	comparison, _, err := prepared.FrozenRuns()
	return comparison, err
}

// FrozenRuns retains the independently owned immutable run entities constructed
// while checking common comparison pins, so acceptance need not rebuild them.
func (prepared PreparedComparison) FrozenRuns() (domain.Comparison, []domain.Run, error) {
	owned := make([]domain.Run, 0, len(prepared.Runs))
	refs := make([]domain.ComparisonRunRef, 0, len(prepared.Runs))
	if prepared.ProtocolSuite && !prepared.ProtocolComparison {
		return domain.Comparison{}, nil, ErrNotReady
	}
	var definitions []byte
	var first *domain.RunSnapshot
	casePins := map[string]uint64{}
	for _, input := range prepared.Runs {
		run, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot)
		if err != nil {
			return domain.Comparison{}, nil, fmt.Errorf("invalid prepared comparison run: %w", err)
		}
		owned = append(owned, run)
		snapshot := run.Snapshot()
		wrongScope := snapshot.Plan != prepared.Plan || snapshot.Model.EntityRevisionRef != prepared.Model
		if wrongScope || snapshot.QuickTask != nil {
			return domain.Comparison{}, nil, ErrNotReady
		}
		refs = append(refs, prepared.RunRef(input))
		if prepared.ProtocolComparison {
			for _, entry := range snapshot.Entries {
				for _, ref := range entry.Cases {
					if revision, found := casePins[ref.CaseID]; found && revision != ref.Revision {
						return domain.Comparison{}, nil, ErrNotReady
					}
					casePins[ref.CaseID] = ref.Revision
				}
			}
			if prepared.ProtocolSuite && (len(snapshot.Entries) != 1 || snapshot.Entries[0].TargetKind != domain.PlanTargetSuite) {
				return domain.Comparison{}, nil, ErrNotReady
			}
			if first == nil {
				first = &snapshot
			}
			if !sameProtocolComparisonPlan(*first, snapshot) {
				return domain.Comparison{}, nil, ErrNotReady
			}
			continue
		}
		frozen, err := json.Marshal(struct {
			Plan    *domain.Plan              `json:"plan"`
			Model   domain.ModelSnapshot      `json:"model"`
			Entries []domain.RunEntrySnapshot `json:"entries"`
		}{Plan: snapshot.PlanDocument, Model: snapshot.Model, Entries: snapshot.Entries})
		if err != nil {
			return domain.Comparison{}, nil, err
		}
		if definitions != nil && !bytes.Equal(definitions, frozen) {
			return domain.Comparison{}, nil, ErrNotReady
		}
		definitions = frozen
	}
	comparison, err := domain.NewComparison(prepared.Meta, prepared.Plan, prepared.Model, refs)
	return comparison, owned, err
}

func (prepared PreparedComparison) RunRef(input runs.PreparedRun) domain.ComparisonRunRef {
	ref := domain.ComparisonRunRef{Channel: input.Snapshot.Channel.EntityRevisionRef, RunID: input.Meta.ID}
	if prepared.ProtocolComparison {
		ref.Protocol = input.Snapshot.Channel.Protocol
		ref.Suite = prepared.ProtocolSuite
	}
	return ref
}

func (preparer *Preparer) buildProtocols(
	ctx context.Context,
	command StartCommand,
) (PreparedComparison, map[string]*credentials.Lease, error) {
	if command.PlanID != "" || command.ModelID != "" || len(command.ChannelIDs) != 0 {
		return PreparedComparison{}, nil, ErrInvalid
	}
	targets, ok := preparer.targets.(interface {
		BuildProtocolTargets(context.Context, runs.ProtocolComparisonCommand, domain.EntityMeta) (
			[]runs.PreparedRun, map[string]*credentials.Lease, error,
		)
	})
	if !ok {
		return PreparedComparison{}, nil, ErrNotReady
	}
	meta, err := preparer.metaFactory(preparer.clock.Now())
	if err != nil {
		return PreparedComparison{}, nil, err
	}
	inputs, leases, err := targets.BuildProtocolTargets(ctx, *command.Protocols, meta)
	if err != nil {
		return PreparedComparison{}, nil, err
	}
	if len(inputs) != 3 {
		closePreparedLeases(leases)
		return PreparedComparison{}, nil, ErrNotReady
	}
	first := inputs[0].Snapshot
	prepared := PreparedComparison{
		Meta: meta, Plan: first.Plan, Model: first.Model.EntityRevisionRef,
		Runs: inputs, ProtocolComparison: true, ProtocolSuite: command.Protocols.Suite != nil,
	}
	if _, err := prepared.Comparison(); err != nil {
		closePreparedLeases(leases)
		return PreparedComparison{}, nil, err
	}
	return prepared, leases, nil
}

func sameProtocolComparisonPlan(first, next domain.RunSnapshot) bool {
	sameChannel := first.Channel.ID == next.Channel.ID
	if sameChannel && first.Channel.Revision != next.Channel.Revision {
		return false
	}
	if first.Model.Name != next.Model.Name || first.Channel.UpstreamModelName != next.Channel.UpstreamModelName ||
		first.Channel.BaseURL != next.Channel.BaseURL || first.Channel.Name != next.Channel.Name ||
		(first.QuickTask == nil) != (next.QuickTask == nil) {
		return false
	}
	left, right := first.PlanDocument, next.PlanDocument
	if left.EntityMeta != right.EntityMeta || left.Name != right.Name || left.Seed != right.Seed ||
		len(left.Entries) != len(right.Entries) {
		return false
	}
	for index, source := range left.Entries {
		target := right.Entries[index]
		if source.TargetKind == domain.PlanTargetSuite && target.TargetKind == domain.PlanTargetSuite {
			source.TargetID, target.TargetID = "", ""
			// A suite's effective concurrency is capped by its case count.
			if source.Load.Mode == domain.LoadSingle && target.Load.Mode == domain.LoadSingle {
				source.Load.Concurrency, target.Load.Concurrency = 0, 0
			}
		}
		if !reflect.DeepEqual(source, target) {
			return false
		}
	}
	return true
}

func closePreparedLeases(leases map[string]*credentials.Lease) {
	for _, lease := range leases {
		_ = lease.Close()
	}
}
