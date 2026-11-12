package jobcontract

import (
	"slices"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

const FeatureExecutionOrigin = "execution_origin"
const FeatureMixedPlanItems = "mixed_plan_items"
const FeaturePerformanceAffinity = "performance_affinity"
const FeatureFunctionalProgress = "functional_progress"
const FeatureCaseDescriptions = "case_descriptions"
const FeatureCacheWriteHeaders = "cache_write_headers"
const FeatureJSONPrefixAssertions = "json_prefix_assertions"
const FeatureSequenceSteps = "sequence_steps"
const FeatureAssertionFailureStatus = "assertion_failure_status"
const FeatureHTTPHeaderEvidence = "http_header_evidence"

// SupportsJob checks only the current task requirements, independent of client
// release and build. Report-transfer requirements may be stricter for a caller.
func (capabilities RunnerCapabilities) SupportsJob(spec JobSpec) bool {
	supportedKind := slices.Contains(capabilities.JobKinds, spec.Kind)
	supportedOrigin := slices.Contains(capabilities.Features, FeatureExecutionOrigin)
	if spec.Validate() != nil || !supportedKind || !supportedOrigin {
		return false
	}
	protocols := []string{}
	features := []string{}
	if spec.HTTPHeaderEvidence {
		features = append(features, FeatureHTTPHeaderEvidence)
	}
	if spec.FunctionalProgress {
		features = append(features, FeatureFunctionalProgress)
	}
	if spec.Kind == KindPerformance {
		performance := spec.Spec.Performance
		protocols = append(protocols, string(performance.Protocol))
		features = append(features, performanceFeatures(performance.Profile)...)
	} else {
		for _, run := range spec.PreparedRuns() {
			protocols = append(protocols, string(run.Snapshot.Channel.Protocol))
			for index, entry := range run.Snapshot.Entries {
				for _, testCase := range entry.CaseDefinitions {
					if testCase.Description != "" {
						features = append(features, FeatureCaseDescriptions)
					}
					definition, err := testCase.SpecFor(run.Snapshot.Channel.Protocol)
					if err != nil {
						return false
					}
					if requiresCacheWriteHeaders(definition.Assertions) {
						features = append(features, FeatureCacheWriteHeaders)
					}
					if requiresJSONPrefixAssertions(definition.Assertions) {
						features = append(features, FeatureJSONPrefixAssertions)
					}
					if definition.UsesSequenceControls() {
						features = append(features, FeatureSequenceSteps)
					}
					if requiresAssertionFailureStatus(definition.Assertions) {
						features = append(features, FeatureAssertionFailureStatus)
					}
				}
				if entry.Performance != nil || entry.OnFailure != "" || run.Snapshot.PlanDocument.Entries[index].Name != "" {
					features = append(features, FeatureMixedPlanItems)
				}
				if entry.Performance != nil {
					prepared, err := quicktest.PreparePlanEntry(entry.EntryID, run.Snapshot.Channel, *entry.Performance)
					if err != nil {
						return false
					}
					features = append(features, performanceFeatures(prepared.Profile)...)
				}
				if entry.WarmupCount > 0 {
					features = append(features, "warmup")
				}
				if entry.Load.Mode == domain.LoadOpenLoop {
					features = append(features, "open_loop")
				}
			}
		}
	}
	if len(spec.Assets) > 0 {
		features = append(features, "assets")
	}
	return containsAll(capabilities.Protocols, protocols) && containsAll(capabilities.Features, features)
}

func containsAll(supported, required []string) bool {
	for _, item := range required {
		if !slices.Contains(supported, item) {
			return false
		}
	}
	return true
}

func performanceFeatures(profile quicktest.PerformanceProfile) []string {
	features := []string{}
	if profile.WarmupRequests > 0 {
		features = append(features, "warmup")
	}
	if profile.RampDurationMS > 0 {
		features = append(features, "ramp")
	}
	if profile.CapacityEnabled {
		features = append(features, "capacity")
	}
	if profile.LoadMode == domain.LoadOpenLoop {
		features = append(features, "open_loop")
	}
	if profile.SliceDurationMS > 0 {
		features = append(features, "time_slices")
	}
	if profile.WorkloadMode == quicktest.PerformanceWorkloadNormal {
		features = append(features, "normal_workload")
	}
	if profile.RandomInput {
		features = append(features, "random_input")
	}
	if profile.AffinityEnabled {
		features = append(features, FeaturePerformanceAffinity)
	}
	return features
}
