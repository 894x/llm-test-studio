package jobcontent_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func receiveManager[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("manager operation did not reach its channel boundary")
		var zero T
		return zero
	}
}

func responsiveManagerStatus(t *testing.T, manager *jobcontent.Manager) jobcontent.Status {
	t.Helper()
	status := make(chan jobcontent.Status, 1)
	go func() { status <- manager.Status() }()
	return receiveManager(t, status)
}

type blockedManagerInput struct {
	started  chan struct{}
	released chan struct{}
	exited   chan struct{}
	start    sync.Once
	close    sync.Once
	exit     sync.Once
	closes   atomic.Int64
}

func newBlockedManagerInput() *blockedManagerInput {
	return &blockedManagerInput{
		started: make(chan struct{}), released: make(chan struct{}), exited: make(chan struct{}),
	}
}

func (source *blockedManagerInput) Read([]byte) (int, error) {
	source.start.Do(func() { close(source.started) })
	<-source.released
	source.exit.Do(func() { close(source.exited) })
	return 0, io.ErrClosedPipe
}

func (source *blockedManagerInput) Close() error {
	source.closes.Add(1)
	source.close.Do(func() { close(source.released) })
	return nil
}

func TestManagerCloseClosesOwnedInputAndJoinsBlockedRead(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	input := newBlockedManagerInput()
	defer func() {
		if input.closes.Load() == 0 {
			_ = input.Close()
		}
	}()
	result := make(chan error, 1)
	go func() {
		_, err := manager.Upload(t.Context(), managerIdentity([]byte("pending bytes")), input)
		result <- err
	}()
	receiveManager(t, input.started)
	if got := responsiveManagerStatus(t, manager); got.Operations != 1 {
		t.Fatalf("blocked upload operation not owned: %+v", got)
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	if err := receiveManager(t, closed); err != nil {
		t.Fatal(err)
	}
	select {
	case <-input.exited:
	default:
		t.Fatal("Manager.Close returned before its Read exited")
	}
	if err := receiveManager(t, result); err == nil || input.closes.Load() != 1 {
		t.Fatalf("blocked source was not failed/closed once: %v closes=%d", err, input.closes.Load())
	}
	assertManagerEmpty(t, manager)
	if err := physical.Ready(t.Context()); err != nil {
		t.Fatal("manager closed its runtime-owned physical store", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal("second Manager.Close failed", err)
	}
	late := &countedReadCloser{Reader: bytes.NewReader([]byte("late"))}
	lateIdentity := managerIdentity([]byte("late"))
	if _, err := manager.Upload(t.Context(), lateIdentity, late); !errors.Is(err, jobcontent.ErrClosed) {
		t.Fatalf("closed manager accepted upload: %v", err)
	}
	if late.closes.Load() != 1 {
		t.Fatal("closed manager abandoned its transferred input")
	}
	if _, err := manager.Prepare(t.Context(), managerSpec(t)); !errors.Is(err, jobcontent.ErrClosed) {
		t.Fatalf("closed manager prepared resources: %v", err)
	}
}

func TestManagerLedgerWaitDoesNotHoldMutexAndCloseCancelsIt(t *testing.T) {
	manager, _, ledger := newTestManager(t, jobcontent.DefaultLimits())
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	ledger.publishHook = func(ctx context.Context, _ jobcontent.Publication) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	result := make(chan error, 1)
	go func() {
		body := []byte("ledger blocks after complete verification")
		_, err := manager.Upload(t.Context(), managerIdentity(body), io.NopCloser(bytes.NewReader(body)))
		result <- err
	}()
	receiveManager(t, entered)
	if got := responsiveManagerStatus(t, manager); got.Operations != 1 {
		t.Fatalf("ledger operation not counted: %+v", got)
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close() }()
	if err := receiveManager(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := receiveManager(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("manager close did not cancel publication: %v", err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerLimitsOperationsAndClosesRejectedInput(t *testing.T) {
	limits := jobcontent.DefaultLimits()
	limits.Operations = 1
	manager, _, _ := newTestManager(t, limits)
	input := newBlockedManagerInput()
	defer func() {
		if input.closes.Load() == 0 {
			_ = input.Close()
		}
	}()
	first := make(chan error, 1)
	go func() {
		_, err := manager.Upload(t.Context(), managerIdentity([]byte("first")), input)
		first <- err
	}()
	receiveManager(t, input.started)
	other := &countedReadCloser{Reader: bytes.NewReader([]byte("second"))}
	otherIdentity := managerIdentity([]byte("second"))
	if _, err := manager.Upload(t.Context(), otherIdentity, other); !errors.Is(err, jobcontent.ErrLimit) {
		t.Fatalf("operation limit was not enforced: %v", err)
	}
	if other.closes.Load() != 1 {
		t.Fatal("limit rejection abandoned its owned input")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if err := receiveManager(t, first); err == nil {
		t.Fatal("closing blocked upload reported success")
	}
	assertManagerEmpty(t, manager)
}

func TestManagerPreparationCancellationClosesEveryAnchor(t *testing.T) {
	for _, stage := range []string{"before", "during second open", "after prepare"} {
		t.Run(stage, func(t *testing.T) {
			manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
			firstBody, secondBody := []byte("first asset"), []byte("second asset")
			uploadManagerBody(t, manager, firstBody)
			uploadManagerBody(t, manager, secondBody)
			spec := managerSpec(t, managerAsset("a", firstBody), managerAsset("b", secondBody))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if stage == "before" {
				before := physical.opens.Load()
				cancel()
				if _, err := manager.Prepare(ctx, spec); !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled preparation accepted: %v", err)
				}
				if physical.opens.Load() != before {
					t.Fatal("cancelled preparation opened physical content")
				}
			} else if stage == "during second open" {
				entered, release := make(chan struct{}), make(chan struct{})
				defer close(release)
				var calls atomic.Int64
				physical.setOpenHook(func(ctx context.Context, _ jobcontract.ContentIdentity) error {
					if calls.Add(1) != 2 {
						return nil
					}
					close(entered)
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-release:
						return nil
					}
				})
				result := make(chan error, 1)
				go func() {
					prepared, err := manager.Prepare(ctx, spec)
					if prepared != nil {
						_ = prepared.Close()
					}
					result <- err
				}()
				receiveManager(t, entered)
				if got := responsiveManagerStatus(t, manager); got.Operations != 1 {
					t.Fatalf("preparation held the manager mutex: %+v", got)
				}
				cancel()
				if err := receiveManager(t, result); !errors.Is(err, context.Canceled) {
					t.Fatalf("open cancellation was lost: %v", err)
				}
			} else {
				prepared, err := manager.Prepare(ctx, spec)
				if err != nil {
					t.Fatal(err)
				}
				pins := physical.pinSnapshot()
				cancel()
				for _, pin := range pins {
					receiveManager(t, pin.closed)
				}
				if err := prepared.Close(); err != nil {
					t.Fatal(err)
				}
			}
			assertManagerEmpty(t, manager)
			for _, pin := range physical.pinSnapshot() {
				if pin.closes.Load() != 1 {
					t.Fatalf("cancel leaked or multiply closed a pin: %d", pin.closes.Load())
				}
			}
		})
	}
}

func TestManagerBindingDetachesSubmissionCancellation(t *testing.T) {
	manager, _, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("bound resources outlive submission")
	uploadManagerBody(t, manager, body)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	spec := managerSpec(t, managerAsset("input", body))
	prepared, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	cancel()
	stream, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal("submission cancellation closed transferred job resources", err)
	}
	defer stream.Close()
	actual, err := io.ReadAll(stream)
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatal("bound anchor retained cancelled submission context", err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerResourceCloseCancelsAndJoinsBorrowOpening(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("borrow pin will block")
	uploadManagerBody(t, manager, body)
	spec := managerSpec(t, managerAsset("input", body))
	prepared, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	physical.setOpenHook(func(ctx context.Context, _ jobcontract.ContentIdentity) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	opened := make(chan error, 1)
	go func() {
		stream, err := resources.Resolver().Open(t.Context(), "input")
		if stream != nil {
			_ = stream.Close()
		}
		opened <- err
	}()
	receiveManager(t, entered)
	if got := responsiveManagerStatus(t, manager); got.Borrowed != 1 || got.Operations != 1 {
		t.Fatalf("opening borrower not owned: %+v", got)
	}
	closed := make(chan error, 1)
	go func() { closed <- resources.Close() }()
	if err := receiveManager(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := receiveManager(t, opened); err == nil {
		t.Fatal("resource close admitted its in-flight borrower")
	}
	assertManagerEmpty(t, manager)
}

func TestManagerResourceAndBorrowLimitsAreReleased(t *testing.T) {
	limits := jobcontent.DefaultLimits()
	limits.Resources = 1
	limits.Borrowed = 1
	manager, _, _ := newTestManager(t, limits)
	empty, err := manager.Prepare(t.Context(), managerSpec(t))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if _, err := manager.Prepare(t.Context(), managerSpec(t)); !errors.Is(err, jobcontent.ErrLimit) {
		t.Fatalf("empty manifests bypassed resource limit: %v", err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	body := []byte("borrow limit")
	uploadManagerBody(t, manager, body)
	spec := managerSpec(t, managerAsset("input", body))
	prepared, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	first, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := resources.Resolver().Open(t.Context(), "input"); !errors.Is(err, jobcontent.ErrLimit) {
		t.Fatalf("borrower limit ignored: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal("closed borrower did not release its slot", err)
	}
	defer second.Close()
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerPinLimitDoesNotLeavePartialPreparedResources(t *testing.T) {
	limits := jobcontent.DefaultLimits()
	limits.Pins = 1
	manager, _, _ := newTestManager(t, limits)
	first, second := []byte("first independent input"), []byte("second independent input")
	uploadManagerBody(t, manager, first)
	uploadManagerBody(t, manager, second)
	spec := managerSpec(t, managerAsset("first", first), managerAsset("second", second))
	if prepared, err := manager.Prepare(t.Context(), spec); !errors.Is(err, jobcontent.ErrLimit) {
		if prepared != nil {
			_ = prepared.Close()
		}
		t.Fatalf("too many anchors accepted: %v", err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerBindRacingSubmissionCancellationHasOneResourceOwner(t *testing.T) {
	manager, _, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("bind/cancel boundary")
	uploadManagerBody(t, manager, body)
	spec := managerSpec(t, managerAsset("input", body))
	for range 32 {
		ctx, cancel := context.WithCancel(t.Context())
		prepared, err := manager.Prepare(ctx, spec)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		type outcome struct {
			resources jobcontent.JobResources
			err       error
		}
		start, cancelled := make(chan struct{}), make(chan struct{})
		bound := make(chan outcome, 1)
		go func() {
			<-start
			resources, err := prepared.BindJob(spec.JobID)
			bound <- outcome{resources: resources, err: err}
		}()
		go func() {
			<-start
			cancel()
			close(cancelled)
		}()
		close(start)
		result := receiveManager(t, bound)
		receiveManager(t, cancelled)
		if result.err == nil {
			if result.resources == nil {
				t.Fatal("successful binding lost its owner")
			}
			stream, err := result.resources.Resolver().Open(t.Context(), "input")
			if err != nil {
				t.Fatal("successful bind was later reclaimed by submission callback", err)
			}
			actual, readErr := io.ReadAll(stream)
			closeErr := stream.Close()
			if readErr != nil || closeErr != nil || !bytes.Equal(actual, body) {
				t.Fatalf("bound reader lost bytes: %v %v", readErr, closeErr)
			}
			if err := result.resources.Close(); err != nil {
				t.Fatal(err)
			}
			if err := result.resources.Close(); err != nil {
				t.Fatal("repeated resource close failed", err)
			}
		} else if result.resources != nil {
			t.Fatal("failed bind returned an owner")
		}
		if err := prepared.Close(); err != nil {
			t.Fatal(err)
		}
		if err := prepared.Close(); err != nil {
			t.Fatal("repeated prepared close failed", err)
		}
		assertManagerEmpty(t, manager)
	}
}

func TestManagerConcurrentSameHashPinsLeaveNoHashRegistryEntry(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("one hash across two independent jobs")
	uploadManagerBody(t, manager, body)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseOpen := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseOpen()
	physical.setOpenHook(func(ctx context.Context, _ jobcontract.ContentIdentity) error {
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	})
	type outcome struct {
		prepared jobcontent.PreparedResources
		err      error
	}
	results := make(chan outcome, 2)
	for _, id := range []string{managerJobID, otherJobID} {
		spec := managerSpec(t, managerAsset("input", body))
		spec.JobID = id
		go func() {
			prepared, err := manager.Prepare(t.Context(), spec)
			results <- outcome{prepared: prepared, err: err}
		}()
	}
	receiveManager(t, entered)
	if got := responsiveManagerStatus(t, manager); got.Hashes != 1 || got.Operations < 1 || got.Operations > 2 {
		t.Fatalf("same hash operations did not share bounded registry: %+v", got)
	}
	releaseOpen()
	first, second := receiveManager(t, results), receiveManager(t, results)
	if first.err != nil || second.err != nil {
		t.Fatalf("concurrent preparation failed: %v %v", first.err, second.err)
	}
	defer first.prepared.Close()
	defer second.prepared.Close()
	if got := manager.Status(); got.Hashes != 1 || got.Resources != 2 {
		t.Fatalf("independent resource owners did not retain the shared hash: %+v", got)
	}
	if err := first.prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if got := manager.Status(); got.Hashes != 1 || got.Resources != 1 {
		t.Fatalf("closing one job dropped another shared hash: %+v", got)
	}
	if err := second.prepared.Close(); err != nil {
		t.Fatal(err)
	}
	assertManagerEmpty(t, manager)
}
