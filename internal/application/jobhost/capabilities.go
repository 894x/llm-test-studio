package jobhost

import (
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/protocols"
)

// CoreCapabilities describes execution shared by local and remote hosts.
// Transport-specific management and file services add their own capabilities.
func CoreCapabilities() jobcontract.RunnerCapabilities {
	protocolIDs := []string{}
	for _, descriptor := range protocols.NewRegistry().Descriptors() {
		protocolIDs = append(protocolIDs, descriptor.ID)
	}
	return jobcontract.RunnerCapabilities{
		JobKinds:  []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindPerformance, jobcontract.KindComparison},
		Protocols: protocolIDs,
		Features: []string{
			jobcontract.FeatureExecutionOrigin, jobcontract.FeaturePerformanceArchives,
			"warmup", "ramp", "capacity", "open_loop",
			"time_slices", "normal_workload", "random_input",
		},
	}
}
