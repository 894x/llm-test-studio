package catalog

import (
	"context"
	"testing"
)

func TestCreatePlanStoresReferencesWithoutResolvingExecution(t *testing.T) {
	repository := validRepository()
	repository.mappings = nil
	service := newTestService(t, repository, fixtureTime())
	_, err := service.CreatePlan(context.Background(), validCreatePlanCommand("reference plan"))
	if err != nil {
		t.Fatalf("saving references must not require an executable mapping: %v", err)
	}
	if repository.suiteRevisionCalls != 0 || repository.testCaseRevisionCalls != 0 {
		t.Fatalf("saving resolved %d suites and %d cases", repository.suiteRevisionCalls, repository.testCaseRevisionCalls)
	}
}
