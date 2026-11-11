package jobcontract

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestJSONPrefixCapabilityCoversDirectAndNestedAssertions(t *testing.T) {
	for _, assertion := range []string{
		`{"id":"syntax","source":"text","operator":"json_prefix_valid","value":"{"}`,
		`{"id":"value","source":"text","operator":"json_prefix_equals","value":{"prefix":"{","expected":{}}}`,
		`{"id":"all","all":[{"id":"syntax","source":"text","operator":"json_prefix_valid","value":"{"}]}`,
		`{"id":"any","any":[{"id":"syntax","source":"text","operator":"json_prefix_valid","value":"{"}]}`,
		`{"id":"each","source":"requests","each":[
			{"id":"syntax","source":"item","pointer":"/text","operator":"json_prefix_valid","value":"{"}]}`,
	} {
		for _, kind := range []Kind{KindRun, KindComparison} {
			t.Run(string(kind)+"/"+assertion, func(t *testing.T) {
				spec := comparisonSpec(t, 2)
				if kind == KindRun {
					run := preparedRunFixture(t)
					spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
				}
				legacy := completeTaskCapabilities()
				legacy.Features = slices.DeleteFunc(legacy.Features, func(value string) bool {
					return value == FeatureJSONPrefixAssertions
				})
				if !legacy.SupportsJob(spec) {
					t.Fatal("ordinary task unexpectedly requires JSON prefix assertions")
				}
				for _, run := range spec.PreparedRuns() {
					testCase := &run.Snapshot.Entries[0].CaseDefinitions[0]
					testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
						`{"inputs":{},"request":{"body":{"messages":[]}},"assertions":[` + assertion + `]}`,
					)
				}
				assertConfiguredFeatureRequired(t, spec, FeatureJSONPrefixAssertions)
			})
		}
	}
}
