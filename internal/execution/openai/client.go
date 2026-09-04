// Package openai implements the OpenAI-compatible HTTP execution boundary.
// It classifies transport/protocol failures into stable load error codes and
// never returns response bodies, request bodies, URLs, or credentials in an
// Observation.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const (
	// Response budgets are per request. The load scheduler owns the independent
	// process-wide admission cap (load.MaxOpenLoopInFlight); callers must lower
	// one or both limits when their memory budget requires it.
	defaultMaxResponseBytes int64 = 32 << 20
	maxAllowedResponseBytes int64 = 256 << 20

	// MaxSSELineBytes and MaxSSEEventBytes cap transient parser allocations
	// independently of the total response limit. MaxSSELines caps parser work.
	MaxSSELineBytes  int64 = 64 << 10
	MaxSSEEventBytes int64 = 256 << 10
	MaxSSELines      int64 = 32_768
)

var (
	ErrSecretSerialization   = errors.New("openai client contains credential material and cannot be serialized")
	ErrInvalidChannel        = errors.New("invalid OpenAI channel configuration")
	ErrUnsupportedProtocol   = errors.New("unsupported OpenAI channel protocol")
	ErrCredentialRequired    = errors.New("OpenAI credential lease is required")
	ErrCredentialUnavailable = errors.New("OpenAI credential is unavailable")
	ErrInvalidTransport      = errors.New("invalid OpenAI HTTP transport")
	ErrInsecureEndpoint      = errors.New("OpenAI endpoint must use HTTPS")
	ErrInvalidOption         = errors.New("invalid OpenAI client option")
	ErrInvalidRequest        = errors.New("invalid OpenAI request definition")
	ErrClientClosed          = errors.New("OpenAI client is closed")
)

type Option func(*Client) error

func WithMaxResponseBytes(limit int64) Option {
	return func(client *Client) error {
		if limit < 1 || limit > maxAllowedResponseBytes {
			return ErrInvalidOption
		}
		client.state.maxResponseBytes = limit
		return nil
	}
}

// WithLoopbackHTTPForTesting permits plaintext HTTP only for localhost or a
// literal loopback address. It is intentionally explicit and cannot weaken
// transport security for remote endpoints.
func WithLoopbackHTTPForTesting() Option {
	return func(client *Client) error {
		client.state.allowLoopbackHTTP = true
		return nil
	}
}

type Client struct {
	state *clientState
}

// clientState contains all mutable and credential-bearing state behind the
// exported opaque handle. Copying Client therefore copies only this pointer;
// default reflection formatting can never walk into the secret byte slice.
type clientState struct {
	mu sync.Mutex

	baseURL                 string
	upstreamModel           string
	secret                  []byte
	httpClient              *http.Client
	maxResponseBytes        int64
	maxSSELineBytes         int64
	maxSSEEventBytes        int64
	maxSSELines             int64
	maxFailureEvidenceBytes int64
	failureEvidenceSink     FailureResponseEvidenceSink
	allowLoopbackHTTP       bool

	lifecycleContext context.Context
	cancelLifecycle  context.CancelFunc
	inFlight         sync.WaitGroup
	closed           bool
}

// NewClient constructs an executor-owned HTTP client. Callers may supply a
// transport (for proxy/TLS/test configuration), but never an http.Client, so
// redirect behavior remains under this package's control.
func NewClient(channel domain.ChannelSnapshot, lease *credentials.Lease, transport http.RoundTripper, options ...Option) (*Client, error) {
	if err := channel.Validate(); err != nil {
		return nil, ErrInvalidChannel
	}
	if channel.Protocol != domain.ProtocolOpenAIChat && channel.Protocol != domain.ProtocolKimiK3 {
		return nil, ErrUnsupportedProtocol
	}
	if lease == nil {
		return nil, ErrCredentialRequired
	}
	if transport != nil && isNilInterface(transport) {
		return nil, ErrInvalidTransport
	}

	client := &Client{state: &clientState{
		baseURL:          strings.TrimRight(channel.BaseURL, "/"),
		upstreamModel:    channel.UpstreamModelName,
		maxResponseBytes: defaultMaxResponseBytes,
		maxSSELineBytes:  MaxSSELineBytes,
		maxSSEEventBytes: MaxSSEEventBytes,
		maxSSELines:      MaxSSELines,
	}}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := applyOption(option, client); err != nil {
			return nil, ErrInvalidOption
		}
	}
	if !secureEndpoint(client.state.baseURL, client.state.allowLoopbackHTTP) {
		return nil, ErrInsecureEndpoint
	}

	secret, err := lease.Bytes()
	if err != nil || len(secret) == 0 {
		clear(secret)
		return nil, ErrCredentialUnavailable
	}
	if transport == nil {
		defaultTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok || defaultTransport == nil {
			clear(secret)
			return nil, ErrInvalidTransport
		}
		transport = defaultTransport.Clone()
	}
	client.state.secret = secret
	client.state.lifecycleContext, client.state.cancelLifecycle = context.WithCancel(context.Background())
	client.state.httpClient = &http.Client{
		Transport: ownedTransport{base: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client, nil
}

func applyOption(option Option, client *Client) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrInvalidOption
		}
	}()
	return option(client)
}

