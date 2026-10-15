package comparisons_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type readOnlyComparisonRepository struct{ repository *comparisonRepository }

func (reader readOnlyComparisonRepository) GetPlan(ctx context.Context, id string) (domain.Plan, error) {
	return reader.repository.GetPlan(ctx, id)
}

func (reader readOnlyComparisonRepository) ResolvePlanTargetSelection(
	ctx context.Context,
	plan domain.Plan,
	modelID string,
	channelID string,
) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	return reader.repository.ResolvePlanTargetSelection(ctx, plan, modelID, channelID)
}

type comparisonTargetBuilder func(context.Context, runs.StartCommand) (runs.PreparedRun, *credentials.Lease, error)

func (builder comparisonTargetBuilder) BuildTarget(
	ctx context.Context,
	command runs.StartCommand,
) (runs.PreparedRun, *credentials.Lease, error) {
	return builder(ctx, command)
}

func TestComparisonPreparationFreezesWholeGroupWithoutExecutionWrites(t *testing.T) {
	fixture := newComparisonFixture(t)
	repository := &comparisonRepository{fixture: fixture}
	targets := &comparisonRunner{fixture: fixture, repository: repository}
	preparer, err := comparisons.NewPreparer(comparisons.PreparationDependencies{
		Repository: readOnlyComparisonRepository{repository: repository}, Targets: targets,
		Clock: &comparisonClock{now: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, leases, err := preparer.Build(context.Background(), comparisons.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID,
		ChannelIDs: []string{fixture.channels[0].ID, fixture.channels[1].ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, lease := range leases {
			_ = lease.Close()
		}
	}()
	if repository.comparison.Meta().ID != "" || len(targets.activated) != 0 || len(leases) != 2 {
		t.Fatal("preparation wrote comparison facts or activated a run")
	}
	encoded, err := json.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "comparison-test-secret") {
		t.Fatal("prepared comparison serialized its credential")
	}
	var transported comparisons.PreparedComparison
	if err := json.Unmarshal(encoded, &transported); err != nil {
		t.Fatal(err)
	}
	comparison, err := transported.Comparison()
	if err != nil || len(comparison.Runs()) != 2 || comparison.Meta().ID != prepared.Meta.ID {
		t.Fatalf("transported comparison: %v", err)
	}
	transported.Runs[1].Snapshot.PlanDocument.Name = "changed after freezing"
	if _, err := transported.Comparison(); !errors.Is(err, comparisons.ErrNotReady) {
		t.Fatalf("mixed definitions were accepted: %v", err)
	}
	if _, err := prepared.Comparison(); err != nil {
		t.Fatalf("transport mutation changed original preparation: %v", err)
	}
}

func TestComparisonPreparationReleasesEarlierCredentialsOnFailure(t *testing.T) {
	fixture := newComparisonFixture(t)
	repository := &comparisonRepository{fixture: fixture}
	targets := &comparisonRunner{fixture: fixture, repository: repository}
	var firstLease *credentials.Lease
	preparer, err := comparisons.NewPreparer(comparisons.PreparationDependencies{
		Repository: readOnlyComparisonRepository{repository: repository},
		Targets: comparisonTargetBuilder(func(ctx context.Context, command runs.StartCommand) (
			runs.PreparedRun, *credentials.Lease, error,
		) {
			if firstLease != nil {
				return runs.PreparedRun{}, nil, errors.New("second channel cannot be prepared")
			}
			prepared, lease, err := targets.BuildTarget(ctx, command)
			firstLease = lease
			return prepared, lease, err
		}),
		Clock: &comparisonClock{now: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, leases, err := preparer.Build(context.Background(), comparisons.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID,
		ChannelIDs: []string{fixture.channels[0].ID, fixture.channels[1].ID},
	})
	if err == nil || leases != nil || firstLease == nil {
		t.Fatalf("partial preparation: leases=%v err=%v", leases, err)
	}
	if _, err := firstLease.Bytes(); err == nil {
		t.Fatal("failed group preparation retained an earlier channel credential")
	}
	if repository.comparison.Meta().ID != "" || len(targets.activated) != 0 {
		t.Fatal("failed preparation changed execution state")
	}
}

func TestComparisonPreparationRejectsCaseChangesBetweenTargetsAndClosesAllLeases(t *testing.T) {
	fixture := newComparisonFixture(t)
	repository := &comparisonRepository{fixture: fixture}
	targets := &comparisonRunner{fixture: fixture, repository: repository}
	issued := []*credentials.Lease{}
	preparer, err := comparisons.NewPreparer(comparisons.PreparationDependencies{
		Repository: readOnlyComparisonRepository{repository: repository},
		Targets: comparisonTargetBuilder(func(ctx context.Context, command runs.StartCommand) (
			runs.PreparedRun, *credentials.Lease, error,
		) {
			input, lease, err := targets.BuildTarget(ctx, command)
			issued = append(issued, lease)
			if len(issued) == 2 {
				entry := &input.Snapshot.Entries[0]
				entry.Cases[0].Revision++
				entry.CaseDefinitions[0].Revision++
				entry.CaseDefinitions[0].Definitions[fixture.plan.Protocol] = json.RawMessage(
					`{"request":{"body":{"messages":[{"role":"user","content":"different request"}]}},` +
						`"inputs":{},"assertions":[]}`,
				)
				if _, err := domain.NewRun(input.Meta, input.PlanID, input.Snapshot); err != nil {
					t.Fatalf("changed snapshot must remain independently valid: %v", err)
				}
			}
			return input, lease, err
		}),
		Clock: &comparisonClock{now: fixture.now},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = preparer.Build(context.Background(), comparisons.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID,
		ChannelIDs: []string{fixture.channels[0].ID, fixture.channels[1].ID},
	})
	if !errors.Is(err, comparisons.ErrNotReady) || len(issued) != 2 {
		t.Fatalf("mixed Case revisions accepted: err=%v leases=%d", err, len(issued))
	}
	for _, lease := range issued {
		if _, err := lease.Bytes(); err == nil {
			t.Fatal("rejected comparison retained a channel credential")
		}
	}
}
