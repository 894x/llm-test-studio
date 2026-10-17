package jobcontent_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const (
	managerStoreID = "fb000000-0000-4000-8000-000000000001"
	managerInstall = "fb000000-0000-4000-8000-000000000002"
	managerJobID   = "fb000000-0000-4000-8000-000000000003"
	otherJobID     = "fb000000-0000-4000-8000-000000000004"
)

var managerEpoch = time.Date(
	2026,
	10,
	16,
	12,
	0,
	0,
	0,
	time.UTC,
)

func managerSpec(t *testing.T, assets ...jobcontract.Asset) jobcontract.JobSpec {
	t.Helper()
	prepared, secret, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "fixture", APIKey: "fixture-only-not-a-provider-key",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	if err := secret.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.ReportID = "fb000000-0000-4000-8000-000000000005"
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: managerJobID, NodeInstallationID: managerInstall,
		EngineBuildID: strings.Repeat("a", 64), Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal},
		Kind: jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared},
		Assets: append([]jobcontract.Asset{}, assets...),
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	return spec
}

func managerIdentity(body []byte) jobcontract.ContentIdentity {
	sum := sha256.Sum256(body)
	return jobcontract.ContentIdentity{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))}
}

func managerAsset(ref string, body []byte) jobcontract.Asset {
	identity := managerIdentity(body)
	return jobcontract.Asset{Ref: ref, SHA256: identity.SHA256, Size: identity.Size, MediaType: "application/octet-stream"}
}

func managerSpecSHA(t *testing.T, spec jobcontract.JobSpec) string {
	t.Helper()
	digest, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

// The channel-controlled physical fixture checks bytes but has no kernel FDs.
// Real Linux identity and FD ownership are tested separately through contentstate.
type managerPhysical struct {
	mu       sync.Mutex
	store    jobcontent.Store
	bodies   map[string][]byte
	objects  map[string]jobcontent.ObjectIdentity
	pins     []*managerPin
	openHook func(context.Context, jobcontract.ContentIdentity) error
	readyErr error
	ready    atomic.Int64
	opens    atomic.Int64
	puts     atomic.Int64
	checks   atomic.Int64
	opened   chan *managerPin
}

func newManagerPhysical() *managerPhysical {
	return &managerPhysical{
		store: jobcontent.Store{
			ID: managerStoreID, Kind: jobcontent.StoreKindExecutor,
			InstallationID: managerInstall, CreatedAt: managerEpoch,
		},
		bodies: map[string][]byte{}, objects: map[string]jobcontent.ObjectIdentity{},
		pins: []*managerPin{}, opened: make(chan *managerPin, 256),
	}
}

func (physical *managerPhysical) Store() jobcontent.Store { return physical.store }
func (physical *managerPhysical) Ready(ctx context.Context) error {
	physical.ready.Add(1)
	if err := ctx.Err(); err != nil {
		return err
	}
	return physical.readyErr
}

func (physical *managerPhysical) PutContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.Reader,
) (bool, error) {
	physical.puts.Add(1)
	body, err := io.ReadAll(io.LimitReader(source, identity.Size+1))
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if managerIdentity(body) != identity {
		return false, jobcontract.ErrAssetIntegrity
	}
	physical.mu.Lock()
	defer physical.mu.Unlock()
	if previous, exists := physical.bodies[identity.SHA256]; exists {
		if !bytes.Equal(previous, body) {
			return false, jobcontent.ErrCorrupt
		}
		return false, nil
	}
	physical.bodies[identity.SHA256] = bytes.Clone(body)
	physical.objects[identity.SHA256] = jobcontent.ObjectIdentity{
		StoreID: physical.store.ID, Content: identity,
		IncarnationID: "fb000000-0000-4000-8000-000000000006",
		WitnessSHA256: identity.SHA256, ChangeToken: identity.SHA256,
	}
	return true, nil
}

func (physical *managerPhysical) OpenPinned(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
) (jobcontent.PinnedContent, error) {
	physical.opens.Add(1)
	physical.mu.Lock()
	hook := physical.openHook
	physical.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, identity); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	physical.mu.Lock()
	defer physical.mu.Unlock()
	body, exists := physical.bodies[identity.SHA256]
	if !exists {
		return nil, jobcontent.ErrNotFound
	}
	if managerIdentity(body) != identity {
		return nil, jobcontent.ErrCorrupt
	}
	pin := &managerPin{
		physical: physical, ctx: ctx, object: physical.objects[identity.SHA256],
		reader: bytes.NewReader(bytes.Clone(body)), closed: make(chan struct{}),
	}
	physical.pins = append(physical.pins, pin)
	physical.opened <- pin
	return pin, nil
}

