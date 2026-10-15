package runnerprocess

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
