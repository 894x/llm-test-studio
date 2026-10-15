package catalog

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSnapshotReadsOnlyRequestedCatalogScope(t *testing.T) {
	for _, test := range []struct {
		scope Scope
		reads map[string]int
	}{
		{scope: ScopeConnections, reads: map[string]int{"models": 1, "channels": 1, "mappings": 1}},
		{scope: ScopeDefinitions, reads: map[string]int{"test_cases": 1, "suites": 1}},
		{scope: ScopeQuickTest, reads: map[string]int{
			"models": 1, "channels": 1, "mappings": 1, "test_cases": 1, "suites": 1, "plans": 1,
		}},
		{scope: ScopeAll, reads: map[string]int{"models": 1, "channels": 1, "mappings": 1, "test_cases": 1, "suites": 1, "plans": 1}},
	} {
		t.Run(string(test.scope), func(t *testing.T) {
			repository := validRepository()
			service := newTestService(t, repository, fixtureTime())
			if _, err := service.Snapshot(context.Background(), test.scope); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(repository.listCalls, test.reads) {
				t.Fatalf("catalog reads = %v, want %v", repository.listCalls, test.reads)
			}
		})
	}
}

func TestSnapshotRejectsUnknownScopeBeforeReadingFiles(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	if _, err := service.Snapshot(context.Background(), Scope("unknown")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown scope error = %v, want ErrInvalid", err)
	}
	if len(repository.listCalls) != 0 {
		t.Fatalf("unexpected reads = %v", repository.listCalls)
	}
}
