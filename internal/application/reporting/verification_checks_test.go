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
	for i := range results {
		status := testspec.VerdictPassed
		if i == 1 {
			status = testspec.VerdictFailed
		}
		results[i].Verification.Assertions = append(results[i].Verification.Assertions, testspec.AssertionResult{
			ID: "cache_msh_headers", Status: status,
		})
	}
	checks = cacheVerificationChecks(results)
	if len(checks) != 3 || checks[2].ID != "cache_msh_headers" ||
		checks[2].Verification.Failed != 1 || checks[2].Verification.Passed != 1 {
		t.Fatalf("unexpected MSH check: %+v", checks)
	}
	if labels := htmlVerificationChecks(checks, "zh-CN"); labels[2].Label != "MSH 响应头失败" || labels[2].Class != "fail" {
		t.Fatalf("unexpected exported MSH check: %+v", labels[2])
	}
	if results[0].Verification.Status != testspec.VerdictIndeterminate {
		t.Fatal("projection changed the sealed outcome")
	}
	results[0].Verification.Assertions = []testspec.AssertionResult{}
	if checks := cacheVerificationChecks(results); checks != nil {
		t.Fatal("older reports without authored groups must not get inferred checks")
	}
}

func TestLegacyMSHReportDescribesPresenceWithoutRewritingVerdict(t *testing.T) {
	result := domain.Result{Verification: testspec.Verdict{
		Status: testspec.VerdictFailed,
		Assertions: []testspec.AssertionResult{
			{ID: "cache_io", Status: testspec.VerdictFailed},
			{ID: "cache_ttl", Status: testspec.VerdictFailed},
			{ID: "cache_msh_headers", Status: testspec.VerdictPassed, Children: []testspec.AssertionResult{
				{ID: "msh_0_header_5m_exists", Status: testspec.VerdictPassed},
				{ID: "msh_0_header_1h_exists", Status: testspec.VerdictPassed},
			}},
		},
	}}
	checks := cacheVerificationChecks([]domain.Result{result})
	if len(checks) != 3 || !checks[2].PresenceOnly || checks[2].Verification.Status != testspec.VerdictPassed {
		t.Fatalf("legacy projection: %+v", checks)
	}
	if label := htmlVerificationChecks(checks, "zh-CN")[2]; label.Label != "MSH 头齐全（旧规则）" || label.Class != "muted" {
		t.Fatalf("legacy badge: %+v", label)
	}
}
