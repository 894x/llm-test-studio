package catalog

import "github.com/894x/llm-test-studio/internal/domain"

// The quick-performance picker accepts single-profile presets. Mixed plans
// must execute through the normal plan launcher and cannot become a preset.
func singlePerformanceProfile(plan domain.Plan) *domain.PerformancePlanProfile {
	if len(plan.Entries) == 1 {
		return clonePerformancePlanProfile(plan.Entries[0].Performance)
	}
	return nil
}
