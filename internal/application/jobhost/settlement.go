package jobhost

import (
	"context"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

// A missing Run report needs at most eleven storage calls, plus one child
// read during Comparison refresh. Reserve their independent I/O allowances
// and a minute of computation per child; the four group operations are fixed.
// This is a work bound, separate from request execution and individual I/O.
func settlementBudget(spec jobcontract.JobSpec) time.Duration {
	children := max(1, len(spec.PreparedRuns()))
	return time.Duration(12*children+4)*storageTimeout + time.Duration(children)*time.Minute
}

// Closing permits a short drain, then cancels all settlement work and joins it.
// Normal work may use its full workload budget while the owner is running.
const settlementCloseGrace = 2 * storageTimeout

func (host *Host) settle(ctx context.Context, spec jobcontract.JobSpec, outcome Outcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	work, cancel := context.WithTimeoutCause(ctx, settlementBudget(spec), ErrSettlementLimit)
	defer cancel()
	if err := work.Err(); err != nil {
		return err
	}
	err := host.executor.Settle(work, spec, outcome)
	if cause := context.Cause(work); cause != nil {
		return errors.Join(err, cause)
	}
	return err
}
