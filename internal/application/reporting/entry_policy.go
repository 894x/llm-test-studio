package reporting

import (
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// EntryFailed applies the same measured verdict and SLA rules used by reports
// before the run host decides whether to start the next plan item.
func EntryFailed(entry domain.RunEntrySnapshot, results []domain.Result) bool {
	summary := domain.SummarizeVerification(results)
	if summary.Status == testspec.VerdictFailed || summary.Status == testspec.VerdictIndeterminate {
		return true
	}
	_, issues := evaluateSLA(entry.SLA, results, aggregateMetrics(results))
	return len(issues) > 0
}
