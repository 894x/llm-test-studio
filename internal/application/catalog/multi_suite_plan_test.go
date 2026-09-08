package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCreatePlanBuildsOrderedEntriesAndResolvesQuickTestDefaults(t *testing.T) {
	repository := validRepository()
	repository.testCases[0].Definition.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"default"}]}},"expected":{"allowed_http_statuses":[200],"stream_completion":"required"},"assertions":[{"kind":"text","config":{"contains":"ok"}}]}`)
	repository.suites[0].QuickTest = &domain.SuiteQuickTest{
		Description: "Prompt", TimeoutMS: 30_000,
		Inputs: []domain.SuiteInput{{
			Key: "prompt", Label: "Prompt", Type: "text", Default: json.RawMessage(`"default"`),
			Bindings: []domain.SuiteInputBinding{{CaseKey: "T001", Pointer: "/request/body/messages/0/content"}},
		}},
	}
	const (
		createdPlanID = "84000000-0000-4000-8000-000000000001"
	)
	service := newTestServiceWithFactory(
		t, repository, fixtureTime(), sequentialMetaFactory([]string{createdPlanID}),
	)
	profile := PlanSuiteInput{
		SuiteID: suiteID, SuiteRevision: 1,
		LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000,
		SLAThresholds: map[string]float64{"p95_ms": 1_500}, Parameters: map[string]json.RawMessage{},
	}
	second := profile
	second.Parameters = map[string]json.RawMessage{"prompt": json.RawMessage(`"second"`)}
	second.RequestCount = 2

	_, err := service.CreatePlan(context.Background(), CreatePlanCommand{
		Name: "ordered", ModelIDs: []string{modelBID}, ChannelIDs: []string{channelID},
		Suites: []PlanSuiteInput{profile, second},
	})
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if len(repository.createdPlan.Suites) != 2 || !domain.IsUUID(repository.createdPlan.Suites[0].EntryID) ||
		!domain.IsUUID(repository.createdPlan.Suites[1].EntryID) || repository.createdPlan.Suites[0].EntryID == repository.createdPlan.Suites[1].EntryID ||
		repository.createdPlan.Suites[1].Load.RequestCount != 2 {
		t.Fatalf("created ordered suites = %#v", repository.createdPlan.Suites)
	}
	if got := string(repository.createdPlan.Suites[0].Parameters["prompt"]); got != `"default"` {
		t.Fatalf("resolved default parameter = %s", got)
	}
	if len(repository.createdPlan.Suites[0].Cases) != 1 || repository.createdPlan.Suites[0].Cases[0].CaseID != caseID {
		t.Fatalf("derived cases = %#v", repository.createdPlan.Suites[0].Cases)
	}
	repository.plans = []domain.Plan{repository.createdPlan}
	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if len(snapshot.Plans) != 1 || snapshot.Plans[0].SuiteCount != 2 || snapshot.Plans[0].CaseCount != 2 ||
		len(snapshot.Plans[0].Suites) != 2 || snapshot.Plans[0].Suites[0].SuiteKey != repository.suites[0].Key ||
		snapshot.Plans[0].Suites[0].Protocol != repository.suites[0].Protocol ||
		snapshot.Plans[0].Suites[0].ModelTarget != repository.suites[0].ModelTarget ||
		snapshot.Plans[0].Suites[0].CaseCount != 1 || snapshot.Plans[0].Suites[0].QuickTest == nil {
		t.Fatalf("Plan summary = %#v", snapshot.Plans)
	}
}

func TestPlanSuiteEntryIDsAreServerOwnedAndStableAcrossUpdates(t *testing.T) {
	t.Run("update preserves an existing entry id", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validUpdatePlanCommand(planID, "updated")
		command.Suites[0].EntryID = planEntryID

		if _, err := service.UpdatePlan(context.Background(), command); err != nil {
			t.Fatalf("UpdatePlan() error = %v", err)
		}
		if got := repository.updatedPlan.Suites[0].EntryID; got != planEntryID {
			t.Fatalf("updated entry id = %q, want %q", got, planEntryID)
		}
	})

	t.Run("create rejects a caller supplied entry id", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validCreatePlanCommand("created")
		command.Suites[0].EntryID = planEntryID

		if _, err := service.CreatePlan(context.Background(), command); err != ErrInvalid {
			t.Fatalf("CreatePlan() error = %v, want %v", err, ErrInvalid)
		}
	})

	t.Run("update rejects an entry id from another plan", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validUpdatePlanCommand(planID, "updated")
		command.Suites[0].EntryID = "79797979-7979-4979-8979-797979797979"

		if _, err := service.UpdatePlan(context.Background(), command); err != ErrInvalid {
			t.Fatalf("UpdatePlan() error = %v, want %v", err, ErrInvalid)
		}
	})
}

func TestTargetedPlanRequiresEverySuiteModelTargetToMatchEveryBinding(t *testing.T) {
	repository := validRepository()
	repository.mappings[1].UpstreamModelName = "zulu-upstream"
	service := newTestService(t, repository, fixtureTime())
	command := validCreatePlanCommand("target mismatch")
	command.ModelIDs = []string{modelAID}

	if _, err := service.CreatePlan(context.Background(), command); err != ErrInvalid {
		t.Fatalf("CreatePlan() target mismatch error = %v, want %v", err, ErrInvalid)
	}

	command.ModelIDs, command.ChannelIDs = []string{}, []string{}
	if _, err := service.CreatePlan(context.Background(), command); err != nil {
		t.Fatalf("CreatePlan() targetless error = %v", err)
	}

	conflictingSuite := repository.suites[0]
	conflictingSuite.EntityMeta.ID = "84000000-0000-4000-8000-000000000002"
	conflictingSuite.Key = "different-target"
	conflictingSuite.ModelTarget = "another-upstream"
	repository.suites = append(repository.suites, conflictingSuite)
	conflicting := command.Suites[0]
	conflicting.SuiteID = conflictingSuite.ID
	command.Suites = append(command.Suites, conflicting)
	if _, err := service.CreatePlan(context.Background(), command); err != ErrInvalid {
		t.Fatalf("CreatePlan() conflicting targetless suites error = %v, want %v", err, ErrInvalid)
	}
}
