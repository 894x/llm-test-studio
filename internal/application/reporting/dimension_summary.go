package reporting

import (
	"sort"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// DimensionStatistic counts each frozen (entry, case) once, independently of load.
type DimensionStatistic struct {
	Dimension              string   `json:"dimension"`
	CaseCount              uint64   `json:"case_count"`
	PassedCaseCount        uint64   `json:"passed_case_count"`
	FailedCaseCount        uint64   `json:"failed_case_count"`
	ObservedCaseCount      uint64   `json:"observed_case_count"`
	IndeterminateCaseCount uint64   `json:"indeterminate_case_count"`
	NotRunCaseCount        uint64   `json:"not_run_case_count"`
	PassRatePercent        *float64 `json:"pass_rate_percent"`
}

func summarizeDimensions(entries []EntryDetail) []DimensionStatistic {
	groups := make(map[string]*DimensionStatistic)
	for _, entry := range entries {
		if entry.TargetKind == domain.PlanTargetPerformance {
			continue
		}
		for _, testCase := range entry.Cases {
			group := groups[testCase.Dimension]
			if group == nil {
				group = &DimensionStatistic{Dimension: testCase.Dimension}
				groups[testCase.Dimension] = group
			}
			group.CaseCount++
			verification := testCase.Verification
			measured := verification.Passed + verification.Failed + verification.Observed + verification.Indeterminate
			switch {
			case measured == 0:
				group.NotRunCaseCount++
			case verification.Status == testspec.VerdictPassed:
				group.PassedCaseCount++
			case verification.Status == testspec.VerdictFailed:
				group.FailedCaseCount++
			case verification.Status == testspec.VerdictIndeterminate:
				group.IndeterminateCaseCount++
			case verification.Status == testspec.VerdictNotApplicable:
				group.ObservedCaseCount++
			}
		}
	}

	statistics := make([]DimensionStatistic, 0, len(groups))
	for _, group := range groups {
		verified := group.PassedCaseCount + group.FailedCaseCount
		if verified > 0 {
			rate := 100 * float64(group.PassedCaseCount) / float64(verified)
			group.PassRatePercent = &rate
		}
		statistics = append(statistics, *group)
	}
	sort.Slice(statistics, func(left, right int) bool {
		return statistics[left].Dimension < statistics[right].Dimension
	})
	return statistics
}
