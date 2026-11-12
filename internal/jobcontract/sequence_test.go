package jobcontract

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestSequenceControlsRequireUpdatedRunnerForRunsAndComparisons(t *testing.T) {
	for _, step := range []string{
		`{"id":"next","request":{"body":{}},"delay_ms":360000}`,
		`{"id":"next","reuse_request_from":"initial"}`,
	} {
		for _, kind := range []Kind{KindRun, KindComparison} {
			t.Run(string(kind)+step, func(t *testing.T) {
				spec := comparisonSpec(t, 2)
				if kind == KindRun {
					run := preparedRunFixture(t)
					spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
				}
				legacy := completeTaskCapabilities()
				legacy.Features = slices.DeleteFunc(legacy.Features, func(value string) bool {
					return value == FeatureSequenceSteps
				})
				if !legacy.SupportsJob(spec) {
					t.Fatal("ordinary task unexpectedly requires sequence controls")
				}
				for _, run := range spec.PreparedRuns() {
					testCase := &run.Snapshot.Entries[0].CaseDefinitions[0]
					testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
						`{"inputs":{},"request":{"body":{}},"assertions":[],"workflow":{"mode":"sequence","steps":[` + step + `]}}`,
					)
				}
				assertConfiguredFeatureRequired(t, spec, FeatureSequenceSteps)
			})
		}
	}
}
