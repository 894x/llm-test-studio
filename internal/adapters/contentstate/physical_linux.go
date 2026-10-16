// Package contentstate adapts the runner's private Linux content store.
package contentstate

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type Config struct {
	State          *runnerstate.State
	InstallationID string
	Now            func() time.Time
}

type Adapter struct {
	state  *runnerstate.State
	marker *runnerstate.ContentStoreMarker
	store  jobcontent.Store
}

func New(ctx context.Context, config Config) (*Adapter, error) {
	if ctx == nil || config.State == nil || !domain.IsUUID(config.InstallationID) {
		return nil, jobcontent.ErrInvalid
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	marker, err := config.State.EnsureContentStore(ctx, config.InstallationID, now())
	if err != nil {
		return nil, physicalError(err)
	}
	identity := marker.Identity()
	store := jobcontent.Store{
		ID: identity.StoreID, Kind: jobcontent.StoreKindExecutor,
		InstallationID: identity.InstallationID, CreatedAt: identity.CreatedAt,
	}
	if err := store.Validate(); err != nil {
		return nil, err
	}
	return &Adapter{state: config.State, marker: marker, store: store}, nil
}

func (adapter *Adapter) Store() jobcontent.Store { return adapter.store }

func (adapter *Adapter) Ready(ctx context.Context) error {
	if ctx == nil || adapter == nil || adapter.marker == nil {
		return jobcontent.ErrInvalid
	}
	return physicalError(adapter.marker.Check(ctx))
}

func (adapter *Adapter) PutContent(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
	source io.Reader,
) (bool, error) {
	if identity.Validate() != nil || source == nil {
		return false, jobcontent.ErrInvalid
	}
	if err := adapter.Ready(ctx); err != nil {
		return false, err
	}
	created, err := adapter.state.PutContent(ctx, identity, source)
	if err != nil {
		return created, physicalError(err)
	}
	return created, adapter.Ready(ctx)
}

func (adapter *Adapter) OpenPinned(
	ctx context.Context,
	identity jobcontract.ContentIdentity,
) (jobcontent.PinnedContent, error) {
	if identity.Validate() != nil {
		return nil, jobcontent.ErrInvalid
	}
	if err := adapter.Ready(ctx); err != nil {
		return nil, err
	}
	reader, err := adapter.state.OpenContent(ctx, identity)
	if err != nil {
		return nil, physicalError(err)
	}
	facts, err := reader.PhysicalIdentity(adapter.store.ID)
	if err != nil {
		return nil, errors.Join(physicalError(err), reader.Close())
	}
	object := jobcontent.ObjectIdentity{
		StoreID: adapter.store.ID, Content: identity, IncarnationID: facts.IncarnationID,
		WitnessSHA256: facts.WitnessSHA256, ChangeToken: facts.ChangeToken,
	}
	pinned := &pinnedContent{reader: reader, marker: adapter.marker, ctx: ctx, object: object}
	if err := object.Validate(); err != nil {
		return nil, errors.Join(err, reader.Close())
	}
	if err := pinned.Check(); err != nil {
		return nil, errors.Join(err, reader.Close())
	}
	return pinned, nil
}

type pinnedContent struct {
	mu        sync.Mutex
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
	reader    *runnerstate.ContentReader
	marker    *runnerstate.ContentStoreMarker
	ctx       context.Context
	object    jobcontent.ObjectIdentity
}

func (content *pinnedContent) Object() jobcontent.ObjectIdentity { return content.object }
func (content *pinnedContent) Read(buffer []byte) (int, error) {
	content.mu.Lock()
	defer content.mu.Unlock()
	if err := content.check(); err != nil {
		return 0, err
	}
	n, err := content.reader.Read(buffer)
	if checkErr := content.check(); checkErr != nil {
		return 0, checkErr
	}
	return n, err
}
func (content *pinnedContent) Seek(offset int64, whence int) (int64, error) {
	content.mu.Lock()
	defer content.mu.Unlock()
	if err := content.check(); err != nil {
		return 0, err
	}
	position, err := content.reader.Seek(offset, whence)
	if checkErr := content.check(); checkErr != nil {
		return 0, checkErr
	}
	return position, err
}
func (content *pinnedContent) Close() error {
	content.closeOnce.Do(func() {
		content.closed.Store(true)
		content.closeErr = content.reader.Close()
		content.mu.Lock()
		content.mu.Unlock()
	})
	return content.closeErr
}

func (content *pinnedContent) Check() error {
	content.mu.Lock()
	defer content.mu.Unlock()
	return content.check()
}

func (content *pinnedContent) check() error {
	if content.closed.Load() {
		return os.ErrClosed
	}
	if err := content.marker.Check(content.ctx); err != nil {
		return physicalError(err)
	}
	return physicalError(content.reader.Check())
}

func physicalError(err error) error {
	if errors.Is(err, runnerstate.ErrContentCorrupt) || errors.Is(err, runnerstate.ErrInvalid) {
		return errors.Join(jobcontent.ErrCorrupt, err)
	}
	return err
}

var _ jobcontent.PhysicalStore = (*Adapter)(nil)
var _ jobcontent.PinnedContent = (*pinnedContent)(nil)
