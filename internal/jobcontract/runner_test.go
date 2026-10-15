package jobcontract_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func runnerInfo() jobcontract.RunnerInfo {
	return jobcontract.RunnerInfo{
		Contract:           jobcontract.CurrentContract,
		NodeID:             "11111111-1111-4111-8111-111111111111",
		NodeInstallationID: "22222222-2222-4222-8222-222222222222",
		EngineBuildID:      strings.Repeat("b", 64), RunnerVersion: "runner-dev", OS: "linux", Arch: "amd64",
		Capabilities: jobcontract.RunnerCapabilities{
			JobKinds:  []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindPerformance, jobcontract.KindComparison},
			Protocols: []string{"openai-chat"}, Features: []string{},
		},
		Budget: jobcontract.RunnerBudget{
			MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 2000,
			QueueTimeoutNS: int64(15 * time.Minute), ExecutionTimeoutNS: int64(time.Hour),
		},
	}
}

func TestRunnerInfoValidatesIdentityCapabilitiesAndExactBudgetBoundaries(t *testing.T) {
	for _, change := range []func(*jobcontract.RunnerInfo){
		func(info *jobcontract.RunnerInfo) { info.Contract = 0 },
		func(info *jobcontract.RunnerInfo) { info.Contract++ },
		func(info *jobcontract.RunnerInfo) { info.NodeID = "invalid" },
		func(info *jobcontract.RunnerInfo) { info.NodeInstallationID = "invalid" },
		func(info *jobcontract.RunnerInfo) { info.EngineBuildID = strings.Repeat("A", 64) },
		func(info *jobcontract.RunnerInfo) { info.RunnerVersion = "" },
		func(info *jobcontract.RunnerInfo) { info.RunnerVersion = strings.Repeat("x", 129) },
		func(info *jobcontract.RunnerInfo) { info.RunnerVersion = " version" },
		func(info *jobcontract.RunnerInfo) { info.OS = "linux\n" },
		func(info *jobcontract.RunnerInfo) { info.Arch = "" },
		func(info *jobcontract.RunnerInfo) { info.Budget.MaxActiveJobs = 0 },
		func(info *jobcontract.RunnerInfo) { info.Budget.MaxActiveJobs = 129 },
		func(info *jobcontract.RunnerInfo) { info.Budget.MaxQueuedJobs = 1025 },
		func(info *jobcontract.RunnerInfo) { info.Budget.MaxConcurrency = 0 },
		func(info *jobcontract.RunnerInfo) { info.Budget.MaxConcurrency = 65537 },
		func(info *jobcontract.RunnerInfo) { info.Budget.QueueTimeoutNS = 0 },
		func(info *jobcontract.RunnerInfo) { info.Budget.ExecutionTimeoutNS = -1 },
		func(info *jobcontract.RunnerInfo) { info.Budget.ExecutionTimeoutNS = int64(24*time.Hour) + 1 },
		func(info *jobcontract.RunnerInfo) { info.Active = 2 },
		func(info *jobcontract.RunnerInfo) { info.Queued = 17 },
		func(info *jobcontract.RunnerInfo) { info.Capabilities.JobKinds = []jobcontract.Kind{} },
		func(info *jobcontract.RunnerInfo) { info.Capabilities.JobKinds = []jobcontract.Kind{"unknown"} },
		func(info *jobcontract.RunnerInfo) {
			info.Capabilities.JobKinds = []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindRun}
		},
		func(info *jobcontract.RunnerInfo) { info.Capabilities.Protocols = []string{} },
		func(info *jobcontract.RunnerInfo) { info.Capabilities.Features = nil },
		func(info *jobcontract.RunnerInfo) { info.Capabilities.Features = []string{"warmup", "warmup"} },
		func(info *jobcontract.RunnerInfo) { info.Capabilities.Features = []string{strings.Repeat("x", 65)} },
	} {
		info := runnerInfo()
		change(&info)
		if info.Validate() == nil {
			t.Fatal("invalid runner metadata was accepted")
		}
	}
	for _, jobs := range []uint32{1, 128} {
		for _, concurrency := range []uint32{1, 65536} {
			for _, timeout := range []time.Duration{time.Nanosecond, 24 * time.Hour} {
				info := runnerInfo()
				info.RunnerVersion = strings.Repeat("x", 128)
				info.Budget.MaxActiveJobs, info.Budget.MaxQueuedJobs = jobs, 1024
				info.Budget.MaxConcurrency = concurrency
				info.Budget.QueueTimeoutNS, info.Budget.ExecutionTimeoutNS = int64(timeout), int64(timeout)
				info.Active, info.Queued = jobs, 1024
				info.Pending = jobs + 1024
				if err := info.Validate(); err != nil {
					t.Fatalf("valid exact resource boundary rejected: %v", err)
				}
			}
		}
	}
	for _, size := range []int{1, 64, 65} {
		info := runnerInfo()
		info.Capabilities.Protocols, info.Capabilities.Features = []string{}, []string{}
		for index := range size {
			info.Capabilities.Protocols = append(info.Capabilities.Protocols, fmt.Sprintf("protocol-%d", index))
			info.Capabilities.Features = append(info.Capabilities.Features, fmt.Sprintf("feature-%d", index))
		}
		if (info.Validate() == nil) != (size <= 64) {
			t.Fatal("capability cardinality boundary changed")
		}
	}
}
