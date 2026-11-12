package runs

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestAuthoredEvidenceWarningDoesNotBecomeAProtocolFailure(t *testing.T) {
	result := testspec.Result{Verdict: testspec.Verdict{Status: testspec.VerdictIndeterminate,
		Assertions: []testspec.AssertionResult{{ID: "cache_ttl", Status: testspec.VerdictIndeterminate,
			Children: []testspec.AssertionResult{{ID: "evidence", Status: testspec.VerdictIndeterminate, Reason: "evidence_unavailable"}},
		}},
	}}
	draft := draftFromProtocol("case", result)
	if draft.ExecutionStatus != domain.ExecutionCompleted || draft.ErrorCode != "" || draft.Failure != "" {
		t.Fatalf("evidence warning became an execution failure: %+v", draft)
	}
	result.Verdict.Assertions[0].Children[0].Reason = "source_unavailable"
	if draft := draftFromProtocol("case", result); draft.ErrorCode != "assertion_unavailable" {
		t.Fatal("ordinary unavailable assertions changed behavior")
	}
}
