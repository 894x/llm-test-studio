package jobcontent

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrClosed = errors.New("content_closed")
	ErrLimit  = errors.New("content_limit")
)

type Limits struct {
	Operations int
	Pins       int
	Borrowed   int
	Resources  int
}

func DefaultLimits() Limits {
	return Limits{Operations: 4, Pins: 256, Borrowed: 128, Resources: 128}
}

func (limits Limits) valid() bool {
	maximum := DefaultLimits()
	validOperations := limits.Operations > 0 && limits.Operations <= maximum.Operations
	validPins := limits.Pins > 0 && limits.Pins <= maximum.Pins
	validBorrowed := limits.Borrowed > 0 && limits.Borrowed <= maximum.Borrowed
	validResources := limits.Resources > 0 && limits.Resources <= maximum.Resources
	return validOperations && validPins && validBorrowed && validResources
}

type Dependencies struct {
	Physical PhysicalStore
	Ledger   Ledger
	Limits   Limits
}

type Status struct {
	Operations int
	Pins       int
	Borrowed   int
	Resources  int
	Hashes     int
}

type UploadResult struct {
	PhysicalCreated bool
	Generation      Generation
}

// Permit has no public constructor or writable evidence. It identifies a live
// resource owned by one Manager; copying Claims cannot create a permit.
type Permit struct {
	owner    *Manager
	resource *resources
}

type Manager struct {
	mu         sync.Mutex
	physical   PhysicalStore
	ledger     Ledger
	store      Store
	limits     Limits
	ctx        context.Context
	cancel     context.CancelFunc
	closed     bool
	status     Status
	hashes     map[string]*hashLease
	resources  map[*resources]struct{}
	operations sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
}

type hashLease struct {
	size    int64
	refs    int
	working bool
	changed chan struct{}
}

func New(ctx context.Context, dependencies Dependencies) (*Manager, error) {
	missingDependencies := dependencies.Physical == nil || dependencies.Ledger == nil
	if ctx == nil || missingDependencies || !dependencies.Limits.valid() {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := dependencies.Physical.Ready(ctx); err != nil {
		return nil, err
	}
	store := dependencies.Physical.Store()
	if store.Validate() != nil {
		return nil, ErrInvalid
	}
	if err := dependencies.Ledger.RegisterContentStore(ctx, store); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(context.Background())
	return &Manager{
		physical: dependencies.Physical, ledger: dependencies.Ledger, store: store,
		limits: dependencies.Limits, ctx: lifetime, cancel: cancel,
		hashes: make(map[string]*hashLease), resources: make(map[*resources]struct{}),
	}, nil
}

func (manager *Manager) Status() Status {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	status := manager.status
	status.Hashes = len(manager.hashes)
	status.Resources = len(manager.resources)
	return status
}

func (manager *Manager) begin(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalid
	}
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		manager.mu.Unlock()
		return nil, nil, err
	}
	if manager.status.Operations >= manager.limits.Operations {
		manager.mu.Unlock()
		return nil, nil, ErrLimit
	}
	manager.status.Operations++
	manager.operations.Add(1)
	manager.mu.Unlock()
	operation, cancel := context.WithCancel(manager.ctx)
	joinCancellation := cancelOn(ctx, cancel)
	return operation, func() {
		joinCancellation()
		cancel()
		manager.mu.Lock()
		manager.status.Operations--
		manager.mu.Unlock()
		manager.operations.Done()
	}, nil
}

func cancelOn(ctx context.Context, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	var once sync.Once
	stop := context.AfterFunc(ctx, func() {
		cancel()
		close(done)
	})
	return func() {
		once.Do(func() {
			if stop() {
				close(done)
			} else {
				<-done
			}
		})
	}
}

func (manager *Manager) retain(identity jobcontract.ContentIdentity, borrowed bool) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ErrClosed
	}
	if borrowed {
		if manager.status.Borrowed >= manager.limits.Borrowed {
			return ErrLimit
		}
	} else if manager.status.Pins >= manager.limits.Pins {
		return ErrLimit
	}
	lease := manager.hashes[identity.SHA256]
	if lease != nil && lease.size != identity.Size {
		return ErrConflict
	}
	if lease == nil {
		lease = &hashLease{size: identity.Size, changed: make(chan struct{})}
		manager.hashes[identity.SHA256] = lease
	}
	lease.refs++
	if borrowed {
		manager.status.Borrowed++
	} else {
		manager.status.Pins++
	}
	return nil
}

