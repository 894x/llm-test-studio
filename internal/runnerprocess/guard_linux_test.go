package runnerprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func TestDiskAdmissionReservesHeadroomAndReleasesExactlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	state, err := runnerstate.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	config := runnerconfig.Config{MaxStorageBytes: 512 << 20, ReservationBytes: 64 << 20}
	guard := &diskGuard{state: state, config: config}
	file, err := os.Create(filepath.Join(path, "used"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := file.Truncate(449 << 20); err != nil {
		t.Fatal(err)
	}
	spec := jobcontract.JobSpec{Assets: []jobcontract.Asset{}}
	if _, err := guard.Reserve(t.Context(), spec); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatalf("insufficient storage budget accepted: %v", err)
	}
	if err := file.Truncate(447 << 20); err != nil {
		t.Fatal(err)
	}
	reservation, err := guard.Reserve(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := guard.Reserve(t.Context(), spec); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatal("concurrent admission ignored reserved bytes")
	}
	_ = reservation.Close()
	_ = reservation.Close()
	reservation, err = guard.Reserve(t.Context(), spec)
	if err != nil {
		t.Fatal("released headroom was not reusable")
	}
	defer reservation.Close()
	if err := file.Truncate(513 << 20); err != nil {
		t.Fatal(err)
	}
	if err := guard.Check(t.Context()); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatal("periodic disk check missed exceeded storage budget")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := guard.Check(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("disk scan ignored cancellation")
	}
}

type fixedDiskUsage struct {
	used         uint64
	available    uint64
	contentFiles uint64
	usedErr      error
	availableErr error
	contentErr   error
}

func (usage fixedDiskUsage) ContentFiles(ctx context.Context) (uint64, error) {
	return usage.contentFiles, errors.Join(ctx.Err(), usage.contentErr)
}

func (usage fixedDiskUsage) UsedBytes(ctx context.Context) (uint64, error) {
	return usage.used, errors.Join(ctx.Err(), usage.usedErr)
}

func (usage fixedDiskUsage) AvailableBytes() (uint64, error) {
	return usage.available, usage.availableErr
}

func TestStorageProbeChecksPersistenceEvenWhenAdmissionIsFull(t *testing.T) {
	storageErr := errors.New("test_storage_failure")
	for _, test := range []struct {
		name     string
		usage    fixedDiskUsage
		writeErr error
		wantErr  error
		wantCall bool
	}{
		{name: "within_budget", usage: fixedDiskUsage{available: 100}, wantCall: true},
		{name: "used_limit", usage: fixedDiskUsage{used: 101, available: 100}, wantCall: true},
		{name: "free_limit", usage: fixedDiskUsage{available: 9}, wantCall: true},
		{
			name: "full_and_unwritable", usage: fixedDiskUsage{used: 101, available: 100},
			writeErr: storageErr, wantErr: storageErr, wantCall: true,
		},
		{name: "used_error", usage: fixedDiskUsage{usedErr: storageErr}, wantErr: storageErr},
		{name: "available_error", usage: fixedDiskUsage{availableErr: storageErr}, wantErr: storageErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			guard := &diskGuard{
				state:  test.usage,
				config: runnerconfig.Config{MaxStorageBytes: 100, MinFreeBytes: 10},
			}
			called := false
			err := checkStorage(t.Context(), guard, func(ctx context.Context) error {
				called = true
				if ctx != t.Context() {
					t.Fatal("storage probe replaced the caller context")
				}
				return test.writeErr
			})
			if !errors.Is(err, test.wantErr) || called != test.wantCall {
				t.Fatalf("probe error=%v, persistence called=%v", err, called)
			}
		})
	}
}

func TestDiskAdmissionConcurrentReservationsRespectHeadroom(t *testing.T) {
	guard := &diskGuard{
		state:  fixedDiskUsage{available: 100},
		config: runnerconfig.Config{MaxStorageBytes: 100, ReservationBytes: 60},
	}
	var workers sync.WaitGroup
	reservations := make(chan io.Closer, 16)
	for range 16 {
		workers.Go(func() {
			reservation, err := guard.Reserve(t.Context(), jobcontract.JobSpec{})
			if err == nil {
				reservations <- reservation
			} else if !errors.Is(err, jobhost.ErrCapacity) {
				t.Errorf("unexpected admission error: %v", err)
			}
		})
	}
	workers.Wait()
	close(reservations)
	if len(reservations) != 1 {
		t.Fatalf("accepted %d reservations, want one", len(reservations))
	}
	for reservation := range reservations {
		for range 16 {
			workers.Go(func() { _ = reservation.Close() })
		}
	}
	workers.Wait()
	reservation, err := guard.Reserve(t.Context(), jobcontract.JobSpec{})
	if err != nil {
		t.Fatalf("concurrent close did not release exactly one reservation: %v", err)
	}
	_ = reservation.Close()
}
