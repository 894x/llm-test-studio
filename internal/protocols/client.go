package protocols

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

var ErrInvalidBinding = errors.New("invalid protocol run binding")
var ErrClientClosed = errors.New("protocol client is closed")
var ErrInvalidExecution = errors.New("invalid protocol execution configuration")

type Client struct{ state *clientState }

type clientState struct {
	mu        sync.Mutex
	module    runtime.Module
	channel   domain.ChannelSnapshot
	secret    []byte
	http      *http.Client
	closed    bool
	lifecycle context.Context
	cancel    context.CancelFunc
	active    sync.WaitGroup
	closeDone chan struct{}
}

type Execution struct {
	Spec     testspec.Spec
	Inputs   map[string]json.RawMessage
	Random   testspec.RandomContext
	Settings testspec.RunSettings
	Assets   runtime.AssetResolver
}

func NewClient(
	registry *Registry,
	lease *credentials.Lease,
	channel domain.ChannelSnapshot,
	transport http.RoundTripper,
) (*Client, error) {
	if registry == nil || lease == nil || channel.Validate() != nil {
		return nil, ErrInvalidBinding
	}
	module, exists := registry.lookup(string(channel.Protocol))
	if !exists {
		return nil, ErrInvalidBinding
	}
	client := &Client{state: &clientState{module: module, channel: channel, closeDone: make(chan struct{})}}
	secret, err := lease.Bytes()
	if err != nil || len(secret) == 0 {
		clear(secret)
		return nil, ErrInvalidBinding
	}
	if transport == nil {
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			clear(secret)
			return nil, ErrInvalidBinding
		}
		transport = base.Clone()
	}
	client.state.secret = secret
	client.state.lifecycle, client.state.cancel = context.WithCancel(context.Background())
	client.state.http = &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return client, nil
}

func (client *Client) Execute(ctx context.Context, execution Execution) (testspec.Result, error) {
	call, err := client.beginCall(ctx)
	if err != nil {
		return testspec.Result{}, err
	}
	defer call.finish()
	state := client.state
	if execution.Spec.Validate() != nil || execution.Settings.Validate() != nil {
		return testspec.Result{}, ErrInvalidExecution
	}
	if err := state.module.ValidateProtocol(execution.Spec); err != nil {
		if errors.Is(err, testspec.ErrFileInputsUnsupported) {
			return testspec.Result{}, err
		}
		return testspec.Result{}, ErrInvalidExecution
	}
	inputs, err := testspec.ValidateInputs(execution.Spec.Inputs, execution.Inputs)
	if err != nil {
		return testspec.Result{}, err
	}
	if len(inputs.Files) != 0 && execution.Assets == nil {
		return testspec.Result{}, testspec.ErrFileInputsUnsupported
	}
	timeout := execution.Settings.TimeoutMS
	if timeout == 0 {
		timeout = 600_000
	}
	runContext, cancel := context.WithTimeout(call.ctx, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	observation := state.module.Execute(runContext, runtime.Execution{
		Spec: execution.Spec, Inputs: inputs, Random: execution.Random,
		Settings: execution.Settings, Transport: client, Assets: execution.Assets,
	})
	observation.Model = state.channel.UpstreamModelName
	verdict := testspec.Evaluate(execution.Spec.Assertions, observation)
	result := testspec.Result{Observation: observation, Verdict: verdict}
	state.mu.Lock()
	secret := string(state.secret)
	state.mu.Unlock()
	return sanitize(result, secret), nil
}

func (client *Client) Send(ctx context.Context, request runtime.Request) (runtime.Response, error) {
	call, err := client.beginCall(ctx)
	if err != nil {
		return runtime.Response{}, err
	}
	transferred := false
	defer func() {
		if !transferred {
			call.finish()
		}
	}()
	state := client.state
	state.mu.Lock()
	secret := string(state.secret)
	state.mu.Unlock()
	descriptor, _ := protocol.Lookup(string(state.channel.Protocol))
	address, err := protocol.ResolveAddress(state.channel.BaseURL, request.Path, descriptor.RequestPaths)
	if err != nil {
		return runtime.Response{}, ErrInvalidExecution
	}
	wire, err := prepareWire(call.ctx, request, state.channel)
	if err != nil {
		return runtime.Response{}, err
	}
	defer wire.Close()
	length := int64(len(wire.data))
	httpRequest, err := http.NewRequestWithContext(call.ctx, request.Method, address.Endpoint, wire)
	if err != nil {
		return runtime.Response{}, ErrInvalidExecution
	}
	httpRequest.ContentLength = length
	if length == 0 {
		httpRequest.Body = http.NoBody
	}
	httpRequest.Header.Set("Authorization", "Bearer "+secret)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json, text/event-stream")
	httpRequest.Header.Set("User-Agent", "llm-test-studio")
	for name, value := range request.Headers {
		if strings.EqualFold(name, "Authorization") {
			return runtime.Response{}, ErrInvalidExecution
		}
		httpRequest.Header.Set(name, value)
	}
	started := time.Now()
	var firstByte float64
	var traceMu sync.Mutex
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() {
		traceMu.Lock()
		firstByte = runtime.Milliseconds(started)
		traceMu.Unlock()
	}}
	httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(call.ctx, trace))
	response, err := state.http.Do(httpRequest)
	closeErr := wire.Close()
	traceMu.Lock()
	ttfb := firstByte
	traceMu.Unlock()
	result := runtime.Response{
		HTTP: response, Started: started, TTFBMS: ttfb, RequestBody: wire.view,
		PreparedWire: wire.metadata,
	}
	if err != nil || closeErr != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if call.ctx.Err() != nil {
			return result, call.ctx.Err()
		}
		return result, errors.New("protocol transport failed")
	}
	if response != nil && response.Body != nil {
		response.Body = ownResponseBody(response.Body, call)
		transferred = true
	}
	return result, nil
}

func (client *Client) Close() error {
	if client == nil || client.state == nil {
		return nil
	}
	state := client.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		<-state.closeDone
		return nil
	}
	state.closed = true
	state.cancel()
	state.mu.Unlock()
	state.active.Wait()
	state.mu.Lock()
	clear(state.secret)
	state.secret = nil
	state.mu.Unlock()
	state.http.CloseIdleConnections()
	close(state.closeDone)
	return nil
}

func (Client) String() string   { return "[protocol client]" }
func (Client) GoString() string { return "[protocol client]" }
func (Client) MarshalJSON() ([]byte, error) {
	return nil, errors.New("protocol clients cannot be serialized")
}