func (manager *Manager) release(identity jobcontract.ContentIdentity, borrowed bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	lease := manager.hashes[identity.SHA256]
	lease.refs--
	if borrowed {
		manager.status.Borrowed--
	} else {
		manager.status.Pins--
	}
	if lease.refs == 0 && !lease.working {
		delete(manager.hashes, identity.SHA256)
	}
}

// Work is a per-hash lease, not a mutex held across opaque IO. Retained file
// pins permit identical uploads; the physical store never overwrites a file.
func (manager *Manager) work(ctx context.Context, identity jobcontract.ContentIdentity) (func(), error) {
	for {
		manager.mu.Lock()
		if manager.closed {
			manager.mu.Unlock()
			return nil, ErrClosed
		}
		if err := ctx.Err(); err != nil {
			manager.mu.Unlock()
			return nil, err
		}
		lease := manager.hashes[identity.SHA256]
		if !lease.working {
			lease.working = true
			manager.mu.Unlock()
			return func() {
				manager.mu.Lock()
				lease.working = false
				close(lease.changed)
				lease.changed = make(chan struct{})
				if lease.refs == 0 {
					delete(manager.hashes, identity.SHA256)
				}
				manager.mu.Unlock()
			}, nil
		}
		changed := lease.changed
		manager.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (manager *Manager) publish(
	ctx context.Context,
	pin PinnedContent,
	identity jobcontract.ContentIdentity,
) (Generation, error) {
	object := pin.Object()
	validObject := object.StoreID == manager.store.ID && object.Content == identity
	if object.Validate() != nil || !validObject {
		return Generation{}, ErrCorrupt
	}
	if err := pin.Check(); err != nil {
		return Generation{}, err
	}
	generation, _, err := manager.ledger.PublishContent(ctx, Publication{Object: object, At: time.Now().UTC()})
	if err != nil {
		return Generation{}, err
	}
	if generation.Validate() != nil || generation.Object != object {
		return Generation{}, ErrCorrupt
	}
	if err := ctx.Err(); err != nil {
		return Generation{}, err
	}
	return generation, pin.Check()
}

// Upload owns body, including failures before reading. Close must unblock Read;
// this is the cancellation contract required of the HTTP request body adapter.
func (manager *Manager) Upload(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	body io.ReadCloser,
) (UploadResult, error) {
	if body == nil {
		return UploadResult{}, ErrInvalid
	}
	owned := &ownedInput{ReadCloser: body}
	defer owned.Close()
	if identity.Validate() != nil {
		return UploadResult{}, ErrInvalid
	}
	operation, end, err := manager.begin(ctx)
	if err != nil {
		return UploadResult{}, err
	}
	defer end()
	// Join a cancellation-triggered source Close before releasing the operation.
	// Manager.Close must not return while an owned input is still closing.
	defer owned.Close()
	stop := context.AfterFunc(operation, func() { _ = owned.Close() })
	defer stop()
	if err := manager.retain(identity, false); err != nil {
		return UploadResult{}, err
	}
	defer manager.release(identity, false)
	unlock, err := manager.work(operation, identity)
	if err != nil {
		return UploadResult{}, err
	}
	defer unlock()
	created, err := manager.physical.PutContent(operation, identity, owned)
	if err != nil {
		return UploadResult{}, err
	}
	if err := owned.Close(); err != nil {
		return UploadResult{}, err
	}
	if err := operation.Err(); err != nil {
		return UploadResult{}, err
	}
	pin, err := manager.physical.OpenPinned(operation, identity)
	if err != nil {
		return UploadResult{}, err
	}
	generation, publishErr := manager.publish(operation, pin, identity)
	closeErr := pin.Close()
	if err := errors.Join(publishErr, closeErr); err != nil {
		return UploadResult{}, err
	}
	return UploadResult{PhysicalCreated: created, Generation: generation}, nil
}

type ownedInput struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (input *ownedInput) Close() error {
	input.once.Do(func() { input.err = input.ReadCloser.Close() })
	return input.err
}

func (manager *Manager) Close() error {
	manager.closeOnce.Do(func() {
		manager.mu.Lock()
		manager.closed = true
		resources := make([]*resources, 0, len(manager.resources))
		for resource := range manager.resources {
			resources = append(resources, resource)
		}
		manager.mu.Unlock()
		manager.cancel()
		for _, resource := range resources {
			manager.closeErr = errors.Join(manager.closeErr, resource.Close())
		}
		manager.operations.Wait()
	})
	return manager.closeErr
}
