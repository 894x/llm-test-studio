package runs_test

import (
	"context"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func passedVerification() testspec.Verdict {
	return testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}}
}
func failedVerification() testspec.Verdict {
	return testspec.Verdict{Status: testspec.VerdictFailed, Assertions: []testspec.AssertionResult{}}
}
func startFixtureRun(s *runs.Service, ctx context.Context, f runFixture) error {
	_, err := s.StartTarget(ctx, runs.StartCommand{PlanID: f.plan.ID, ModelID: f.model.ID, ChannelID: f.channel.ID})
	return err
}

type recordingExecutor struct {
	calls      int
	caseCounts []int
}

func (e *recordingExecutor) Execute(_ context.Context, r runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
	e.calls++
	e.caseCounts = append(e.caseCounts, len(r.Cases))
	return nil
}
