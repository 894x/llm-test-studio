package protocols

import (
	"context"
	"io"
	"sync"
)

// A call remains active through response-body reads, including direct Send
// callers that do not enter Execute.
type clientCall struct {
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool
	once   sync.Once
	state  *clientState
}

func (client *Client) beginCall(ctx context.Context) (*clientCall, error) {
	if client == nil || client.state == nil {
		return nil, ErrClientClosed
	}
	if ctx == nil {
		return nil, ErrInvalidExecution
	}
	state := client.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return nil, ErrClientClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state.active.Add(1)
	callContext, cancel := context.WithCancel(ctx)
	return &clientCall{
		ctx: callContext, cancel: cancel, state: state,
		stop: context.AfterFunc(state.lifecycle, cancel),
	}, nil
}

func (call *clientCall) finish() {
	call.once.Do(func() {
		call.stop()
		call.cancel()
		call.state.active.Done()
	})
}

type responseBody struct {
	body   io.ReadCloser
	call   *clientCall
	mu     sync.Mutex
	reads  sync.WaitGroup
	closed bool
	done   chan struct{}
	err    error
}

func ownResponseBody(body io.ReadCloser, call *clientCall) *responseBody {
	owned := &responseBody{body: body, call: call, done: make(chan struct{})}
	context.AfterFunc(call.ctx, func() { _ = owned.Close() })
	return owned
}

func (body *responseBody) Read(destination []byte) (int, error) {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	body.reads.Add(1)
	body.mu.Unlock()
	count, err := body.body.Read(destination)
	body.reads.Done()
	if err == io.EOF {
		_ = body.Close()
	}
	return count, err
}

func (body *responseBody) Close() error {
	body.mu.Lock()
	if body.closed {
		body.mu.Unlock()
		<-body.done
		return body.err
	}
	body.closed = true
	body.mu.Unlock()
	body.err = body.body.Close()
	body.reads.Wait()
	body.call.finish()
	close(body.done)
	return body.err
}
