package jobcontent

import (
	"context"
	"errors"
	"sync"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type assetStream struct {
	resource *resources
	pin      PinnedContent
	claim    Claim
	ctx      context.Context
	cancel   context.CancelFunc
	stop     func() bool
	stopCall func()
	done     chan struct{}
	once     sync.Once
	stopOnce sync.Once
	closeErr error
}

func (resource *resources) Open(ctx context.Context, ref string) (AssetStream, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	manager := resource.manager
	manager.mu.Lock()
	live := resource.state == resourceBound && resource.ctx.Err() == nil
	if manager.closed || !live {
		manager.mu.Unlock()
		return nil, ErrClosed
	}
	var selected *anchor
	for index := range resource.anchors {
		if resource.anchors[index].claim.Ref == ref {
			selected = &resource.anchors[index]
			break
		}
	}
	if selected == nil {
		manager.mu.Unlock()
		return nil, ErrNotFound
	}
	resource.opening.Add(1)
	manager.mu.Unlock()
	defer resource.opening.Done()
	lifetime, cancel := context.WithCancel(resource.ctx)
	stopCall := cancelOn(ctx, cancel)
	identity := selected.claim.Object.Content
	retained := false
	transferred := false
	defer func() {
		if !transferred {
			stopCall()
			cancel()
			if retained {
				manager.release(identity, true)
			}
		}
	}()
	operation, end, err := manager.begin(lifetime)
	if err != nil {
		return nil, err
	}
	defer end()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := manager.retain(identity, true); err != nil {
		return nil, err
	}
	retained = true
	unlock, err := manager.work(operation, identity)
	if err != nil {
		return nil, err
	}
	defer unlock()
	// The lifetime belongs to the reader, not the short operation. Ending this
	// open must not cancel a successfully returned independent stream.
	pin, err := manager.physical.OpenPinned(lifetime, identity)
	if err != nil {
		return nil, err
	}
	if pin.Object() != selected.claim.Object {
		return nil, errors.Join(ErrConflict, pin.Close())
	}
	if err := pin.Check(); err != nil {
		return nil, errors.Join(err, pin.Close())
	}
	stream := &assetStream{
		resource: resource, pin: pin, claim: selected.claim, ctx: lifetime,
		cancel: cancel, stopCall: stopCall, done: make(chan struct{}),
	}
	manager.mu.Lock()
	live = resource.state == resourceBound && lifetime.Err() == nil
	if manager.closed || !live {
		manager.mu.Unlock()
		return nil, errors.Join(ErrClosed, pin.Close())
	}
	resource.borrowers[stream] = struct{}{}
	resource.borrowed.Add(1)
	resource.callbacks.Add(1)
	// The callback only uses closeInternal. Public Close joins it separately.
	stream.stop = context.AfterFunc(lifetime, func() {
		defer resource.callbacks.Done()
		defer close(stream.done)
		_ = stream.closeInternal()
	})
	manager.mu.Unlock()
	transferred = true
	return stream, nil
}

func (stream *assetStream) Identity() jobcontract.ContentIdentity { return stream.claim.Object.Content }
func (stream *assetStream) MediaType() string                     { return stream.claim.MediaType }

func (stream *assetStream) Read(buffer []byte) (int, error) {
	if err := stream.ctx.Err(); err != nil {
		return 0, err
	}
	return stream.pin.Read(buffer)
}

func (stream *assetStream) closeInternal() error {
	stream.once.Do(func() {
		stream.cancel()
		stream.closeErr = stream.pin.Close()
		manager := stream.resource.manager
		manager.mu.Lock()
		delete(stream.resource.borrowers, stream)
		manager.mu.Unlock()
		manager.release(stream.claim.Object.Content, true)
		stream.resource.borrowed.Done()
	})
	return stream.closeErr
}

func (stream *assetStream) Close() error {
	// Joining the caller link precedes releasing the stream. Closing a Job also
	// joins callbacks whose readers have already left its borrower map.
	stream.stopCall()
	err := stream.closeInternal()
	stream.stopOnce.Do(func() {
		if stream.stop() {
			close(stream.done)
			stream.resource.callbacks.Done()
		} else {
			<-stream.done
		}
	})
	return err
}
