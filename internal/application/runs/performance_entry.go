package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func (executor *ProtocolExecutor) executePerformance(ctx context.Context, request ExecutionRequest, emit func(ResultDraft) error) error {
	prepared, err := quicktest.PreparePlanEntry(request.Entry.EntryID, request.Run.Channel(), *request.Entry.Performance)
	if err != nil {
		return err
	}
	secret, err := request.Credential.Bytes()
	if err != nil {
		return err
	}
	lease, err := credentials.NewTemporaryLease(secret)
	clear(secret)
	if err != nil {
		return err
	}
	defer lease.Close()
	host := quicktest.NewHost(quicktest.HostDependencies{Transport: executor.transport})
	sequence := 0
	var checkpoint quicktest.PerformanceCheckpoint
	persist := func(report quicktest.PerformanceReport, final bool) error {
		raw, err := json.Marshal(report)
		if err != nil {
			return err
		}
		sequence++
		verdict := testspec.VerdictNotApplicable
		phase := "checkpoint"
		if final {
			phase = "measured"
			verdict = testspec.VerdictPassed
			failedSLO := report.SLOAssessment != nil && report.SLOAssessment.Status == quicktest.PerformanceSLOFailed
			if !report.Success || failedSLO {
				verdict = testspec.VerdictFailed
			}
		}
		dimensions := map[string]string{"phase": phase, "performance_stage": string(checkpoint.Stage), "performance_boundary": string(checkpoint.Boundary)}
		if checkpoint.RungIndex != nil {
			dimensions["performance_rung"] = strconv.FormatUint(uint64(*checkpoint.RungIndex), 10)
		}
		return emit(ResultDraft{
			RequestID: fmt.Sprintf("performance-%d", sequence), Performance: raw,
			ExecutionStatus: domain.ExecutionCompleted,
			Verification:    testspec.Verdict{Status: verdict, Assertions: []testspec.AssertionResult{}},
			Dimensions:      dimensions, Metrics: map[string]float64{},
		})
	}
	report, err := host.ExecutePrepared(ctx, prepared, lease, quicktest.ExecutionOptions{
		StopSending: request.StopSending,
		OnCheckpoint: func(_ context.Context, next quicktest.PerformanceCheckpoint) error {
			if err := next.Validate(); err != nil {
				return err
			}
			checkpoint = next
			return persist(next.Report, false)
		},
	})
	if err != nil {
		return err
	}
	return persist(report, true)
}
