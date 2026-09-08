package workspace

import (
	"errors"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

// A profile is a driver input, not necessarily a budget for the whole Suite.
// Unknown/variable observation counts have no request denominator.
func snapshotRequestBudget(snapshot domain.RunSnapshot) uint64 {
	if snapshot.QuickTask != nil {
		return 0
	}
	var total uint64
	for _, suite := range snapshot.Suites {
		groups := map[domain.CaseType]bool{}
		for _, testCase := range suite.CaseDefinitions {
			caseType := testCase.Definition.Type
			switch caseType {
			case casetypes.TypeLegacyAPIAudit:
				total++
			case casetypes.TypeRequestSingle, casetypes.TypeResponseProbe:
				if suite.Load.RequestCount == 0 {
					return 0
				}
				if !groups[caseType] {
					total += suite.Load.RequestCount
					groups[caseType] = true
				}
			default:
				return 0
			}
		}
	}
	return total
}

func progressSuites(run domain.Run) []domain.RunSuiteSnapshot {
	snapshot := run.Snapshot()
	if snapshot.QuickTask != nil {
		return []domain.RunSuiteSnapshot{{
			Suite: snapshot.QuickTask.Suite, Cases: snapshot.Cases,
		}}
	}
	return snapshot.Suites
}

func validateSuiteResults(projection RunProjection) error {
	planned := map[string]uint64{}
	for _, suite := range progressSuites(projection.Run) {
		planned[suite.EntryID] = uint64(len(suite.Cases))
	}
	seen := map[string]bool{}
	for _, result := range projection.SuiteResults {
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

func summarizeSuiteProgress(projection RunProjection) []SuiteProgress {
	results := map[string]SuiteResultProjection{}
	for _, result := range projection.SuiteResults {
		results[result.EntryID] = result
	}
	suites := progressSuites(projection.Run)
	progress := make([]SuiteProgress, 0, len(suites))
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
		entryID := suite.EntryID
		if entryID == "" {
			entryID = projection.Run.Meta().ID
		}
		progress = append(progress, SuiteProgress{
			EntryID: entryID, Name: suite.Suite.Name,
			CaseCount: uint64(len(suite.Cases)), ObservedCaseCount: result.ObservedCases, Status: status,
		})
	}
	return progress
}
