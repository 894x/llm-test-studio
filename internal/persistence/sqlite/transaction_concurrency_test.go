package sqlite_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestRepositoryConcurrentProjectionsAndResultWrites(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	errors := make(chan error, 100)
	var wg sync.WaitGroup
	for worker := 0; worker < 2; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < 20; i++ {
				_, err := repository.ListRunProjections(ctx)
				errors <- err
				_, err = repository.ListReportProjections(ctx)
				errors <- err
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := 0; i < 20; i++ {
			result := fixture.result
			result.ID = fmt.Sprintf("20000000-0000-4000-8000-%012d", i+1)
			result.RequestID, result.EvidenceIDs = fmt.Sprintf("request-%d", i+1), nil
			errors <- repository.AppendResult(ctx, result)
		}
	}()
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent repository operation: %v", err)
		}
	}
	transitionRun(t, repository, run, domain.RunCompleted)
	results, err := repository.ListResults(ctx, run.Meta().ID)
	if err != nil || len(results) != 20 {
		t.Fatalf("persisted results = %d, err = %v", len(results), err)
	}
}
