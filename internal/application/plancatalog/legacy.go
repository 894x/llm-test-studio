package plancatalog

import "github.com/894x/llm-test-studio/internal/domain"

// Version 1 used a plan-wide performance profile. Its deterministic item ID
// preserves identity across repeated reads without rewriting the original file.
func legacyPerformanceEntry(id, name string, profile domain.PerformancePlanProfile) domain.PlanEntry {
	return domain.PlanEntry{
		EntryID: id, TargetID: id, Name: name, TargetKind: domain.PlanTargetPerformance,
		OnFailure: "continue", Performance: &profile,
		Load: profile.LoadProfile(),
	}
}
