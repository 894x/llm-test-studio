package catalog

import (
	"context"
	"github.com/894x/llm-test-studio/internal/domain"
	"testing"
)

func TestCreatePlanKeepsRepeatedSuiteAndDirectCaseReferences(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	command := validCreatePlanCommand("mixed entries")
	repeated := command.Entries[0]
	direct := command.Entries[0]
	direct.TargetKind = domain.PlanTargetCase
	direct.TargetID = caseID
	command.Entries = append(command.Entries, repeated, direct)
	if _, err := service.CreatePlan(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if len(repository.createdPlan.Entries) != 3 {
		t.Fatal("entries were deduplicated")
	}
	seen := map[string]bool{}
	for _, entry := range repository.createdPlan.Entries {
		if !domain.IsUUID(entry.EntryID) || seen[entry.EntryID] {
			t.Fatal("invalid entry identity")
		}
		seen[entry.EntryID] = true
	}
}

func TestPlanDefersReferencedProtocolAndInputChecksUntilRun(t *testing.T) {
	repository := validRepository()
	repository.suites = nil
	repository.testCases = nil
	service := newTestService(t, repository, fixtureTime())
	if _, err := service.CreatePlan(context.Background(), validCreatePlanCommand("unresolved")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Snapshot(context.Background()); err != nil {
		t.Fatalf("catalog cannot display unresolved references: %v", err)
	}
}

func TestPlanSuiteEntryIDsAreServerOwnedAndStableAcrossUpdates(t *testing.T) {
	t.Run("update preserves an existing entry id", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validUpdatePlanCommand(planID, "updated")
		command.Entries[0].EntryID = planEntryID

		if _, err := service.UpdatePlan(context.Background(), command); err != nil {
			t.Fatalf("UpdatePlan() error = %v", err)
		}
		if got := repository.updatedPlan.Entries[0].EntryID; got != planEntryID {
			t.Fatalf("updated entry id = %q, want %q", got, planEntryID)
		}
	})

	t.Run("create rejects a caller supplied entry id", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validCreatePlanCommand("created")
		command.Entries[0].EntryID = planEntryID

		if _, err := service.CreatePlan(context.Background(), command); err != ErrInvalid {
			t.Fatalf("CreatePlan() error = %v, want %v", err, ErrInvalid)
		}
	})

	t.Run("update rejects an entry id from another plan", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime())
		command := validUpdatePlanCommand(planID, "updated")
		command.Entries[0].EntryID = "79797979-7979-4979-8979-797979797979"

		if _, err := service.UpdatePlan(context.Background(), command); err != ErrInvalid {
			t.Fatalf("UpdatePlan() error = %v, want %v", err, ErrInvalid)
		}
	})
}
