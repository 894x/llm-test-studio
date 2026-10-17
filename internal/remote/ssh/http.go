package ssh

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const maxRunnerEndpoints = 4

// runnerHTTP owns one endpoint's management pool and its lazy content pool.
// Requests remain owned until their response is consumed or closed.
type runnerHTTP struct {
	parent     *Client
	address    string
	mu         sync.Mutex
	closed     bool
	done       chan struct{}
	management *http.Client
	bulk       *http.Client
	requests   map[*runnerCall]struct{}
	wg         sync.WaitGroup
}

// RunnerHTTP opens one endpoint owner, using SSH direct-tcpip to one loopback
// port. It creates no local listener and never uses a proxy or redirects.
func (client *Client) RunnerHTTP(port uint16) (*http.Client, error) {
	if client == nil || port == 0 {
		return nil, ErrInvalid
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.closed {
		return nil, ErrConnection
	}
	if len(client.endpoints) >= maxRunnerEndpoints {
		return nil, ErrInvalid
	}
	owner := &runnerHTTP{
		parent: client, address: net.JoinHostPort("127.0.0.1", strconv.Itoa(int(port))),
		done: make(chan struct{}), requests: map[*runnerCall]struct{}{},
	}
	owner.management = owner.newClient(false)
	client.endpoints = append(client.endpoints, owner)
	return owner.management, nil
}

func (owner *runnerHTTP) newClient(bulk bool) *http.Client {
	headerTimeout, timeout := 25*time.Second, 2*time.Minute
	if bulk {
		headerTimeout, timeout = jobcontract.AssetResponseHeaderTimeout, 0
	}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, requested string) (net.Conn, error) {
			if network != "tcp" || requested != owner.address {
				return nil, ErrInvalid
			}
			ctx, cancel := context.WithTimeout(ctx, connectTimeout)
			defer cancel()
			// net/http detaches pooled dials from request cancellation. Bound a
			// withheld channel-open reply by its initiating request as well.
			if request, ok := ctx.Value(dialOwnerKey{}).(context.Context); ok {
				stop := context.AfterFunc(request, cancel)
				defer stop()
			}
			connection, err := owner.parent.dialRunner(ctx, owner.address)
			if err != nil {
				return nil, connectionError(ctx, err)
			}
			return connection, nil
		},
		MaxIdleConns: 8, MaxIdleConnsPerHost: 8, MaxConnsPerHost: 16,
		IdleConnTimeout: time.Minute, ResponseHeaderTimeout: headerTimeout,
		MaxResponseHeaderBytes: 16 << 10,
	}
	return &http.Client{
		Transport: runnerTransport{owner: owner, transport: transport}, Timeout: timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (client *Client) bulkRunnerHTTP(management *http.Client) (*http.Client, error) {
	if client == nil || management == nil {
		return nil, ErrInvalid
	}
	transport, ok := management.Transport.(runnerTransport)
	if !ok || transport.owner.parent != client {
		return nil, ErrInvalid
	}
	owner := transport.owner
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed {
		return nil, ErrConnection
	}
	if owner.bulk == nil {
		owner.bulk = owner.newClient(true)
	}
	return owner.bulk, nil
}

func (client *Client) releaseRunnerHTTP(management *http.Client) {
	if management == nil {
		return
	}
	transport, ok := management.Transport.(runnerTransport)
	if !ok || transport.owner.parent != client {
		return
	}
	owner := transport.owner
	owner.close()
	client.mu.Lock()
	defer client.mu.Unlock()
	for index, current := range client.endpoints {
		if current == owner {
			client.endpoints = append(client.endpoints[:index], client.endpoints[index+1:]...)
			return
		}
	}
}

func (owner *runnerHTTP) close() {
	owner.mu.Lock()
	if owner.closed {
		owner.mu.Unlock()
		<-owner.done
		return
	}
	owner.closed = true
	requests := make([]*runnerCall, 0, len(owner.requests))
	for request := range owner.requests {
		requests = append(requests, request)
	}
	bulk := owner.bulk
	owner.mu.Unlock()
	for _, request := range requests {
		request.close()
	}
	owner.management.CloseIdleConnections()
	if bulk != nil {
		bulk.CloseIdleConnections()
	}
	owner.wg.Wait()
	close(owner.done)
}

type dialOwnerKey struct{}

type runnerTransport struct {
	owner     *runnerHTTP
	transport *http.Transport
}

func (transport runnerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	call := &runnerCall{owner: transport.owner, cancel: cancel, source: request.Body}
	transport.owner.mu.Lock()
	if transport.owner.closed {
		transport.owner.mu.Unlock()
		call.close()
		return nil, ErrConnection
	}
	transport.owner.requests[call] = struct{}{}
	transport.owner.wg.Add(1)
	transport.owner.mu.Unlock()
	stop := context.AfterFunc(ctx, call.close)
	call.mu.Lock()
	call.stop = stop
	call.mu.Unlock()
	ctx = context.WithValue(ctx, dialOwnerKey{}, ctx)
	response, err := transport.transport.RoundTrip(request.Clone(ctx))
	if err != nil {
		call.close()
		call.finish()
		return nil, err
	}
	call.mu.Lock()
	closed := call.closed
	if !closed {
		call.response = response.Body
	}
	call.mu.Unlock()
	if closed {
		_ = response.Body.Close()
		call.finish()
		return nil, ctx.Err()
	}
	response.Body = &runnerResponse{ReadCloser: response.Body, call: call}
	return response, nil
}

func (transport runnerTransport) CloseIdleConnections() { transport.transport.CloseIdleConnections() }

type runnerCall struct {
	owner    *runnerHTTP
	cancel   context.CancelFunc
	source   io.ReadCloser
	mu       sync.Mutex
	closed   bool
	response io.ReadCloser
	stop     func() bool
	closeOne sync.Once
	doneOne  sync.Once
}

func (call *runnerCall) close() {
	call.closeOne.Do(func() {
		call.mu.Lock()
		call.closed = true
		response := call.response
		call.mu.Unlock()
		call.cancel()
		if call.source != nil {
			_ = call.source.Close()
		}
		if response != nil {
			_ = response.Close()
		}
	})
	call.mu.Lock()
	response := call.response
	call.mu.Unlock()
	if response != nil {
		call.finish()
	}
}

func (call *runnerCall) finish() {
	call.doneOne.Do(func() {
		call.mu.Lock()
		stop := call.stop
		call.mu.Unlock()
		if stop != nil {
			stop()
		}
		call.owner.mu.Lock()
		delete(call.owner.requests, call)
		call.owner.mu.Unlock()
		call.owner.wg.Done()
	})
}

type runnerResponse struct {
	io.ReadCloser
	call    *runnerCall
	stateMu sync.Mutex
	eof     bool
	closed  bool
}

func (response *runnerResponse) Read(destination []byte) (int, error) {
	response.stateMu.Lock()
	complete := response.eof && !response.closed
	response.stateMu.Unlock()
	if complete {
		return 0, io.EOF
	}
	count, err := response.ReadCloser.Read(destination)
	if err == io.EOF {
		// Automatic resource release must not change a later EOF probe into a
		// read on the now-closed body. Explicit Close still ends this reader.
		response.stateMu.Lock()
		response.eof = true
		response.stateMu.Unlock()
	}
	if err != nil {
		response.call.close()
	}
	return count, err
}

func (response *runnerResponse) Close() error {
	response.stateMu.Lock()
	response.closed = true
	response.stateMu.Unlock()
	response.call.close()
	return nil
}
