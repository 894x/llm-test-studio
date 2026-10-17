package runnerprocess

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

func contentIdentity(size int64) jobcontract.ContentIdentity {
	return jobcontract.ContentIdentity{SHA256: strings.Repeat("a", 64), Size: size}
}

func TestContentAndJobReservationsShareStorageBudget(t *testing.T) {
	guard := &diskGuard{
		state:  fixedDiskUsage{available: 100},
		config: runnerconfig.Config{MaxStorageBytes: 100, ReservationBytes: 60},
	}
	job, err := guard.Reserve(t.Context(), jobcontract.JobSpec{})
	if err != nil {
		t.Fatal(err)
	}
	defer job.Close()
	if _, err := guard.reserveContent(t.Context(), contentIdentity(41)); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatalf("upload ignored job reservation: %v", err)
	}
	upload, err := guard.reserveContent(t.Context(), contentIdentity(40))
	if err != nil {
		t.Fatal(err)
	}
	defer upload.Close()
	if _, err := guard.Reserve(t.Context(), jobcontract.JobSpec{}); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatalf("job ignored upload reservation: %v", err)
	}
	_ = upload.Close()
	_ = upload.Close()
	if guard.reserved != 60 || guard.uploads != 0 {
		t.Fatal("upload lease did not release exactly once")
	}
}

func TestContentReservationsBoundSlotsAndRetainedFiles(t *testing.T) {
	guard := &diskGuard{
		state:  fixedDiskUsage{available: 100},
		config: runnerconfig.Config{MaxStorageBytes: 100},
	}
	leases := []io.Closer{}
	for range maxContentUploads {
		lease, err := guard.reserveContent(t.Context(), contentIdentity(1))
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	if _, err := guard.reserveContent(t.Context(), contentIdentity(1)); !errors.Is(err, jobhost.ErrCapacity) {
		t.Fatalf("unbounded active uploads: %v", err)
	}
	for _, lease := range leases {
		_ = lease.Close()
	}
	for _, count := range []uint64{maxContentFiles - 1, maxContentFiles, maxContentFiles + 1} {
		guard.state = fixedDiskUsage{available: 100, contentFiles: count}
		lease, err := guard.reserveContent(t.Context(), contentIdentity(1))
		if count == maxContentFiles-1 {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := guard.reserveContent(t.Context(), contentIdentity(1)); !errors.Is(err, jobhost.ErrCapacity) {
				t.Fatal("pending scratch was not counted before file creation")
			}
			_ = lease.Close()
			continue
		}
		if !errors.Is(err, jobhost.ErrCapacity) {
			t.Fatalf("retained content count=%d was not a soft refusal: %v", count, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := guard.reserveContent(ctx, contentIdentity(1)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled reservation ignored context")
	}
}

func TestContentAndJobConcurrentReservationsCannotOvercommit(t *testing.T) {
	guard := &diskGuard{
		state:  fixedDiskUsage{available: 100},
		config: runnerconfig.Config{MaxStorageBytes: 100, ReservationBytes: 60},
	}
	var workers sync.WaitGroup
	leases := make(chan io.Closer, 32)
	for index := range 32 {
		workers.Go(func() {
			var lease io.Closer
			var err error
			if index%2 == 0 {
				lease, err = guard.reserveContent(t.Context(), contentIdentity(50))
			} else {
				lease, err = guard.Reserve(t.Context(), jobcontract.JobSpec{})
			}
			if err == nil {
				leases <- lease
			} else if !errors.Is(err, jobhost.ErrCapacity) {
				t.Errorf("unexpected reservation failure: %v", err)
			}
		})
	}
	workers.Wait()
	close(leases)
	if len(leases) < 1 || guard.reserved > 100 || guard.uploads > maxContentUploads {
		t.Fatalf("shared quota was overcommitted: leases=%d bytes=%d uploads=%d", len(leases), guard.reserved, guard.uploads)
	}
	for lease := range leases {
		workers.Go(func() {
			_ = lease.Close()
			_ = lease.Close()
		})
	}
	workers.Wait()
	if guard.reserved != 0 || guard.uploads != 0 {
		t.Fatal("parallel lease cleanup did not restore the shared budget")
	}
	for _, available := range []uint64{99, 100, 101} {
		guard.state = fixedDiskUsage{available: available}
		guard.config.MinFreeBytes = 10
		lease, err := guard.reserveContent(t.Context(), contentIdentity(90))
		if available < 100 {
			if !errors.Is(err, jobhost.ErrCapacity) {
				t.Fatal("free-space floor was exceeded")
			}
			continue
		}
		if err != nil {
			t.Fatal("inclusive free-space boundary was rejected")
		}
		_ = lease.Close()
	}
}

type contentWriteFunc func(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error)

func (write contentWriteFunc) PutContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.Reader,
) (bool, error) {
	return write(ctx, identity, source)
}

type contentFaultCounter struct{ calls int }

func (owner *contentFaultCounter) MarkUnavailable() { owner.calls++ }

func TestContentStoreIsolatesOnlyRealStorageFailures(t *testing.T) {
	for _, test := range []struct {
		name  string
		err   error
		fault bool
	}{
		{name: "success"},
		{name: "integrity", err: jobcontract.ErrAssetIntegrity},
		{name: "reader", err: jobcontract.ErrAssetRead},
		{name: "cancelled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "storage", err: jobcontract.ErrAssetStorage, fault: true},
		{name: "unknown_io", err: errors.New("test_file_failure"), fault: true},
		{
			name: "body_and_close_failure",
			err:  errors.Join(jobcontract.ErrAssetIntegrity, jobcontract.ErrAssetStorage), fault: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			guard := &diskGuard{
				state:  fixedDiskUsage{available: 100},
				config: runnerconfig.Config{MaxStorageBytes: 100},
			}
			owner := &contentFaultCounter{}
			store := &contentStore{
				guard: guard, host: owner,
				state: contentWriteFunc(func(context.Context, jobcontract.ContentIdentity, io.Reader) (bool, error) {
					return test.err == nil, test.err
				}),
			}
			_, err := store.PutContent(t.Context(), contentIdentity(1), strings.NewReader("a"))
			if (owner.calls == 1) != test.fault || errors.Is(err, jobcontract.ErrAssetStorage) != test.fault {
				t.Fatalf("incorrect storage fault classification: calls=%d err=%v", owner.calls, err)
			}
			if guard.reserved != 0 || guard.uploads != 0 {
				t.Fatal("completed upload leaked its full scratch reservation")
			}
		})
	}
}
