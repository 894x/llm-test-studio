package runs

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestCaseSummaryInputOwnsMutableExecutorData(t *testing.T) {
	draft := ResultDraft{
		ExecutionStatus: domain.ExecutionCompleted,
		Verification:    testspec.Verdict{Status: testspec.VerdictPassed},
		Dimensions:      map[string]string{"phase": "measured"},
		Metrics:         map[string]float64{"latency_ms": 12},
		EvidenceIDs:     []string{"original-evidence"},
	}
	input := summaryInput(draft)
	draft.Dimensions["phase"] = "warmup"
	draft.Metrics["latency_ms"] = 999
	draft.EvidenceIDs[0] = "changed-evidence"
	result := aggregateCaseResult(
		domain.EntityMeta{},
		"run",
		"entry",
		"case",
		[]caseSummaryInput{input},
	)
	if result.Metrics["execution_count"] != 1 || result.Metrics["latency_ms"] != 12 {
		t.Fatalf("executor mutations changed summary metrics: %v", result.Metrics)
	}
	if len(result.EvidenceIDs) != 1 || result.EvidenceIDs[0] != "original-evidence" {
		t.Fatalf("executor mutations changed summary evidence: %v", result.EvidenceIDs)
	}
	if result.Verification.Status != testspec.VerdictPassed {
		t.Fatalf("summary verification = %s", result.Verification.Status)
	}
}