func (physical *managerPhysical) setOpenHook(hook func(context.Context, jobcontract.ContentIdentity) error) {
	physical.mu.Lock()
	defer physical.mu.Unlock()
	physical.openHook = hook
}

func (physical *managerPhysical) pinSnapshot() []*managerPin {
	physical.mu.Lock()
	defer physical.mu.Unlock()
	return append([]*managerPin{}, physical.pins...)
}

type managerPin struct {
	mu       sync.Mutex
	physical *managerPhysical
	ctx      context.Context
	object   jobcontent.ObjectIdentity
	reader   *bytes.Reader
	closed   chan struct{}
	closing  sync.Once
	closes   atomic.Int64
}

func (pin *managerPin) Object() jobcontent.ObjectIdentity { return pin.object }
func (pin *managerPin) Check() error {
	pin.physical.checks.Add(1)
	select {
	case <-pin.closed:
		return io.ErrClosedPipe
	default:
		return pin.ctx.Err()
	}
}
func (pin *managerPin) Read(buffer []byte) (int, error) {
	if err := pin.Check(); err != nil {
		return 0, err
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	return pin.reader.Read(buffer)
}
func (pin *managerPin) Seek(offset int64, whence int) (int64, error) {
	if err := pin.Check(); err != nil {
		return 0, err
	}
	pin.mu.Lock()
	defer pin.mu.Unlock()
	return pin.reader.Seek(offset, whence)
}
func (pin *managerPin) Close() error {
	pin.closes.Add(1)
	pin.closing.Do(func() { close(pin.closed) })
	return nil
}

type managerLedger struct {
	mu           sync.Mutex
	stores       map[string]jobcontent.Store
	publications map[string]jobcontent.Generation
	publishHook  func(context.Context, jobcontent.Publication) error
	lookupHook   func(context.Context) error
	publishes    atomic.Int64
}

func newManagerLedger() *managerLedger {
	return &managerLedger{
		stores: map[string]jobcontent.Store{}, publications: map[string]jobcontent.Generation{},
	}
}

func (ledger *managerLedger) RegisterContentStore(ctx context.Context, store jobcontent.Store) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if previous, exists := ledger.stores[store.ID]; exists && previous != store {
		return jobcontent.ErrConflict
	}
	ledger.stores[store.ID] = store
	return nil
}

func (ledger *managerLedger) PublishContent(
	ctx context.Context,
	publication jobcontent.Publication,
) (jobcontent.Generation, bool, error) {
	ledger.publishes.Add(1)
	ledger.mu.Lock()
	hook := ledger.publishHook
	ledger.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, publication); err != nil {
			return jobcontent.Generation{}, false, err
		}
	}
	if err := ctx.Err(); err != nil {
		return jobcontent.Generation{}, false, err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	key := publication.Object.StoreID + "/" + publication.Object.Content.SHA256
	if previous, exists := ledger.publications[key]; exists {
		if previous.Object != publication.Object {
			return jobcontent.Generation{}, false, jobcontent.ErrConflict
		}
		return previous, false, nil
	}
	generation := jobcontent.Generation{Object: publication.Object, Number: 1, CreatedAt: publication.At}
	ledger.publications[key] = generation
	return generation, true, nil
}

