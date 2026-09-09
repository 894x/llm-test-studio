package workspace

import (
	"errors"

	"github.com/894x/llm-test-studio/internal/domain"
)

// A result represents one complete Case execution, including its protocol workflow.
func snapshotRequestBudget(snapshot domain.RunSnapshot) uint64 {
	var total uint64
	for _, entry := range snapshot.Entries {
		total += uint64(entry.WarmupCount)
		if entry.Load.Mode == domain.LoadSingle {
			total += uint64(len(entry.Cases))
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
	for _, suite := range projection.Run.Snapshot().Entries {
		planned[suite.EntryID] = uint64(len(suite.Cases))
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
	suites := projection.Run.Snapshot().Entries
	progress := make([]EntryProgress, 0, len(suites))
	currentAssigned := false
	for _, suite := range suites {
		result := results[suite.EntryID]
		status := "not_started"
		switch {
		case result.Status != "":
			status = string(result.Status)
		case projection.Run.Status() == domain.RunCompleted:
			status = "completed"
		case !currentAssigned:
			switch projection.Run.Status() {
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
			CaseCount: uint64(len(suite.Cases)), ObservedCaseCount: result.ObservedCases, Status: status,
		})
	}
	return progress
}
