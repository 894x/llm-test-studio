package jobcontract

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestCacheWriteMetricCapabilityCoversNestedAndReferencedAssertions(t *testing.T) {
	for _, assertion := range []string{
		`{"id":"header","source":"metrics","pointer":"/msh_usage_cache_write_tokens_5m","operator":"gte","value":0}`,
		`{"id":"final_usage","source":"metrics","pointer":"/stream_final_usage_count","operator":"equals","value":1}`,
		`{"id":"group","all":[
			{"id":"header","source":"requests","pointer":"/1/metrics/msh_usage_cache_write_tokens_1h",
			"operator":"equals","value":0}]}`,
		`{"id":"header","source":"usage","pointer":"/prompt_tokens_details/cache_write_tokens",
			"operator":"equals","expected_from":{"source":"metrics","pointer":"/msh_usage_cache_write_tokens_5m"}}`,
		`{"id":"headers","source":"requests","each":[
			{"id":"counter","source":"item","pointer":"/metrics/msh_usage_cache_write_tokens_5m",
			"operator":"gte","value":0}]}`,
		`{"id":"sum","source":"requests","pointer":"/0","operator":"sum_equals",
			"value":{"total":"/response/usage/prompt_tokens_details/cache_write_tokens",
			"parts":["/metrics/msh_usage_cache_write_tokens_5m","/metrics/msh_usage_cache_write_tokens_1h"]}}`,
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
					return value == FeatureCacheWriteHeaders
				})
				if !legacy.SupportsJob(spec) {
					t.Fatal("ordinary task unexpectedly requires header metrics")
				}
				for _, run := range spec.PreparedRuns() {
					testCase := &run.Snapshot.Entries[0].CaseDefinitions[0]
					testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
						`{"inputs":{},"request":{"body":{"messages":[]}},"assertions":[` + assertion + `]}`,
					)
				}
				assertConfiguredFeatureRequired(t, spec, FeatureCacheWriteHeaders)
			})
		}
	}
}
