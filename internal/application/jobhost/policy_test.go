package jobhost

import (
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestPolicyExplicitResourceBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Policy)
		valid  bool
	}{
		{name: "remote defaults", valid: true},
		{name: "no active slot", change: func(policy *Policy) { policy.MaxActiveJobs = 0 }},
		{name: "maximum slots", change: func(policy *Policy) { policy.MaxActiveJobs = 128 }, valid: true},
		{name: "too many slots", change: func(policy *Policy) { policy.MaxActiveJobs = 129 }},
		{name: "no queue", change: func(policy *Policy) { policy.MaxQueuedJobs = 0 }, valid: true},
		{name: "maximum queue", change: func(policy *Policy) { policy.MaxQueuedJobs = 1024 }, valid: true},
		{name: "too much queue", change: func(policy *Policy) { policy.MaxQueuedJobs = 1025 }},
		{name: "no concurrency", change: func(policy *Policy) { policy.MaxConcurrency = 0 }},
		{name: "maximum concurrency", change: func(policy *Policy) { policy.MaxConcurrency = 65_536 }, valid: true},
		{name: "too much concurrency", change: func(policy *Policy) { policy.MaxConcurrency = 65_537 }},
		{name: "no queue deadline", change: func(policy *Policy) { policy.QueueTimeout = 0 }},
		{name: "negative run deadline", change: func(policy *Policy) { policy.ExecutionTimeout = -time.Second }},
		{name: "maximum run deadline", change: func(policy *Policy) {
			policy.ExecutionTimeout = 24 * time.Hour
		}, valid: true},
		{name: "run deadline overflow", change: func(policy *Policy) {
			policy.ExecutionTimeout = 24*time.Hour + time.Nanosecond
		}},
		{name: "queue deadline overflow", change: func(policy *Policy) {
			policy.QueueTimeout = 24*time.Hour + time.Nanosecond
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := RemotePolicy()
			if test.change != nil {
				test.change(&policy)
			}
			if err := policy.Validate(); (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func budgetRun(entries ...domain.RunEntrySnapshot) runs.PreparedRun {
	return runs.PreparedRun{Snapshot: domain.RunSnapshot{Entries: entries}}
}

func TestConcurrencyBudgetUsesCorePhasePeaksAndWholeComparison(t *testing.T) {
	single := domain.RunEntrySnapshot{
		Load: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 16}, WarmupCount: 10,
		CaseDefinitions: make([]domain.TestCase, 3),
	}
	warmup := domain.RunEntrySnapshot{
		Load: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 4, RequestCount: 1}, WarmupCount: 8,
	}
	openLoop := domain.RunEntrySnapshot{
		Load: domain.LoadProfile{Mode: domain.LoadOpenLoop, Concurrency: 1, DurationMS: 1000},
	}
	for _, test := range []struct {
		name string
		run  runs.PreparedRun
		want uint64
	}{
		{name: "single complete cases", run: budgetRun(single), want: 3},
		{name: "warmup above measured request cap", run: budgetRun(warmup), want: 4},
		{name: "open loop uses scheduler safety limit", run: budgetRun(openLoop), want: load.MaxOpenLoopInFlight},
		{name: "entries are sequential", run: budgetRun(single, warmup), want: 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := jobcontract.JobSpec{Kind: jobcontract.KindRun, Spec: jobcontract.Payload{Run: &test.run}}
			if got := jobConcurrency(spec); got != test.want {
				t.Fatalf("budget=%d want=%d", got, test.want)
			}
		})
	}
	comparison := comparisons.PreparedComparison{Runs: []runs.PreparedRun{budgetRun(single), budgetRun(warmup)}}
	spec := jobcontract.JobSpec{Kind: jobcontract.KindComparison, Spec: jobcontract.Payload{Comparison: &comparison}}
	if got := jobConcurrency(spec); got != 7 {
		t.Fatalf("comparison did not add child peaks: %d", got)
	}
	performance := quicktest.PreparedPerformance{Profile: quicktest.PerformanceProfile{
		LoadMode: domain.LoadOpenLoop, MaxInFlight: 13,
	}}
	spec = jobcontract.JobSpec{Kind: jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &performance}}
	if got := jobConcurrency(spec); got != 13 {
		t.Fatalf("performance did not use explicit in-flight bound: %d", got)
	}
}
