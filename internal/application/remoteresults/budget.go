package remoteresults

import (
	"errors"

	"github.com/894x/llm-test-studio/internal/jsonbudget"
)

const (
	MaxEventCollectionItems  = 32_768
	MaxReportCollectionItems = 131_072
	// 10000 samples may contribute up to 30000 sparse time slices. Preserve
	// their complete DTOs within the independent 32 MiB archive byte budget.
	MaxPerformanceCollectionItems = 4_194_304
)

var ErrCollectionBudget = errors.New("remote result exceeds collection budget")

// CheckEventBudget bounds allocations before any typed domain decoding.
func CheckEventBudget(raw []byte) error { return checkDocumentBudget(raw, MaxEventCollectionItems) }
func CheckRunReportBudget(raw []byte) error {
	return checkDocumentBudget(raw, MaxReportCollectionItems)
}
func CheckPerformanceBudget(raw []byte) error {
	return checkDocumentBudget(raw, MaxPerformanceCollectionItems)
}

func checkDocumentBudget(raw []byte, limit int) error {
	err := jsonbudget.Check(raw, limit)
	if errors.Is(err, jsonbudget.ErrExceeded) {
		return ErrCollectionBudget
	}
	if err != nil {
		return ErrInvalid
	}
	return nil
}
