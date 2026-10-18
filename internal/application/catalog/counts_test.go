package catalog

import (
	"context"
	"errors"
	"testing"
)

type countedRepository struct {
	*fakeRepository
	countErr error
}

func (repository countedRepository) CountAuthored(context.Context) (int, int, int, error) {
	return 150, 20, 3, repository.countErr
}

func TestCountsUsesMetadataProjectionAndPropagatesFailures(t *testing.T) {
	repository := countedRepository{fakeRepository: validRepository()}
	service, err := New(Dependencies{Repository: repository, Clock: &stubClock{now: fixtureTime()}})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := service.Counts(t.Context())
	if err != nil || counts.TestCases != 150 || counts.Suites != 20 || counts.Plans != 3 {
		t.Fatalf("counts: %+v %v", counts, err)
	}
	repository.countErr = ErrCorrupt
	service.repository = repository
	if _, err := service.Counts(t.Context()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("error lost: %v", err)
	}
}
