package runnerprocess

import (
	"context"
	"io"
	"sync"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type diskGuard struct {
	state    *runnerstate.State
	config   runnerconfig.Config
	mu       sync.Mutex
	reserved uint64
}

func (guard *diskGuard) Reserve(ctx context.Context, spec jobcontract.JobSpec) (io.Closer, error) {
	// File asset verification and resolution are connected together in the next
	// asset phase. This runner does not advertise the assets capability yet.
	if len(spec.Assets) != 0 {
		return nil, jobhost.ErrInvalid
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if err := guard.check(ctx, guard.config.ReservationBytes); err != nil {
		return nil, err
	}
	guard.reserved += guard.config.ReservationBytes
	return &diskReservation{guard: guard, bytes: guard.config.ReservationBytes}, nil
}

func (guard *diskGuard) check(ctx context.Context, additional uint64) error {
	used, err := guard.state.UsedBytes(ctx)
	if err != nil {
		return err
	}
	available, err := guard.state.AvailableBytes()
	if err != nil {
		return err
	}
	reserve := guard.reserved + additional
	validBudget := reserve <= guard.config.MaxStorageBytes && used <= guard.config.MaxStorageBytes-reserve
	validFree := available >= guard.config.MinFreeBytes && available-guard.config.MinFreeBytes >= reserve
	if !validBudget || !validFree {
		return jobhost.ErrCapacity
	}
	return ctx.Err()
}

func (guard *diskGuard) Check(ctx context.Context) error {
	guard.mu.Lock()
	defer guard.mu.Unlock()
	return guard.check(ctx, 0)
}

type diskReservation struct {
	guard *diskGuard
	bytes uint64
	once  sync.Once
}

func (reservation *diskReservation) Close() error {
	reservation.once.Do(func() {
		reservation.guard.mu.Lock()
		defer reservation.guard.mu.Unlock()
		reservation.guard.reserved -= reservation.bytes
	})
	return nil
}
