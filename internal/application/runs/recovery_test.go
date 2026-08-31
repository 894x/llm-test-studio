package runs_test

import (
	"context"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/domain"
)

func TestRecoverInterruptedCancelsDurableNonTerminalRuns(t *testing.T) {
	fixture := newRunFixture(t)
	active, err := domain.NewRun(fixture.plan.EntityMeta, fixture.plan.ID, domain.RunSnapshot{
		SchemaVersion: 1,
		Plan:          domain.EntityRevisionRef{ID: fixture.plan.ID, Revision: fixture.plan.Revision},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.model.ID, Revision: fixture.model.Revision}, Name: fixture.model.Name, Protocol: fixture.model.Protocol},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: fixture.channel.ID, Revision: fixture.channel.Revision}, Name: fixture.channel.Name, BaseURL: fixture.channel.BaseURL, Protocol: fixture.channel.Protocol, UpstreamModelName: fixture.mapping.UpstreamModelName},
		Cases:         fixture.plan.Cases, Load: fixture.plan.Load, SLA: fixture.plan.SLA, Environment: fixture.environment,
	})
	if err != nil {
		t.Fatal(err)
	}
	active, err = active.Transition(domain.RunStarting, fixture.now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	repository := &recoveryRepository{runs: []domain.Run{active}}
	if err := runs.RecoverInterrupted(context.Background(), repository, fixedRecoveryClock{now: fixture.now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if len(repository.runs) != 1 || repository.runs[0].Status() != domain.RunCancelled {
		t.Fatalf("recovered runs = %#v", repository.runs)
	}
}

type recoveryRepository struct{ runs []domain.Run }

func (repository *recoveryRepository) ListRuns(context.Context) ([]domain.Run, error) {
	return append([]domain.Run(nil), repository.runs...), nil
}
func (repository *recoveryRepository) UpdateRun(_ context.Context, expected uint64, updated domain.Run) error {
	for index := range repository.runs {
		if repository.runs[index].Meta().ID == updated.Meta().ID && repository.runs[index].Meta().Revision == expected {
			repository.runs[index] = updated
			return nil
		}
	}
	return context.Canceled
}

type fixedRecoveryClock struct{ now time.Time }

func (clock fixedRecoveryClock) Now() time.Time { return clock.now }
