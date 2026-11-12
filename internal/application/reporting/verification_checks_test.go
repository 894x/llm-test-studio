package reporting

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestCacheCheckProjectionAggregatesRecordedGroupsWithoutReevaluation(t *testing.T) {
	request := func(io, ttl testspec.VerdictStatus, phase string) domain.Result {
		return domain.Result{Dimensions: map[string]string{"phase": phase}, Verification: testspec.Verdict{
			Status:     testspec.VerdictIndeterminate,
			Assertions: []testspec.AssertionResult{{ID: "cache_io", Status: io}, {ID: "cache_ttl", Status: ttl}},
		}}
	}
	results := []domain.Result{
		request(testspec.VerdictPassed, testspec.VerdictPassed, "measure"),
		request(testspec.VerdictPassed, testspec.VerdictIndeterminate, "measure"),
		request(testspec.VerdictFailed, testspec.VerdictFailed, "warmup"),
	}
	checks := cacheVerificationChecks(results)
	if len(checks) != 2 || checks[0].Verification.Status != testspec.VerdictPassed ||
		checks[0].Verification.Passed != 2 || checks[1].Verification.Status != testspec.VerdictIndeterminate ||
		checks[1].Verification.Indeterminate != 1 || checks[1].Verification.Failed != 0 {
		t.Fatalf("unexpected checks: %+v", checks)
	}
	if results[0].Verification.Status != testspec.VerdictIndeterminate {
		t.Fatal("projection changed the sealed outcome")
	}
	results[0].Verification.Assertions = []testspec.AssertionResult{}
	if checks := cacheVerificationChecks(results); checks != nil {
		t.Fatal("older reports without authored groups must not get inferred checks")
	}
}
