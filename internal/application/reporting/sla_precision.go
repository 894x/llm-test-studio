package reporting

import (
	"github.com/894x/llm-test-studio/internal/domain"
)

func usesRoundedLatencySLA(results []domain.Result) bool {
	if len(results) == 0 {
		return false
	}
	for _, result := range results {
		if result.Dimensions[domain.SLAPrecisionDimension] != domain.SLALatencyTwoDecimalsV1 {
			// Unmarked historical results, including runs whose report was not yet
			// generated, must retain their original comparison semantics.
			return false
		}
	}
	return true
}