func secureEndpoint(rawURL string, allowLoopbackHTTP bool) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

type preparedRequest struct {
	method   string
	url      string
	headers  map[string]string
	body     []byte
	stream   bool
	endpoint endpointKind
	probe    ProbeClassifier
}

type ProbeClassifier func([]byte) (map[string]string, error)

type endpointKind uint8

const (
	endpointUnknown endpointKind = iota
	endpointChatCompletions
	endpointModels
	endpointEmbeddings
)

func classifyEndpoint(request domain.TestRequest) endpointKind {
	switch {
	case request.Method == domain.RequestPOST &&
		(request.Path == "/v1/chat/completions" || request.Path == "/chat/completions"):
		return endpointChatCompletions
	case request.Method == domain.RequestGET &&
		(request.Path == "/v1/models" || request.Path == "/models"):
		return endpointModels
	case request.Method == domain.RequestPOST &&
		(request.Path == "/v1/embeddings" || request.Path == "/embeddings"):
		return endpointEmbeddings
	default:
		return endpointUnknown
	}
}

func (client *Client) Executor(request domain.TestRequest) (load.Executor, error) {
	if client == nil || client.state == nil {
		return nil, ErrClientClosed
	}
	if err := request.Validate(); err != nil {
		return nil, ErrInvalidRequest
	}
	endpoint := classifyEndpoint(request)
	if endpoint == endpointUnknown {
		return nil, ErrInvalidRequest
	}

	state := client.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil, ErrClientClosed
	}
	baseURL := state.baseURL
	model := state.upstreamModel
	state.mu.Unlock()

	bodyObject, err := decodeObject(request.Body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if endpoint == endpointChatCompletions || endpoint == endpointEmbeddings {
		bodyObject["model"] = model
	}
	stream := false
	if configured, exists := bodyObject["stream"]; exists {
		var valid bool
		stream, valid = configured.(bool)
		if !valid {
			return nil, ErrInvalidRequest
		}
	}
	if stream && endpoint != endpointChatCompletions {
		return nil, ErrInvalidRequest
	}
	if stream {
		bodyObject["stream_options"] = map[string]any{"include_usage": true}
	}
	var body []byte
	if request.Method != domain.RequestGET || len(bodyObject) > 0 {
		body, err = json.Marshal(bodyObject)
		if err != nil {
			return nil, ErrInvalidRequest
		}
	}
	headers := make(map[string]string, len(request.Headers))
	for name, value := range request.Headers {
		headers[name] = value
	}
	prepared := preparedRequest{
		method: string(request.Method), url: baseURL + request.Path,
		headers: headers, body: body, stream: stream, endpoint: endpoint,
	}
	return func(ctx context.Context, scheduled load.Request) load.Observation {
		return client.execute(ctx, scheduled, prepared)
	}, nil
}

func (client *Client) ProbeExecutor(request domain.TestRequest, classifier ProbeClassifier) (load.Executor, error) {
	if client == nil || client.state == nil {
		return nil, ErrClientClosed
	}
	if classifier == nil || request.Validate() != nil {
		return nil, ErrInvalidRequest
	}
	endpoint := classifyEndpoint(request)
	if endpoint == endpointUnknown {
		return nil, ErrInvalidRequest
	}

	state := client.state
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil, ErrClientClosed
	}
	baseURL := state.baseURL
	model := state.upstreamModel
	state.mu.Unlock()

	bodyObject, err := decodeObject(request.Body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	if endpoint == endpointChatCompletions || endpoint == endpointEmbeddings {
		bodyObject["model"] = model
	}
	if configured, exists := bodyObject["stream"]; exists && configured != false {
		return nil, ErrInvalidRequest
	}
	var body []byte
	if request.Method != domain.RequestGET || len(bodyObject) > 0 {
		body, err = json.Marshal(bodyObject)
		if err != nil {
			return nil, ErrInvalidRequest
		}
	}
	headers := make(map[string]string, len(request.Headers))
	for name, value := range request.Headers {
		headers[name] = value
	}
	prepared := preparedRequest{
		method: string(request.Method), url: baseURL + request.Path,
		headers: headers, body: body, endpoint: endpoint, probe: classifier,
	}
	return func(ctx context.Context, scheduled load.Request) load.Observation {
		return client.execute(ctx, scheduled, prepared)
	}, nil
}

