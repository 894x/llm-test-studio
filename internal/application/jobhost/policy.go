package jobhost

import (
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type Policy struct {
	MaxActiveJobs    uint32        `json:"max_active_jobs"`
	MaxQueuedJobs    uint32        `json:"max_queued_jobs"`
	MaxConcurrency   uint32        `json:"max_concurrency"`
	QueueTimeout     time.Duration `json:"queue_timeout"`
	ExecutionTimeout time.Duration `json:"execution_timeout"`
}

func RemotePolicy() Policy {
	return Policy{
		MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: load.MaxOpenLoopInFlight,
		QueueTimeout: 15 * time.Minute, ExecutionTimeout: time.Hour,
	}
}

func (policy Policy) Validate() error {
	validCounts := policy.MaxActiveJobs >= 1 && policy.MaxActiveJobs <= 128 && policy.MaxQueuedJobs <= 1024
	validConcurrency := policy.MaxConcurrency >= 1 && policy.MaxConcurrency <= 65_536
	validQueueTime := policy.QueueTimeout > 0 && policy.QueueTimeout <= 24*time.Hour
	validRunTime := policy.ExecutionTimeout > 0 && policy.ExecutionTimeout <= 24*time.Hour
	if !validCounts || !validConcurrency || !validQueueTime || !validRunTime {
		return ErrInvalid
	}
	return nil
}

// Entries in a Run are sequential; Comparison children execute together.
func jobConcurrency(spec jobcontract.JobSpec) uint64 {
	if spec.Kind == jobcontract.KindPerformance {
		profile := spec.Spec.Performance.Profile
		if profile.LoadMode == domain.LoadOpenLoop {
			return uint64(profile.MaxInFlight)
		}
		return uint64(profile.Concurrency)
	}
	var total uint64
	for _, run := range spec.PreparedRuns() {
		var peak uint64
		for _, entry := range run.Snapshot.Entries {
			request := runs.ExecutionRequest{Entry: entry, Cases: entry.CaseDefinitions}
			profile := request.LoadProfile()
			concurrency := uint64(profile.Concurrency)
			if profile.Mode == domain.LoadOpenLoop {
				concurrency = load.MaxOpenLoopInFlight
			}
			if profile.RequestCount > 0 {
				concurrency = min(concurrency, profile.RequestCount)
			}
			warmup := min(uint64(profile.Concurrency), uint64(entry.WarmupCount))
			concurrency = max(concurrency, warmup)
			peak = max(peak, concurrency)
		}
		total += peak
	}
	return total
}
