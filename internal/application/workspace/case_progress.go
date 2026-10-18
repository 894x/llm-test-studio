package workspace

import (
	"errors"

	"github.com/894x/llm-test-studio/internal/domain"
)

// A result represents one complete Case execution, including its protocol workflow.
func entryRequestBudget(entries []RunEntryProjection) uint64 {
	var total uint64
	for _, entry := range entries {
		// Workspace result counters describe measured executions only.
		if entry.Load.Mode == domain.LoadSingle {
			total += entry.CaseCount
			continue
		}
		if entry.Load.RequestCount == 0 {
			return 0
		}
		total += entry.Load.RequestCount
	}
	return total
}

func validateEntryResults(projection RunProjection) error {
	planned := map[string]uint64{}
	for _, suite := range projection.Entries {
		planned[suite.EntryID] = suite.CaseCount
	}
	seen := map[string]bool{}
	for _, result := range projection.EntryResults {
		count, found := planned[result.EntryID]
		if !found || seen[result.EntryID] || result.ObservedCases > count {
			return errors.New("invalid Suite Case progress ownership or count")
		}
		if result.Status != "" && result.Status.Validate() != nil {
			return errors.New("invalid Suite execution progress status")
		}
		seen[result.EntryID] = true
	}
	return nil
}

func summarizeEntryProgress(projection RunProjection) []EntryProgress {
	results := map[string]EntryResultProjection{}
	for _, result := range projection.EntryResults {
		results[result.EntryID] = result
	}
	suites := projection.Entries
	progress := make([]EntryProgress, 0, len(suites))
	// A failure-policy stop happens after the failed item's completion marker.
	// Later items were never started and must not inherit the run's failure.
	currentAssigned := projection.Failure != nil && projection.Failure.ErrorCode == "plan_item_failed"
	for _, suite := range suites {
		result := results[suite.EntryID]
		status := "not_started"
		switch {
		case result.Status != "":
			status = string(result.Status)
		case projection.Status == domain.RunCompleted:
			status = "completed"
		case !currentAssigned:
			switch projection.Status {
			case domain.RunQueued, domain.RunStarting:
				status = "queued"
			case domain.RunRunning, domain.RunDraining:
				status = "running"
			case domain.RunFailed:
				status = "failed"
			case domain.RunCancelled:
				status = "cancelled"
			}
			currentAssigned = true
		}
		progress = append(progress, EntryProgress{
			EntryID: suite.EntryID, Name: suite.Name,
			CaseCount: suite.CaseCount, ObservedCaseCount: result.ObservedCases, Status: status,
		})
	}
	return progress
}
