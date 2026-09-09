package domain

import (
	"encoding/json"
	"github.com/894x/llm-test-studio/internal/testspec"
	"testing"
	"time"
)

func TestPlanValidatesOrderedSuiteEntriesAndAllowsRepeatedSuite(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	meta := EntityMeta{
		ID: "81000000-0000-4000-8000-000000000001", SchemaVersion: 1,
		Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	load := LoadProfile{Mode: LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	sla := SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}}
	suiteID := "81000000-0000-4000-8000-000000000002"
	plan := Plan{
		EntityMeta: meta,
		Name:       "ordered suites", Protocol: ProtocolOpenAIChat, Seed: 1,
		Entries: []PlanEntry{
			{
				EntryID:    "81000000-0000-4000-8000-000000000004",
				TargetKind: PlanTargetSuite, TargetID: suiteID,

				Parameters: map[string]json.RawMessage{"prompt": json.RawMessage(`"first"`)},
				Load:       load, SLA: sla,
			},
			{
				EntryID:    "81000000-0000-4000-8000-000000000005",
				TargetKind: PlanTargetSuite, TargetID: suiteID,

				Parameters: map[string]json.RawMessage{"prompt": json.RawMessage(`"second"`)},
				Load:       load, SLA: sla,
			},
		},
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	duplicate := plan
	duplicate.Entries = append([]PlanEntry(nil), plan.Entries...)
	duplicate.Entries[1].EntryID = duplicate.Entries[0].EntryID
	if err := duplicate.Validate(); err == nil {
		t.Fatal("Validate() accepted duplicate suite entry ids")
	}
}

func TestPlanRejectsLegacyFlatShapeAndDoesNotSerializeIt(t *testing.T) {
	legacy := []byte(`{
		"schema_version":1,
		"id":"81000000-0000-4000-8000-000000000001",
		"revision":1,
		"created_at":"2026-09-08T12:00:00Z",
		"updated_at":"2026-09-08T12:00:00Z",
		"name":"legacy",
		"model_ids":[],
		"channel_ids":[],
		"suite_id":"81000000-0000-4000-8000-000000000002",
		"suite_revision":1,
		"cases":[{"case_id":"81000000-0000-4000-8000-000000000003","revision":1}],
		"load":{"mode":"single","concurrency":1,"request_count":1,"duration_ms":0,"rate_per_second":0,"request_timeout_ms":1000},
		"sla":{"thresholds":{"e2e_p95_ms":1000}}
	}`)
	var plan Plan
	if err := json.Unmarshal(legacy, &plan); err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err == nil {
		t.Fatal("Plan.Validate accepted the removed flat authored-plan shape")
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"suite_id", "suite_revision", "cases", "load", "sla"} {
		if _, exists := fields[field]; exists {
			t.Fatalf("Plan JSON contains removed field %q: %s", field, encoded)
		}
	}
}

func TestResultSuiteMarkerAndRequestOwnership(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	meta := func(id string) EntityMeta {
		return EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	entryID := "82000000-0000-4000-8000-000000000001"
	runID := "82000000-0000-4000-8000-000000000002"
	caseID := "82000000-0000-4000-8000-000000000003"

	request := Result{
		EntityMeta: meta("82000000-0000-4000-8000-000000000004"),
		RunID:      runID, EntryID: entryID, CaseID: caseID, RequestID: "request-1",
		ExecutionStatus: ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("request Validate() error = %v", err)
	}

	marker := Result{
		EntityMeta: meta("82000000-0000-4000-8000-000000000005"),
		RunID:      runID, EntryID: entryID, EntryStatus: EntryExecutionCompleted,
	}
	if err := marker.Validate(); err != nil {
		t.Fatalf("marker Validate() error = %v", err)
	}
}

func TestRunSnapshotCloneOwnsMultiSuiteMutableValues(t *testing.T) {
	fixture := validRunSnapshot()
	if err := fixture.Validate(); err != nil {
		t.Fatalf("fixture Validate() error = %v", err)
	}
	run, err := NewRun(EntityMeta{
		ID: "83000000-0000-4000-8000-000000000002", SchemaVersion: 1, Revision: 1,
		CreatedAt: fixture.PlanDocument.CreatedAt, UpdatedAt: fixture.PlanDocument.UpdatedAt,
	}, fixture.Plan.ID, fixture)
	if err != nil {
		t.Fatal(err)
	}
	copy := run.Snapshot()
	copy.Entries[0].Parameters["added"] = json.RawMessage(`true`)
	copy.Entries[0].SLA.Thresholds["e2e_p95_ms"] = 1
	copy.Entries[0].CaseDefinitions[0].Name = "changed"
	copy.PlanDocument.Entries[0].TargetID = testCaseID
	actual := run.Snapshot()
	if len(actual.Entries[0].Parameters) != 0 || actual.Entries[0].SLA.Thresholds["e2e_p95_ms"] == 1 ||
		actual.Entries[0].CaseDefinitions[0].Name == "changed" ||
		actual.PlanDocument.Entries[0].TargetID != fixture.PlanDocument.Entries[0].TargetID {
		t.Fatalf("Run snapshot leaked mutable state: %#v", actual)
	}
}
