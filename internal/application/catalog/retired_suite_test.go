package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

type retiredSuiteRepository struct {
	*fakeRepository
	retired domain.Suite
	lookup  error
}

func (repository retiredSuiteRepository) ListSuites(context.Context) ([]domain.Suite, error) {
	result := []domain.Suite{}
	for _, suite := range repository.suites {
		if suite.ID != repository.retired.ID {
			result = append(result, suite)
		}
	}
	return result, nil
}

func (repository retiredSuiteRepository) GetSuite(ctx context.Context, id string) (domain.Suite, error) {
	if repository.lookup != nil {
		return domain.Suite{}, repository.lookup
	}
	return repository.fakeRepository.GetSuite(ctx, id)
}

func TestSnapshotResolvesRetiredPlanTargetsWithoutListingTheSuite(t *testing.T) {
	base := validRepository()
	retired := base.suites[0]
	base.plans[0].Entries[0].TargetKind = domain.PlanTargetSuite
	base.plans[0].Entries[0].TargetID = retired.ID
	repository := retiredSuiteRepository{fakeRepository: base, retired: retired}
	service := newTestService(t, repository, fixtureTime())
	snapshot, err := service.Snapshot(context.Background(), ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	for _, suite := range snapshot.Suites {
		if suite.ID == retired.ID {
			t.Fatal("retired suite leaked into selectable entries")
		}
	}
	entry := snapshot.Plans[0].Entries[0]
	if entry.TargetID != retired.ID || entry.TargetKey != retired.Key ||
		entry.TargetName != retired.Name || entry.CaseCount != len(retired.Cases) {
		t.Fatalf("saved plan lost target metadata: %+v", entry)
	}
	repository.lookup = ErrNotFound
	service = newTestService(t, repository, fixtureTime())
	if _, err := service.Snapshot(context.Background(), ScopeAll); err != nil {
		t.Fatalf("ordinary missing targets no longer remain editable: %v", err)
	}
	repository.lookup = ErrCorrupt
	service = newTestService(t, repository, fixtureTime())
	if _, err := service.Snapshot(context.Background(), ScopeAll); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corrupt compatibility lookup was ignored: %v", err)
	}
}
