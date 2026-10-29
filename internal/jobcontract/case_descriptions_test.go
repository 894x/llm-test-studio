package jobcontract

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"
)

func TestDescribedCaseRequiresCapabilityForRunAndComparison(t *testing.T) {
	for _, kind := range []Kind{KindRun, KindComparison} {
		t.Run(string(kind), func(t *testing.T) {
			spec := comparisonSpec(t, 2)
			if kind == KindRun {
				run := preparedRunFixture(t)
				spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
			}
			capabilities := completeTaskCapabilities()
			capabilities.Features = slices.DeleteFunc(capabilities.Features, func(value string) bool {
				return value == FeatureCaseDescriptions
			})
			if !capabilities.SupportsJob(spec) {
				t.Fatal("legacy case without description unexpectedly requires the new capability")
			}
			for _, run := range spec.PreparedRuns() {
				run.Snapshot.Entries[0].CaseDefinitions[0].Description = "Purpose and acceptance context."
			}
			assertConfiguredFeatureRequired(t, spec, FeatureCaseDescriptions)
			assertTaskCapabilityRejectionUnchanged(t, capabilities, spec)
			encoded, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			var decoded JobSpec
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			reencoded, err := json.Marshal(decoded)
			if err != nil || !bytes.Equal(encoded, reencoded) {
				t.Fatalf("new job boundary lost frozen description: %v", err)
			}
		})
	}
}
