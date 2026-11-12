package reporting

import (
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// VerificationCheck is a projection of an authored top-level assertion group.
// It uses recorded outcomes and never reevaluates an older report's assertions.
type VerificationCheck struct {
	ID           string                     `json:"id"`
	Verification domain.VerificationSummary `json:"verification"`
}

func cacheVerificationChecks(results []domain.Result) []VerificationCheck {
	if len(results) == 0 {
		return nil
	}
	checks := []VerificationCheck{}
	for _, id := range []string{"cache_io", "cache_ttl"} {
		projected := make([]domain.Result, 0, len(results))
		for _, result := range results {
			if result.Dimensions["phase"] == "warmup" {
				continue
			}
			found := false
			for _, assertion := range result.Verification.Assertions {
				if assertion.ID != id {
					continue
				}
				copy := result
				copy.Verification = testspec.Verdict{Status: assertion.Status, Assertions: []testspec.AssertionResult{assertion}}
				projected = append(projected, copy)
				found = true
				break
			}
			if !found {
				return nil
			}
		}
		if len(projected) == 0 {
			return nil
		}
		checks = append(checks, VerificationCheck{ID: id, Verification: domain.SummarizeVerification(projected)})
	}
	return checks
}