func (ledger *managerLedger) ContentPublication(
	ctx context.Context,
	storeID, sha string,
) (jobcontent.Generation, error) {
	ledger.mu.Lock()
	hook := ledger.lookupHook
	ledger.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return jobcontent.Generation{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return jobcontent.Generation{}, err
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	generation, exists := ledger.publications[storeID+"/"+sha]
	if !exists {
		return jobcontent.Generation{}, jobcontent.ErrNotFound
	}
	return generation, nil
}

func (*managerLedger) JobContent(context.Context, string) (jobcontent.Inventory, error) {
	return jobcontent.Inventory{}, jobcontent.ErrNotFound
}

func newTestManager(t *testing.T, limits jobcontent.Limits) (*jobcontent.Manager, *managerPhysical, *managerLedger) {
	t.Helper()
	physical, ledger := newManagerPhysical(), newManagerLedger()
	manager, err := jobcontent.New(t.Context(), jobcontent.Dependencies{
		Physical: physical, Ledger: ledger, Limits: limits,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	return manager, physical, ledger
}

func uploadManagerBody(t *testing.T, manager *jobcontent.Manager, body []byte) jobcontent.UploadResult {
	t.Helper()
	result, err := manager.Upload(t.Context(), managerIdentity(body), io.NopCloser(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertManagerEmpty(t *testing.T, manager *jobcontent.Manager) {
	t.Helper()
	if got := manager.Status(); got != (jobcontent.Status{}) {
		t.Fatalf("manager retained ownership: %+v", got)
	}
}

func TestManagerRequiresExplicitBoundedLimits(t *testing.T) {
	want := jobcontent.Limits{Operations: 4, Pins: 256, Borrowed: 128, Resources: 128}
	if got := jobcontent.DefaultLimits(); got != want {
		t.Fatalf("default limits=%+v", got)
	}
	for _, limits := range []jobcontent.Limits{
		{}, {Operations: 5, Pins: 1, Borrowed: 1, Resources: 1},
		{Operations: 0, Pins: 1, Borrowed: 1, Resources: 1},
		{Operations: 1, Pins: 0, Borrowed: 1, Resources: 1},
		{Operations: 1, Pins: 1, Borrowed: 0, Resources: 1},
		{Operations: 1, Pins: 1, Borrowed: 1, Resources: 0},
		{Operations: 1, Pins: 257, Borrowed: 1, Resources: 1},
		{Operations: 1, Pins: 1, Borrowed: 129, Resources: 1},
		{Operations: 1, Pins: 1, Borrowed: 1, Resources: 129},
		{Operations: -1, Pins: 1, Borrowed: 1, Resources: 1},
	} {
		manager, err := jobcontent.New(t.Context(), jobcontent.Dependencies{
			Physical: newManagerPhysical(), Ledger: newManagerLedger(), Limits: limits,
		})
		if manager != nil {
			_ = manager.Close()
		}
		if !errors.Is(err, jobcontent.ErrInvalid) {
			t.Fatalf("invalid limits %+v accepted: %v", limits, err)
		}
	}
}

func TestManagerUploadPreservesUnclaimedGenerationAndOwnsInput(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("independent content bytes")
	input := &countedReadCloser{Reader: bytes.NewReader(body)}
	first, err := manager.Upload(t.Context(), managerIdentity(body), input)
	if err != nil || !first.PhysicalCreated || first.Generation.Object.Content != managerIdentity(body) {
		t.Fatalf("first upload=%+v err=%v", first, err)
	}
	if input.closes.Load() != 1 {
		t.Fatal("upload did not close its input exactly once")
	}
	second := uploadManagerBody(t, manager, body)
	if second.PhysicalCreated || !reflect.DeepEqual(first.Generation, second.Generation) {
		t.Fatal("duplicate upload replaced immutable publication")
	}
	assertManagerEmpty(t, manager)
	for _, pin := range physical.pinSnapshot() {
		if pin.closes.Load() != 1 {
			t.Fatal("upload retained its verification anchor")
		}
	}
}

type countedReadCloser struct {
	io.Reader
	closeErr error
	closes   atomic.Int64
}

func (source *countedReadCloser) Close() error {
	source.closes.Add(1)
	return source.closeErr
}

func TestManagerFailedUploadNeverPublishesOrReplacesExistingBytes(t *testing.T) {
	for _, kind := range []string{"short", "extra", "wrong hash", "close failed"} {
		t.Run(kind, func(t *testing.T) {
			manager, physical, ledger := newTestManager(t, jobcontent.DefaultLimits())
			body := []byte("the original verified content")
			uploadManagerBody(t, manager, body)
			previous := ledger.publishes.Load()
			inputBody := bytes.Clone(body)
			closeErr := error(nil)
			switch kind {
			case "short":
				inputBody = inputBody[:len(inputBody)-1]
			case "extra":
				inputBody = append(inputBody, '!')
			case "wrong hash":
				inputBody[0] ^= 1
			case "close failed":
				closeErr = errors.New("owned input close failed")
			}
			input := &countedReadCloser{Reader: bytes.NewReader(inputBody), closeErr: closeErr}
			if _, err := manager.Upload(t.Context(), managerIdentity(body), input); err == nil {
				t.Fatal("failed body or close returned upload success")
			} else if closeErr != nil && !errors.Is(err, closeErr) {
				t.Fatalf("lost source close error: %v", err)
			}
			if input.closes.Load() != 1 || ledger.publishes.Load() != previous {
				t.Fatal("failed upload did not close once or still published")
			}
			physical.mu.Lock()
			stored := bytes.Clone(physical.bodies[managerIdentity(body).SHA256])
			physical.mu.Unlock()
			if !bytes.Equal(stored, body) {
				t.Fatal("bad upload replaced existing verified bytes")
			}
			assertManagerEmpty(t, manager)
		})
	}
}

func TestManagerPermitIsOwnedLiveAndIndependentOfClaimsCopies(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("permit source bytes")
	uploadManagerBody(t, manager, body)
	spec := managerSpec(t, managerAsset("input", body))
	prepared, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	claims := prepared.Claims()
	if len(claims) != 1 || claims[0].Object.Content != managerIdentity(body) {
		t.Fatalf("prepared claims=%+v", claims)
	}
	claims[0].Ref = "forged"
	claims[0].Object.WitnessSHA256 = strings.Repeat("f", 64)
	digest := managerSpecSHA(t, spec)
	beforeChecks, beforeOpens, beforeReady := physical.checks.Load(), physical.opens.Load(), physical.ready.Load()
	verified, err := manager.ValidatePermit(prepared.Permit(), digest)
	if err != nil || len(verified) != 1 || verified[0].Ref != "input" {
		t.Fatalf("claims copy changed live permit: %+v %v", verified, err)
	}
	performedIO := physical.checks.Load() != beforeChecks || physical.opens.Load() != beforeOpens ||
		physical.ready.Load() != beforeReady
	if performedIO {
		t.Fatal("lightweight permit validation performed physical I/O")
	}
	for _, permit := range []*jobcontent.Permit{nil, {}} {
		if _, err := manager.ValidatePermit(permit, digest); !errors.Is(err, jobcontent.ErrInvalid) {
			t.Fatalf("unminted permit accepted: %v", err)
		}
	}
	copied := *prepared.Permit()
	if _, err := manager.ValidatePermit(&copied, digest); !errors.Is(err, jobcontent.ErrInvalid) {
		t.Fatalf("copied permit pointer became independently live: %v", err)
	}
	other, _, _ := newTestManager(t, jobcontent.DefaultLimits())
	if _, err := other.ValidatePermit(prepared.Permit(), digest); !errors.Is(err, jobcontent.ErrInvalid) {
		t.Fatalf("another manager accepted foreign permit: %v", err)
	}
	changed := spec
	changed.JobID = otherJobID
	changedSHA := managerSpecSHA(t, changed)
	if _, err := manager.ValidatePermit(prepared.Permit(), changedSHA); !errors.Is(err, jobcontent.ErrInvalid) {
		t.Fatalf("different spec accepted permit: %v", err)
	}
	permit := prepared.Permit()
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ValidatePermit(permit, digest); !errors.Is(err, jobcontent.ErrInvalid) {
		t.Fatalf("closed permit accepted: %v", err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerEmptyPreparationChecksPhysicalReadinessEveryTime(t *testing.T) {
	manager, physical, _ := newTestManager(t, jobcontent.DefaultLimits())
	physical.readyErr = jobcontent.ErrCorrupt
	if prepared, err := manager.Prepare(t.Context(), managerSpec(t)); err == nil || prepared != nil {
		if prepared != nil {
			_ = prepared.Close()
		}
		t.Fatalf("empty manifest minted ownership after physical failure: %v", err)
	}
	assertManagerEmpty(t, manager)
}

func TestManagerResourcesResolveIndependentReadersAndCloseAllBorrowers(t *testing.T) {
	manager, _, _ := newTestManager(t, jobcontent.DefaultLimits())
	body := []byte("0123456789abcdef")
	uploadManagerBody(t, manager, body)
	spec := managerSpec(t, managerAsset("input", body))
	prepared, err := manager.Prepare(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	if _, err := prepared.BindJob(otherJobID); !errors.Is(err, jobcontent.ErrInvalid) {
		t.Fatalf("resources bound another job: %v", err)
	}
	resources, err := prepared.BindJob(spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	defer resources.Close()
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if first.Identity() != managerIdentity(body) || first.MediaType() != "application/octet-stream" {
		t.Fatal("resolver changed frozen descriptor")
	}
	prefix := make([]byte, 4)
	if _, err := io.ReadFull(first, prefix); err != nil || !bytes.Equal(prefix, body[:4]) {
		t.Fatal("first reader prefix failed", err)
	}
	second, err := resources.Resolver().Open(t.Context(), "input")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	all, err := io.ReadAll(second)
	if err != nil || !bytes.Equal(all, body) {
		t.Fatal("second reader reused the first offset", err)
	}
	rest, err := io.ReadAll(first)
	if err != nil || !bytes.Equal(rest, body[4:]) {
		t.Fatal("second reader moved the first offset", err)
	}
	if _, err := resources.Resolver().Open(t.Context(), "unknown"); !errors.Is(err, jobcontent.ErrNotFound) {
		t.Fatalf("unknown reference resolved: %v", err)
	}
	if err := resources.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Read(make([]byte, 1)); err == nil {
		t.Fatal("resource close left its borrower live")
	}
	assertManagerEmpty(t, manager)
}