type executionState struct {
	owner                   *clientState
	callerContext           context.Context
	requestContext          context.Context
	cancelRequest           context.CancelFunc
	stopLifecycleWatch      func() bool
	httpClient              *http.Client
	secret                  []byte
	maxResponseBytes        int64
	maxSSELineBytes         int64
	maxSSEEventBytes        int64
	maxSSELines             int64
	maxFailureEvidenceBytes int64
	failureEvidenceSink     FailureResponseEvidenceSink
}

// ownedTransport normalizes the RoundTripper edge before net/http can discard
// a non-nil response returned alongside an error. That keeps body cleanup in
// the same panic-contained executor boundary.
type ownedTransport struct {
	base http.RoundTripper
}

func (transport ownedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil && response != nil {
		if response.Body != nil && !isNilInterface(response.Body) {
			_ = response.Body.Close()
		}
		return nil, err
	}
	return response, err
}

func (transport ownedTransport) CloseIdleConnections() {
	closer, ok := transport.base.(interface{ CloseIdleConnections() })
	if ok && !isNilInterface(closer) {
		closer.CloseIdleConnections()
	}
}

func (client *Client) beginExecution(ctx context.Context) (*executionState, bool) {
	if client == nil || client.state == nil {
		return nil, false
	}
	owner := client.state
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if owner.closed || len(owner.secret) == 0 {
		return nil, false
	}
	var (
		cancelRequest      context.CancelFunc
		stopLifecycleWatch func() bool
		secret             []byte
		admitted           bool
	)
	defer func() {
		if admitted {
			return
		}
		if stopLifecycleWatch != nil {
			stopLifecycleWatch()
		}
		if cancelRequest != nil {
			cancelRequest()
		}
		clear(secret)
	}()

	requestContext, cancelRequest := context.WithCancel(ctx)
	secret = append([]byte(nil), owner.secret...)
	stopLifecycleWatch = context.AfterFunc(owner.lifecycleContext, cancelRequest)
	state := &executionState{
		owner: owner, callerContext: ctx, requestContext: requestContext,
		cancelRequest: cancelRequest, httpClient: owner.httpClient,
		secret:           secret,
		maxResponseBytes: owner.maxResponseBytes, maxSSELineBytes: owner.maxSSELineBytes,
		maxSSEEventBytes: owner.maxSSEEventBytes, maxSSELines: owner.maxSSELines,
		maxFailureEvidenceBytes: owner.maxFailureEvidenceBytes, failureEvidenceSink: owner.failureEvidenceSink,
	}
	state.stopLifecycleWatch = stopLifecycleWatch
	// Add only after every context, secret, watcher, and state initialization
	// step has succeeded. Close shares owner.mu, so Wait cannot start between
	// this admission point and the returned execution state.
	owner.inFlight.Add(1)
	admitted = true
	return state, true
}

func (state *executionState) finish() {
	if state == nil {
		return
	}
	if state.stopLifecycleWatch != nil {
		state.stopLifecycleWatch()
	}
	state.cancelRequest()
	clear(state.secret)
	state.secret = nil
	state.owner.inFlight.Done()
}

func (state *executionState) classify(err error) (domain.ErrorCode, bool) {
	if code, timedOut, classified := state.contextClassification(); classified {
		return code, timedOut
	}
	type timeoutError interface{ Timeout() bool }
	var timeout timeoutError
	classified := err
	if urlError, ok := err.(*url.Error); ok {
		classified = urlError.Err
	}
	if errors.As(classified, &timeout) && timeout.Timeout() {
		return load.ErrorTimeout, true
	}
	return load.ErrorNetwork, false
}

func (state *executionState) contextClassification() (domain.ErrorCode, bool, bool) {
	if errors.Is(state.callerContext.Err(), context.DeadlineExceeded) {
		return load.ErrorTimeout, true, true
	}
	if errors.Is(state.callerContext.Err(), context.Canceled) {
		return load.ErrorCancelled, false, true
	}
	if state.owner.isClosed() {
		return load.ErrorClientClosed, false, true
	}
	if errors.Is(state.requestContext.Err(), context.DeadlineExceeded) {
		return load.ErrorTimeout, true, true
	}
	if errors.Is(state.requestContext.Err(), context.Canceled) {
		return load.ErrorCancelled, false, true
	}
	return "", false, false
}

