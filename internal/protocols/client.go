package protocols

import (
	"bytes"
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
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

var ErrInvalidBinding = errors.New("invalid protocol run binding")
var ErrClientClosed = errors.New("protocol client is closed")
var ErrInvalidExecution = errors.New("invalid protocol execution configuration")

type Client struct{ state *clientState }

type clientState struct {
	mu        sync.Mutex
	registry  *Registry
	channel   domain.ChannelSnapshot
	secret    []byte
	http      *http.Client
	closed    bool
	lifecycle context.Context
	cancel    context.CancelFunc
	active    sync.WaitGroup
}

type Execution struct {
	Spec     testspec.Spec
	Inputs   map[string]json.RawMessage
	Random   testspec.RandomContext
	Settings testspec.RunSettings
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
	if _, exists := registry.lookup(string(channel.Protocol)); !exists {
		return nil, ErrInvalidBinding
	}
	client := &Client{state: &clientState{registry: registry, channel: channel}}
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
	if client == nil || client.state == nil {
		return testspec.Result{}, ErrClientClosed
	}
	state := client.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return testspec.Result{}, ErrClientClosed
	}
	state.active.Add(1)
	state.mu.Unlock()
	defer state.active.Done()
	module, exists := state.registry.lookup(string(state.channel.Protocol))
	if !exists || module.Validate(execution.Spec) != nil || execution.Settings.Validate() != nil {
		return testspec.Result{}, ErrInvalidExecution
	}
	inputs, err := testspec.ValidateInputs(execution.Spec.Inputs, execution.Inputs)
	if err != nil {
		return testspec.Result{}, err
	}
	timeout := execution.Settings.TimeoutMS
	if timeout == 0 {
		timeout = 600_000
	}
	runContext, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
	stop := context.AfterFunc(state.lifecycle, cancel)
	defer func() { stop(); cancel() }()
	observation := module.Execute(runContext, runtime.Execution{
		Spec: execution.Spec, Inputs: inputs, Random: execution.Random,
		Settings: execution.Settings, Transport: client,
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
	state := client.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return runtime.Response{}, ErrClientClosed
	}
	secret := string(state.secret)
	state.mu.Unlock()
	body := append(json.RawMessage{}, request.Body...)
	if len(body) > 0 {
		var object map[string]json.RawMessage
		if json.Unmarshal(body, &object) != nil || object == nil {
			return runtime.Response{}, ErrInvalidExecution
		}
		if _, exists := object["model"]; exists {
			return runtime.Response{}, ErrInvalidExecution
		}
		object["model"], _ = json.Marshal(state.channel.UpstreamModelName)
		body, _ = json.Marshal(object)
	}
	baseURL := strings.TrimRight(state.channel.BaseURL, "/")
	path := request.Path
	// The binding may already contain /v1. This is external endpoint joining,
	// not a project format fallback.
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(path, "/v1/") {
		path = strings.TrimPrefix(path, "/v1")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, baseURL+path, bytes.NewReader(body))
	if err != nil {
		return runtime.Response{}, ErrInvalidExecution
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
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() { traceMu.Lock(); firstByte = runtime.Milliseconds(started); traceMu.Unlock() }}
	httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(ctx, trace))
	response, err := state.http.Do(httpRequest)
	traceMu.Lock()
	ttfb := firstByte
	traceMu.Unlock()
	result := runtime.Response{HTTP: response, Started: started, TTFBMS: ttfb, RequestBody: body}
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return result, errors.New("protocol transport failed")
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
	return nil
}

func (Client) String() string   { return "[protocol client]" }
func (Client) GoString() string { return "[protocol client]" }
func (Client) MarshalJSON() ([]byte, error) {
	return nil, errors.New("protocol clients cannot be serialized")
}
