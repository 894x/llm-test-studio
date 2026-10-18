package jobcontract

import (
	"bytes"
	"encoding/json"
	"slices"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestTaskCapabilitiesRequireCurrentKindAndProtocolWithoutMutation(t *testing.T) {
	run := preparedRunFixture(t)
	runSpec := performanceSpec(t)
	runSpec.Kind, runSpec.Spec = KindRun, Payload{Run: &run}
	for _, spec := range []JobSpec{runSpec, performanceSpec(t), comparisonSpec(t, 2)} {
		t.Run(string(spec.Kind), func(t *testing.T) {
			capabilities := completeTaskCapabilities()
			if !capabilities.SupportsJob(spec) {
				t.Fatal("complete current capabilities rejected a valid task")
			}
			for _, missing := range []string{"kind", "protocol"} {
				t.Run(missing, func(t *testing.T) {
					current := completeTaskCapabilities()
					if missing == "kind" {
						current.JobKinds = slices.DeleteFunc(current.JobKinds, func(kind Kind) bool { return kind == spec.Kind })
					} else {
						current.Protocols = []string{"different-protocol"}
					}
					assertTaskCapabilityRejectionUnchanged(t, current, spec)
				})
			}
		})
	}
}

func TestTaskCapabilitiesRequireConfiguredPerformanceAndRunFeatures(t *testing.T) {
	for _, test := range []struct {
		feature string
		change  func(*quicktest.PerformanceProfile)
	}{
		{feature: "warmup", change: func(profile *quicktest.PerformanceProfile) { profile.WarmupRequests = 1 }},
		{feature: "ramp", change: func(profile *quicktest.PerformanceProfile) {
			profile.RampDurationMS, profile.RampRequestCap = 1000, 1
		}},
		{feature: "capacity", change: func(profile *quicktest.PerformanceProfile) {
			profile.CapacityEnabled, profile.CapacityStart, profile.CapacityStep = true, 1, 1
			profile.SLOE2EMS, profile.SLOTargetPercent = 1000, 100
		}},
		{feature: "open_loop", change: func(profile *quicktest.PerformanceProfile) {
			profile.LoadMode, profile.Concurrency = domain.LoadOpenLoop, 0
			profile.RatePerSecond, profile.MaxInFlight = 1, 1
		}},
		{feature: "time_slices", change: func(profile *quicktest.PerformanceProfile) { profile.SliceDurationMS = 1 }},
		{feature: "normal_workload", change: func(profile *quicktest.PerformanceProfile) {
			profile.WorkloadMode, profile.RandomSeed = quicktest.PerformanceWorkloadNormal, 1
		}},
		{feature: "random_input", change: func(profile *quicktest.PerformanceProfile) { profile.RandomInput = true }},
	} {
		t.Run("performance/"+test.feature, func(t *testing.T) {
			spec := performanceSpec(t)
			test.change(&spec.Spec.Performance.Profile)
			assertConfiguredFeatureRequired(t, spec, test.feature)
		})
	}
	t.Run("assets", func(t *testing.T) {
		spec := performanceSpec(t)
		spec.Assets = testAssets(1, 1)
		assertConfiguredFeatureRequired(t, spec, "assets")
	})
	for _, kind := range []Kind{KindRun, KindComparison} {
		for _, feature := range []string{"warmup", "open_loop"} {
			t.Run(string(kind)+"/"+feature, func(t *testing.T) {
				spec := comparisonSpec(t, 2)
				if kind == KindRun {
					run := preparedRunFixture(t)
					spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
				}
				for _, run := range spec.PreparedRuns() {
					if feature == "warmup" {
						run.Snapshot.Entries[0].WarmupCount = 1
						run.Snapshot.PlanDocument.Entries[0].WarmupCount = 1
						continue
					}
					profile := run.Snapshot.Entries[0].Load
					profile.Mode, profile.RatePerSecond = domain.LoadOpenLoop, 1
					run.Snapshot.Entries[0].Load = profile
					run.Snapshot.PlanDocument.Entries[0].Load = profile
				}
				assertConfiguredFeatureRequired(t, spec, feature)
			})
		}
	}
}

func completeTaskCapabilities() RunnerCapabilities {
	return RunnerCapabilities{
		JobKinds: []Kind{KindRun, KindPerformance, KindComparison}, Protocols: []string{"openai-chat"},
		Features: []string{
			FeatureExecutionOrigin, FeatureMixedPlanItems, "warmup", "ramp", "capacity", "open_loop", "time_slices",
			"normal_workload", "random_input", "assets",
		},
	}
}

func assertConfiguredFeatureRequired(t *testing.T, spec JobSpec, feature string) {
	t.Helper()
	if err := spec.Validate(); err != nil {
		t.Fatalf("feature fixture is not a valid current task: %v", err)
	}
	capabilities := completeTaskCapabilities()
	if !capabilities.SupportsJob(spec) {
		t.Fatal("complete capabilities rejected a configured task")
	}
	capabilities.Features = slices.DeleteFunc(capabilities.Features, func(value string) bool { return value == feature })
	assertTaskCapabilityRejectionUnchanged(t, capabilities, spec)
}

func assertTaskCapabilityRejectionUnchanged(t *testing.T, capabilities RunnerCapabilities, spec JobSpec) {
	t.Helper()
	input := struct {
		Capabilities RunnerCapabilities
		Spec         JobSpec
	}{Capabilities: capabilities, Spec: spec}
	before, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if capabilities.SupportsJob(spec) {
		t.Fatal("task requiring an absent capability was accepted")
	}
	after, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("checking unsupported task capabilities mutated the input")
	}
}

func TestStopPolicyRequiresMixedPlanCapability(t *testing.T) {
	run := preparedRunFixture(t)
	run.Snapshot.Entries[0].OnFailure = "stop"
	run.Snapshot.PlanDocument.Entries[0].OnFailure = "stop"
	spec := performanceSpec(t)
	spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
	assertConfiguredFeatureRequired(t, spec, FeatureMixedPlanItems)
}
