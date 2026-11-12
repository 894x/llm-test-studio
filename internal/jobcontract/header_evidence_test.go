package jobcontract

import (
	"encoding/json"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestEvidenceFeaturesAreNegotiatedForRunsAndComparisons(t *testing.T) {
	for _, kind := range []Kind{KindRun, KindComparison} {
		t.Run(string(kind), func(t *testing.T) {
			spec := comparisonSpec(t, 2)
			if kind == KindRun {
				run := preparedRunFixture(t)
				spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
			}
			spec.HTTPHeaderEvidence = true
			assertConfiguredFeatureRequired(t, spec, FeatureHTTPHeaderEvidence)
			spec.HTTPHeaderEvidence = false
			for _, run := range spec.PreparedRuns() {
				run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
					`{"inputs":{},"request":{"body":{}},"assertions":[{"id":"ttl","failure_status":"indeterminate","any":[{"id":"exists","source":"response","pointer":"/ttl","operator":"exists"}]}]}`,
				)
			}
			assertConfiguredFeatureRequired(t, spec, FeatureAssertionFailureStatus)
		})
	}
}