func (client *Client) execute(ctx context.Context, scheduled load.Request, prepared preparedRequest) (observation load.Observation) {
	started := time.Now()
	observation = load.Observation{Index: scheduled.Index}
	var state *executionState
	// Register completion before the recovery defer. Defers run in reverse
	// order, so Close cannot cross its barrier until panic normalization and
	// final E2E measurement have both completed.
	defer func() {
		if state != nil {
			state.finish()
		}
	}()
	defer func() {
		elapsed := time.Since(started)
		if elapsed <= 0 {
			elapsed = time.Nanosecond
		}
		if recover() != nil {
			observation = load.Observation{Index: scheduled.Index, E2E: elapsed, ErrorCode: load.ErrorExecutorPanic}
			return
		}
		observation.E2E = elapsed
	}()
	if ctx == nil || isNilInterface(ctx) {
		observation.ErrorCode = load.ErrorRequestFailed
		return observation
	}
	var ok bool
	state, ok = client.beginExecution(ctx)
	if !ok {
		observation.ErrorCode = load.ErrorClientClosed
		return observation
	}

	request, err := http.NewRequestWithContext(state.requestContext, prepared.method, prepared.url, bytes.NewReader(prepared.body))
	if err != nil {
		observation.ErrorCode = load.ErrorRequestFailed
		return observation
	}
	for name, value := range prepared.headers {
		request.Header.Set(name, value)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("User-Agent", "llm-test-studio/1")
	if len(prepared.body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+string(state.secret))
	clear(state.secret)
	state.secret = nil
	defer request.Header.Del("Authorization")

	response, err := state.httpClient.Do(request)
	if response != nil && response.Body != nil && !isNilInterface(response.Body) {
		defer response.Body.Close()
	}
	if err != nil {
		observation.ErrorCode, observation.TimedOut = state.classify(err)
		return observation
	}
	if response == nil || response.Body == nil || isNilInterface(response.Body) {
		observation.ErrorCode = load.ErrorProtocol
		return observation
	}
	observation.HTTPStatus = response.StatusCode
	capture := newBoundedEvidenceCapture(state.maxFailureEvidenceBytes)
	responseBody := io.Reader(response.Body)
	if capture != nil {
		responseBody = io.TeeReader(response.Body, capture)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(responseBody, state.maxResponseBytes))
		if response.StatusCode == http.StatusTooManyRequests {
			observation.ErrorCode = load.ErrorRateLimited
		} else {
			observation.ErrorCode = load.ErrorHTTP
		}
		state.publishFailureEvidence(scheduled.Index, response, capture)
		return observation
	}
	if prepared.probe != nil {
		observation = readProbe(state, responseBody, observation, prepared.probe)
	} else if prepared.stream {
		observation = readStream(state, responseBody, prepared.endpoint, started, observation)
	} else {
		observation = readSynchronous(state, responseBody, prepared.endpoint, observation)
	}
	if !observation.Success {
		state.publishFailureEvidence(scheduled.Index, response, capture)
	}
	return observation
}

func (state *clientState) isClosed() bool {
	if state == nil {
		return true
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.closed
}

// Close is a revocation barrier: it blocks new executions, clears the retained
// credential, cancels active requests, and waits until their executor frames
// have completed before returning.
func (client *Client) Close() error {
	if client == nil || client.state == nil {
		return nil
	}
	state := client.state
	state.mu.Lock()
	if !state.closed {
		state.closed = true
		clear(state.secret)
		state.secret = nil
		if state.cancelLifecycle != nil {
			state.cancelLifecycle()
		}
	}
	httpClient := state.httpClient
	state.mu.Unlock()

	state.inFlight.Wait()
	closeIdleConnections(httpClient)
	return nil
}

func closeIdleConnections(client *http.Client) {
	defer func() { _ = recover() }()
	if client != nil {
		client.CloseIdleConnections()
	}
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

func (Client) String() string   { return "[REDACTED OpenAI client]" }
func (Client) GoString() string { return "[REDACTED OpenAI client]" }

func (Client) MarshalJSON() ([]byte, error) {
	return nil, fmt.Errorf("serialize OpenAI client: %w", ErrSecretSerialization)
}
