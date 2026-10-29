package catalog

import (
	"context"
	"testing"
)

func TestCaseDescriptionsSurviveCreateUpdateAndCatalogSummary(t *testing.T) {
	ctx := context.Background()
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	command := validCreateTestCaseCommand("Described case")
	command.Description = "Purpose: verify response.\nPass: authored assertions succeed."
	if _, err := service.CreateTestCase(ctx, command); err != nil {
		t.Fatal(err)
	}
	if repository.createdTestCase.Description != command.Description {
		t.Fatal("create dropped the authored explanation")
	}
	current := repository.testCases[0]
	update := UpdateTestCaseCommand{
		ID: current.ID, ExpectedRevision: current.Revision, Key: current.Key, Name: current.Name,
		Description: "Purpose: revised context.\nLimits: single request.", Dimension: current.Dimension,
		Enabled: current.Enabled, Default: current.Default, Severity: current.Severity,
		ExecutionMode: current.ExecutionMode, Definitions: current.Definitions,
	}
	if _, err := service.UpdateTestCase(ctx, update); err != nil {
		t.Fatal(err)
	}
	if repository.updatedTestCase.Description != update.Description {
		t.Fatal("update dropped the authored explanation")
	}
	repository.testCases[0] = repository.updatedTestCase
	summaries, err := service.ListTestCases(ctx)
	if err != nil || len(summaries) != 1 || summaries[0].Description != update.Description {
		t.Fatalf("catalog summary lost the authored explanation: %#v, %v", summaries, err)
	}
	update.ExpectedRevision = repository.updatedTestCase.Revision
	update.Description = ""
	if _, err := service.UpdateTestCase(ctx, update); err != nil {
		t.Fatal(err)
	}
	if repository.updatedTestCase.Description != "" {
		t.Fatal("clearing the explanation retained obsolete text")
	}
}
