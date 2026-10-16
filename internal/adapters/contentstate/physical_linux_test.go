package contentstate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

const testInstallation = "11111111-1111-4111-8111-111111111111"

func physicalFixture(t *testing.T) (*runnerstate.State, *Adapter, []byte, jobcontract.ContentIdentity) {
	t.Helper()
	state, err := runnerstate.Open(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	adapter, err := New(t.Context(), Config{
		State: state, InstallationID: testInstallation,
		Now: func() time.Time { return time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	document := []byte("independent private seekable content")
	sum := sha256.Sum256(document)
	identity := jobcontract.ContentIdentity{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(document))}
	if _, err := adapter.PutContent(t.Context(), identity, bytes.NewReader(document)); err != nil {
		t.Fatal(err)
	}
	return state, adapter, document, identity
}

func TestPhysicalStoreOpensIndependentKernelPinnedContent(t *testing.T) {
	state, adapter, document, identity := physicalFixture(t)
	store := adapter.Store()
	if store.Validate() != nil || store.Kind != jobcontent.StoreKindExecutor || store.InstallationID != testInstallation {
		t.Fatal("invalid physical store owner")
	}
	first, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	object := first.Object()
	if object.Validate() != nil || object.StoreID != store.ID || object.Content != identity {
		t.Fatal("physical identity did not bind its store and bytes")
	}
	t.Logf("actual_store_id=%s actual_incarnation_id=%s", object.StoreID, object.IncarnationID)
	if _, err := first.Seek(4, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	second, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.Object() != object {
		t.Fatal("same physical file changed identity across independent opens")
	}
	one := make([]byte, 1)
	if n, err := second.Read(one); n != 1 || err != nil || one[0] != document[0] {
		t.Fatal("new pin shared the previous descriptor offset", err)
	}
	if n, err := first.Read(one); n != 1 || err != nil || one[0] != document[4] {
		t.Fatal("opening a second pin changed the first offset", err)
	}
	if _, err := first.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(first)
	if err != nil || !bytes.Equal(got, document) {
		t.Fatal("pinned stream changed bytes", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Ready(t.Context()); err != nil {
		t.Fatal("pin close closed runtime-owned State", err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.Check(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("State.Close left an accepted pin", err)
	}
}

func TestPhysicalStoreRevalidatesBytesAndNeverPublishesCorruptReplacement(t *testing.T) {
	state, adapter, document, identity := physicalFixture(t)
	path := filepath.Join(state.Path(), "content-"+identity.SHA256+".blob")
	pin, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), len(document)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.OpenPinned(t.Context(), identity); err == nil {
		t.Fatal("reopen trusted descriptor metadata without hashing bytes")
	}
	if _, err := pin.Read(make([]byte, 1)); err == nil {
		t.Fatal("original pin read modified bytes")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.PutContent(t.Context(), identity, bytes.NewReader(document)); err == nil {
		t.Fatal("duplicate upload silently replaced a corrupt complete file")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("corrupt complete file was replaced", err)
	}
}

func TestPhysicalStoreMarkerAndRootReplacementInvalidateLivePins(t *testing.T) {
	for _, mode := range []string{"marker_replaced", "marker_permissions", "root_replaced", "root_symlink", "hard_link", "file_symlink"} {
		t.Run(mode, func(t *testing.T) {
			state, adapter, document, identity := physicalFixture(t)
			pin, err := adapter.OpenPinned(t.Context(), identity)
			if err != nil {
				t.Fatal(err)
			}
			defer pin.Close()
			marker := filepath.Join(state.Path(), runnerstate.ContentStoreName)
			file := filepath.Join(state.Path(), "content-"+identity.SHA256+".blob")
			switch mode {
			case "marker_replaced":
				raw, err := os.ReadFile(marker)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(marker, marker+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(marker, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "marker_permissions":
				if err := os.Chmod(marker, 0644); err != nil {
					t.Fatal(err)
				}
			case "root_replaced", "root_symlink":
				if err := os.Rename(state.Path(), state.Path()+"-retained"); err != nil {
					t.Fatal(err)
				}
				if mode == "root_replaced" {
					if err := os.Mkdir(state.Path(), 0700); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(state.Path()+"-retained", state.Path()); err != nil {
					t.Fatal(err)
				}
			case "hard_link":
				if err := os.Link(file, file+".linked"); err != nil {
					t.Fatal(err)
				}
			case "file_symlink":
				if err := os.Rename(file, file+".retained"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(file+".retained", file); err != nil {
					t.Fatal(err)
				}
			}
			if err := pin.Check(); err == nil {
				t.Fatal("changed ownership accepted")
			}
			if _, err := pin.Read(make([]byte, len(document))); err == nil {
				t.Fatal("changed ownership read through original pin")
			}
			if _, err := adapter.OpenPinned(t.Context(), identity); err == nil {
				t.Fatal("changed ownership admitted new pin")
			}
		})
	}
}

func TestPhysicalStoreCancellationAffectsOnlyOneIndependentReader(t *testing.T) {
	_, adapter, document, identity := physicalFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	first, err := adapter.OpenPinned(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	cancel()
	if err := first.Check(); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled pin remained active", err)
	}
	got, err := io.ReadAll(second)
	if err != nil || !bytes.Equal(got, document) {
		t.Fatal("one cancellation poisoned independent pin", err)
	}
}

func TestPhysicalStoreRejectsInvalidInstallationWithoutCreatingMarker(t *testing.T) {
	state, err := runnerstate.Open(filepath.Join(t.TempDir(), "private"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, installation := range []string{"", "00000000-0000-0000-0000-000000000000", "old-installation"} {
		if _, err := New(t.Context(), Config{State: state, InstallationID: installation}); !errors.Is(err, jobcontent.ErrInvalid) {
			t.Fatal("invalid installation accepted", err)
		}
	}
	if _, err := os.Lstat(filepath.Join(state.Path(), runnerstate.ContentStoreName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid configuration created a marker")
	}
}

func TestPhysicalStoreMarkerChecksAndPinsReleaseDescriptors(t *testing.T) {
	_, adapter, _, identity := physicalFixture(t)
	warm, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := warm.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	for range 32 {
		if err := adapter.Ready(t.Context()); err != nil {
			t.Fatal(err)
		}
		pin, err := adapter.OpenPinned(t.Context(), identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := pin.Check(); err != nil {
			pin.Close()
			t.Fatal(err)
		}
		if err := pin.Close(); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("marker checks or pins leaked descriptors: before=%d after=%d", len(before), len(after))
	}
}

func TestPhysicalStoreConcurrentPinOperationsBoundTransientDescriptors(t *testing.T) {
	_, adapter, _, identity := physicalFixture(t)
	pin, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer pin.Close()
	baseline, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	finished := make(chan struct{})
	failures := make(chan error, 64)
	var group sync.WaitGroup
	for index := range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range 16 {
				var err error
				switch index % 3 {
				case 0:
					err = pin.Check()
				case 1:
					_, err = pin.Seek(0, io.SeekStart)
				case 2:
					_, err = pin.Read(make([]byte, 1))
				}
				if err != nil && !errors.Is(err, io.EOF) {
					failures <- err
					return
				}
			}
		}()
	}
	go func() {
		group.Wait()
		close(finished)
	}()
	close(start)
	maximum := len(baseline)
	var sampleErr error
sampling:
	for {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			sampleErr = err
			break
		}
		maximum = max(maximum, len(entries))
		select {
		case <-finished:
			break sampling
		default:
		}
	}
	<-finished
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if sampleErr != nil {
		t.Fatal(sampleErr)
	}
	// One serialized operation may briefly hold its marker and directory handles.
	if maximum > len(baseline)+4 {
		t.Fatalf("one pin admitted unbounded transient descriptors: baseline=%d maximum=%d", len(baseline), maximum)
	}
	t.Logf("concurrent_operations=64 baseline_fds=%d maximum_fds=%d", len(baseline), maximum)
}

func TestPhysicalStoreCloseJoinsOperationAndGatesMarkerIO(t *testing.T) {
	state, adapter, _, identity := physicalFixture(t)
	baseline, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	content := pin.(*pinnedContent)
	content.mu.Lock()
	closed := make(chan error, 1)
	go func() { closed <- pin.Close() }()
	deadline := time.After(time.Second)
	for !content.closed.Load() {
		select {
		case <-deadline:
			content.mu.Unlock()
			t.Fatal("close did not gate new operations")
		default:
			runtime.Gosched()
		}
	}
	returnedEarly := false
	select {
	case err = <-closed:
		returnedEarly = true
	case <-time.After(25 * time.Millisecond):
	}
	if changeErr := os.Chmod(filepath.Join(state.Path(), runnerstate.ContentStoreName), 0644); changeErr != nil {
		content.mu.Unlock()
		t.Fatal(changeErr)
	}
	checking := make(chan error, 1)
	go func() { checking <- pin.Check() }()
	content.mu.Unlock()
	if !returnedEarly {
		err = <-closed
	}
	if returnedEarly {
		t.Error("close returned before joining the in-flight operation")
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := <-checking; !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed pin checked the now-invalid marker", err)
	}
	if _, err := pin.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed read reached marker IO", err)
	}
	if _, err := pin.Seek(0, io.SeekStart); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed seek reached marker IO", err)
	}
	if err := pin.Close(); err != nil {
		t.Fatal("repeated close changed result", err)
	}
	after, err := os.ReadDir("/proc/self/fd")
	if err != nil || len(after) != len(baseline) {
		t.Fatalf("close did not reclaim all pin descriptors: before=%d after=%d error=%v", len(baseline), len(after), err)
	}
}

func TestPhysicalStoreConcurrentCloseDrainsReadersAndDescriptors(t *testing.T) {
	_, adapter, _, identity := physicalFixture(t)
	baseline, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	pin, err := adapter.OpenPinned(t.Context(), identity)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	failures := make(chan error, 48)
	var group sync.WaitGroup
	for index := range 48 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for range 8 {
				var err error
				switch index % 4 {
				case 0:
					err = pin.Check()
				case 1:
					_, err = pin.Read(make([]byte, 1))
				case 2:
					_, err = pin.Seek(0, io.SeekStart)
				case 3:
					err = pin.Close()
				}
				if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
					failures <- err
					return
				}
			}
		}()
	}
	close(start)
	if err := pin.Close(); err != nil {
		t.Fatal(err)
	}
	afterClose, err := os.ReadDir("/proc/self/fd")
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if err != nil || len(afterClose) != len(baseline) {
		t.Fatalf("close returned before descriptor drain: before=%d after_close=%d error=%v", len(baseline), len(afterClose), err)
	}
}
