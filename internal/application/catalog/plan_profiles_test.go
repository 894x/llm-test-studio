package catalog

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPerformancePlanCatalogSerializesEmptyParametersAsObject(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	command := validCreatePlanCommand("pressure")
	command.Entries = nil
	command.Performance = &domain.PerformancePlanProfile{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: "constant", WorkloadMode: "fixed",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 1000, InputTokens: 8, OutputTokens: 1,
	}
	if _, err := service.CreatePlan(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	plan := repository.createdPlan
	plan.Entries[0].Parameters = nil // Published legacy plans and built-in presets omit parameters.
	repository.plans = []domain.Plan{plan}
	snapshot, err := service.Snapshot(t.Context(), ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot.Plans[0].Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &entry); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"parameters", "sla_thresholds"} {
		if string(entry[field]) != "{}" {
			t.Fatalf("desktop catalog %s must be an empty object, got %s", field, entry[field])
		}
	}
	if repository.plans[0].Entries[0].Parameters != nil {
		t.Fatal("catalog serialization mutated the stored plan")
	}
}

func TestLegacyPerformanceCommandPreservesItemIdentityAndRejectsMixedReplacement(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	profile := &domain.PerformancePlanProfile{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: "constant", WorkloadMode: "fixed",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 1000, InputTokens: 8, OutputTokens: 1,
	}
	command := validCreatePlanCommand("pressure")
	command.Entries, command.Performance = nil, profile
	if _, err := service.CreatePlan(t.Context(), command); err != nil {
		t.Fatal(err)
	}
	created := repository.createdPlan
	repository.plans = []domain.Plan{created}
	update := UpdatePlanCommand{ID: created.ID, ExpectedRevision: created.Revision,
		Name: "renamed pressure", Protocol: created.Protocol, Performance: profile}
	if _, err := service.UpdatePlan(t.Context(), update); err != nil {
		t.Fatal(err)
	}
	if repository.updatedPlan.Entries[0].EntryID != created.Entries[0].EntryID {
		t.Fatal("legacy update replaced item identity")
	}
	second := created.Entries[0]
	second.EntryID, second.TargetID = planEntryID, planEntryID
	repository.plans[0].Entries = append(repository.plans[0].Entries, second)
	if _, err := service.UpdatePlan(t.Context(), update); err != ErrInvalid {
		t.Fatalf("legacy command must not overwrite mixed items: %v", err)
	}
}
