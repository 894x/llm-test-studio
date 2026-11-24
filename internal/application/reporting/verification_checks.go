package reporting

import (
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// VerificationCheck is a projection of an authored top-level assertion group.
// It uses recorded outcomes and never reevaluates an older report's assertions.
type VerificationCheck struct {
	ID           string                     `json:"id"`
	Verification domain.VerificationSummary `json:"verification"`
	PresenceOnly bool                       `json:"presence_only,omitempty"`
}

func cacheVerificationChecks(results []domain.Result) []VerificationCheck {
	if len(results) == 0 {
		return nil
	}
	checks := []VerificationCheck{}
	for _, id := range []string{"cache_io", "cache_ttl", "cache_msh_headers"} {
		projected := make([]domain.Result, 0, len(results))
		presenceOnly := false
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
				presenceOnly = presenceOnly || legacyMSHCheck(assertion)
				found = true
				break
			}
			if !found {
				projected = nil
				break
			}
		}
		if len(projected) == 0 {
			if id == "cache_msh_headers" {
				return checks
			}
			return nil
		}
		checks = append(checks, VerificationCheck{ID: id, Verification: domain.SummarizeVerification(projected), PresenceOnly: presenceOnly})
	}
	return checks
}

func legacyMSHCheck(assertion testspec.AssertionResult) bool {
	if assertion.ID != "cache_msh_headers" || len(assertion.Children) == 0 {
		return false
	}
	for _, child := range assertion.Children {
		if !strings.HasPrefix(child.ID, "msh_") || !strings.HasSuffix(child.ID, "_exists") {
			return false
		}
	}
	return true
}
