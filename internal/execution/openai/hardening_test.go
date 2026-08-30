package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
)

func TestClientOwnsRedirectPolicyAndNeverFollows307(t *testing.T) {
	var redirected atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/models":
			writer.Header().Set("Location", "/credential-sink")
			writer.WriteHeader(http.StatusTemporaryRedirect)
		case "/credential-sink":
			redirected.Add(1)
			if request.Header.Get("Authorization") != "" {
				t.Error("redirected request carried Authorization")
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"choices":[{"message":{"content":"unexpected"}}]}`)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := mustClient(t, server)
	request := modelsRequest()
	executor, err := client.Executor(request)
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{})
	if observation.Success || observation.HTTPStatus != http.StatusTemporaryRedirect || observation.ErrorCode != load.ErrorHTTP {
		t.Fatalf("observation = %#v", observation)
	}
	if got := redirected.Load(); got != 0 {
		t.Fatalf("redirect target called %d times", got)
	}
}

func TestNewClientRequiresHTTPSAndLimitsHTTPTestEscapeHatchToLoopback(t *testing.T) {
	lease := testLease(t, "super-secret")
	_, err := NewClient(testChannel("http://api.example.test"), lease, nil)
	if !errors.Is(err, ErrInsecureEndpoint) {
		t.Fatalf("remote HTTP error = %v", err)
	}
	_, err = NewClient(testChannel("http://api.example.test"), lease, nil, WithLoopbackHTTPForTesting())
	if !errors.Is(err, ErrInsecureEndpoint) {
		t.Fatalf("remote HTTP with test option error = %v", err)
	}
	_ = lease.Close()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()
	lease = testLease(t, "super-secret")
	client, err := NewClient(testChannel(server.URL), lease, server.Client().Transport, WithLoopbackHTTPForTesting())
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if observation := executor(context.Background(), load.Request{}); !observation.Success {
		t.Fatalf("loopback HTTP observation = %#v", observation)
	}
}

func TestStreamingDoneReturnsWithoutAnotherBodyRead(t *testing.T) {
	body := &oneChunkThenPanicBody{payload: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")}
	client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	}))
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{})
	if !observation.Success || !observation.StreamComplete || observation.ErrorCode != "" {
		t.Fatalf("observation = %#v", observation)
	}
	if reads := body.reads.Load(); reads != 1 {
		t.Fatalf("body reads = %d, want exactly one", reads)
	}
}

func TestExecutorContainsBoundaryPanicsAndAlwaysMeasuresE2E(t *testing.T) {
	tests := map[string]http.RoundTripper{
		"round trip": roundTripperFunc(func(*http.Request) (*http.Response, error) {
			panic("super-secret round trip panic")
		}),
		"read": roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: panicReadBody{}}, nil
		}),
		"close": roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: &panicCloseBody{Reader: strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)}}, nil
		}),
		"error As": roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return nil, panicAsError{}
		}),
	}
	for name, transport := range tests {
		t.Run(name, func(t *testing.T) {
			client := mustTransportClient(t, transport)
			executor, err := client.Executor(chatRequest(false))
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{Index: 42})
			if observation.Success || observation.ErrorCode != load.ErrorExecutorPanic || observation.Index != 42 {
				t.Fatalf("observation = %#v", observation)
			}
			if observation.E2E <= 0 {
				t.Fatalf("E2E = %v", observation.E2E)
			}
			if strings.Contains(fmt.Sprintf("%#v", observation), "super-secret") {
				t.Fatalf("observation leaked panic material: %#v", observation)
			}
		})
	}
}

func TestTypedNilTransportAndBodyAreRejectedWithoutPanic(t *testing.T) {
	var typedNilTransport *nilTransport
	lease := testLease(t, "super-secret")
	_, err := NewClient(testChannel("https://api.example.test"), lease, typedNilTransport)
	_ = lease.Close()
	if !errors.Is(err, ErrInvalidTransport) {
		t.Fatalf("typed-nil transport error = %v", err)
	}

	client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		var body *typedNilBody
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body}, nil
	}))
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{})
	if observation.Success || observation.ErrorCode != load.ErrorProtocol {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestZeroValueClientCloseDoesNotPanic(t *testing.T) {
	client := new(Client)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseCancelsAndWaitsForInFlightRequestBeforeReturning(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	var calls atomic.Int64
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-request.Context().Done()
		close(exited)
		return nil, request.Context().Err()
	})
	client := mustTransportClient(t, transport)
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	executionDone := make(chan load.Observation, 1)
	go func() { executionDone <- executor(context.Background(), load.Request{}) }()
	<-started
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Close returned before transport exited")
	}
	observation := <-executionDone
	if observation.Success || observation.ErrorCode != load.ErrorClientClosed {
		t.Fatalf("in-flight observation = %#v", observation)
	}
	closed := executor(context.Background(), load.Request{})
	if closed.ErrorCode != load.ErrorClientClosed || calls.Load() != 1 {
		t.Fatalf("closed observation=%#v calls=%d", closed, calls.Load())
	}
}

func TestCloseAfterExecutionOnSameGoroutineDoesNotDeadlock(t *testing.T) {
	client := mustTransportClient(t, staticJSONTransport(`{"choices":[{"message":{"content":"ok"}}]}`))
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if observation := executor(context.Background(), load.Request{}); !observation.Success {
		t.Fatalf("observation = %#v", observation)
	}
	done := make(chan error, 1)
	go func() { done <- client.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked after execution on the same calling path")
	}
}

func TestInvalidExecutionContextsNeverLeakTheCloseBarrier(t *testing.T) {
	tests := []struct {
		name     string
		context  context.Context
		wantCode domain.ErrorCode
	}{
		{
			name: "typed nil",
			context: func() context.Context {
				var value *typedNilContext
				return value
			}(),
			wantCode: load.ErrorRequestFailed,
		},
		{
			name:     "panicking context",
			context:  panickingContext{},
			wantCode: load.ErrorExecutorPanic,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			lease := testLease(t, "super-secret")
			client, err := NewClient(
				testChannel("https://api.example.test"),
				lease,
				roundTripperFunc(func(*http.Request) (*http.Response, error) {
					calls.Add(1)
					return nil, errors.New("transport must not be reached")
				}),
			)
			if closeErr := lease.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			executor, err := client.Executor(chatRequest(false))
			if err != nil {
				t.Fatal(err)
			}

			observation := executor(test.context, load.Request{Index: 17})
			if observation.Success || observation.ErrorCode != test.wantCode || observation.Index != 17 || observation.E2E <= 0 {
				t.Fatalf("observation = %#v, want safe %q failure", observation, test.wantCode)
			}
			if calls.Load() != 0 || strings.Contains(fmt.Sprintf("%#v", observation), "super-secret") {
				t.Fatalf("calls=%d observation=%#v", calls.Load(), observation)
			}

			closed := make(chan error, 1)
			go func() { closed <- client.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("Close blocked after invalid execution context")
			}
		})
	}
}

func TestCloseRacingExecutionAdmissionNeverSubmitsAfterBarrier(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		var calls atomic.Int64
		client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
			}, nil
		}))
		executor, err := client.Executor(chatRequest(false))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		executionDone := make(chan load.Observation, 1)
		closeDone := make(chan error, 1)
		go func() {
			<-start
			executionDone <- executor(context.Background(), load.Request{})
		}()
		go func() {
			<-start
			closeDone <- client.Close()
		}()
		close(start)
		if err := <-closeDone; err != nil {
			t.Fatal(err)
		}
		<-executionDone
		before := calls.Load()
		observation := executor(context.Background(), load.Request{})
		if observation.ErrorCode != load.ErrorClientClosed || calls.Load() != before {
			t.Fatalf("iteration %d observation=%#v calls before=%d after=%d", iteration, observation, before, calls.Load())
		}
	}
}

func TestChatSchemaDistinguishesProtocolErrorsFromSemanticEmpty(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantCode  domain.ErrorCode
		wantValid bool
	}{
		{name: "choices wrong type", response: `{"choices":{}}`, wantCode: load.ErrorProtocol},
		{name: "message wrong type", response: `{"choices":[{"message":"bad"}]}`, wantCode: load.ErrorProtocol},
		{name: "tool calls wrong type", response: `{"choices":[{"message":{"tool_calls":{}}}]}`, wantCode: load.ErrorProtocol},
		{name: "usage wrong type", response: `{"choices":[],"usage":"bad"}`, wantCode: load.ErrorProtocol},
		{name: "empty choices", response: `{"choices":[]}`, wantCode: load.ErrorSemanticEmpty},
		{name: "null tool calls", response: `{"choices":[{"message":{"tool_calls":null}}]}`, wantCode: load.ErrorSemanticEmpty},
		{name: "empty tool call object", response: `{"choices":[{"message":{"tool_calls":[{}]}}]}`, wantCode: load.ErrorSemanticEmpty},
		{name: "valid tool call", response: `{"choices":[{"message":{"tool_calls":[{"function":{"name":"lookup"}}]}}]}`, wantValid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := mustTransportClient(t, staticJSONTransport(test.response))
			executor, err := client.Executor(chatRequest(false))
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{})
			if observation.Success != test.wantValid || observation.ErrorCode != test.wantCode {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}

func TestChatStreamRequiresTypedDeltaSchema(t *testing.T) {
	response := "data: {\"choices\":[{\"delta\":\"bad\"}]}\n\ndata: [DONE]\n\n"
	client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
	}))
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}
	if observation := executor(context.Background(), load.Request{}); observation.ErrorCode != load.ErrorProtocol {
		t.Fatalf("observation = %#v", observation)
	}
}

func TestEmbeddingsEndpointInjectsPinnedModelAndRequiresTypedItems(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantCode domain.ErrorCode
		wantOK   bool
	}{
		{name: "choices envelope", response: `{"choices":[]}`, wantCode: load.ErrorProtocol},
		{name: "empty item", response: `{"object":"list","model":"upstream-model","data":[{}]}`, wantCode: load.ErrorProtocol},
		{name: "non-numeric vector", response: `{"object":"list","model":"upstream-model","data":[{"object":"embedding","embedding":[0.25,"bad"],"index":0}]}`, wantCode: load.ErrorProtocol},
		{name: "valid", response: `{"object":"list","model":"upstream-model","data":[{"object":"embedding","embedding":[0.25,-1,2.5],"index":0}]}`, wantOK: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode request body: %v", err)
				}
				if body["model"] != "upstream-model" {
					t.Errorf("request model = %#v", body["model"])
				}
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.response))}, nil
			})
			client := mustTransportClient(t, transport)
			executor, err := client.Executor(embeddingsRequest())
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{})
			if observation.Success != test.wantOK || observation.ErrorCode != test.wantCode {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}

func TestModelsEndpointRequiresModelListSchema(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		wantOK   bool
	}{
		{name: "choices envelope", response: `{"choices":[]}`},
		{name: "missing id", response: `{"object":"list","data":[{"object":"model"}]}`},
		{name: "valid", response: `{"object":"list","data":[{"id":"upstream-model","object":"model"}]}`, wantOK: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := mustTransportClient(t, staticJSONTransport(test.response))
			executor, err := client.Executor(modelsRequest())
			if err != nil {
				t.Fatal(err)
			}
			observation := executor(context.Background(), load.Request{})
			if observation.Success != test.wantOK || (!test.wantOK && observation.ErrorCode != load.ErrorProtocol) {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}

func TestExecutorRejectsUnknownAndStreamingNonChatEndpointsBeforeTransport(t *testing.T) {
	var calls atomic.Int64
	client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}, nil
	}))
	unknown := embeddingsRequest()
	unknown.Path = "/v1/unknown"
	for name, request := range map[string]domain.TestRequest{
		"unknown":              unknown,
		"streaming embeddings": nonChatRequest(true),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := client.Executor(request); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("Executor() error = %v", err)
			}
		})
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("transport calls = %d", got)
	}
}

func TestStreamingUsageMergesByPresenceAndExplicitZeroOverwrites(t *testing.T) {
	responses := []string{
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n" +
			"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}],\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0,\"prompt_tokens_details\":{\"cached_tokens\":0}}}\n\ndata: [DONE]\n\n",
	}
	wants := [][3]uint64{{9, 3, 2}, {0, 0, 0}}
	for index, response := range responses {
		client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
		}))
		executor, err := client.Executor(chatRequest(true))
		if err != nil {
			t.Fatal(err)
		}
		observation := executor(context.Background(), load.Request{})
		if !observation.Success || [3]uint64{observation.PromptTokens, observation.CompletionTokens, observation.CachedTokens} != wants[index] {
			t.Fatalf("case %d observation = %#v", index, observation)
		}
	}
}

func TestInvalidUsageNumbersAreProtocolErrors(t *testing.T) {
	for _, response := range []string{
		`{"choices":[],"usage":{"prompt_tokens":"1"}}`,
		`{"choices":[],"usage":{"completion_tokens":-1}}`,
		`{"choices":[],"usage":{"prompt_tokens_details":{"cached_tokens":1.5}}}`,
	} {
		client := mustTransportClient(t, staticJSONTransport(response))
		executor, err := client.Executor(chatRequest(false))
		if err != nil {
			t.Fatal(err)
		}
		if observation := executor(context.Background(), load.Request{}); observation.ErrorCode != load.ErrorProtocol {
			t.Fatalf("response %s observation = %#v", response, observation)
		}
	}
}

func TestStreamParserEnforcesIndependentLineEventAndLineCountBudgets(t *testing.T) {
	if MaxSSELineBytes >= MaxSSEEventBytes || MaxSSEEventBytes >= defaultMaxResponseBytes || MaxSSELines < 1 {
		t.Fatalf("unsafe stream limits: line=%d event=%d lines=%d total=%d", MaxSSELineBytes, MaxSSEEventBytes, MaxSSELines, defaultMaxResponseBytes)
	}
	tests := []struct {
		name     string
		response string
		mutate   func(*Client)
	}{
		{name: "line", response: "data: " + strings.Repeat("x", int(MaxSSELineBytes)) + "\n\n"},
		{name: "event", response: "data: " + strings.Repeat("x", int(MaxSSELineBytes-16)) + "\n" + "data: " + strings.Repeat("x", int(MaxSSELineBytes-16)) + "\n\n", mutate: func(client *Client) { client.state.maxSSEEventBytes = MaxSSELineBytes }},
		{name: "line count", response: ": one\n: two\n: three\n: four\n", mutate: func(client *Client) { client.state.maxSSELines = 3 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := mustTransportClient(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.response))}, nil
			}))
			if test.mutate != nil {
				test.mutate(client)
			}
			executor, err := client.Executor(chatRequest(true))
			if err != nil {
				t.Fatal(err)
			}
			if observation := executor(context.Background(), load.Request{}); observation.ErrorCode != load.ErrorResponseTooLarge {
				t.Fatalf("observation = %#v", observation)
			}
		})
	}
}

func TestConstructionAndPreparationErrorsNeverEchoCallerMaterial(t *testing.T) {
	lease := testLease(t, "super-secret")
	channel := testChannel("http://api.example.test/path?secret-material")
	_, err := NewClient(channel, lease, nil)
	if !errors.Is(err, ErrInvalidChannel) || containsAny(err.Error(), "secret-material", channel.BaseURL) {
		t.Fatalf("channel error = %q", err)
	}
	_ = lease.Close()

	client := mustTransportClient(t, staticJSONTransport(`{"data":[]}`))
	request := nonChatRequest(false)
	request.Method = domain.RequestMethod("SECRET-METHOD")
	request.Path = "/secret-path"
	request.Headers = map[string]string{"X-Secret-Header": "secret-header-value"}
	_, err = client.Executor(request)
	if !errors.Is(err, ErrInvalidRequest) || containsAny(err.Error(), "SECRET-METHOD", "secret-path", "Secret-Header", "secret-header-value") {
		t.Fatalf("request error = %q", err)
	}
}

func TestCopiedClientValueFormattingAndJSONNeverExposeCredential(t *testing.T) {
	const sentinel = "copy-only-secret-sentinel"
	lease := testLease(t, sentinel)
	client, err := NewClient(testChannel("https://api.example.test"), lease, staticJSONTransport(`{"data":[]}`))
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	snapshot := *client
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, snapshot)
		if strings.Contains(formatted, sentinel) || !strings.Contains(formatted, "REDACTED") {
			t.Fatalf("format %s leaked or bypassed redaction: %s", format, formatted)
		}
	}
	if _, err := json.Marshal(snapshot); !errors.Is(err, ErrSecretSerialization) {
		t.Fatalf("json.Marshal(copied client) error = %v", err)
	}
}

func embeddingsRequest() domain.TestRequest {
	return domain.TestRequest{
		Method:  domain.RequestPOST,
		Path:    "/v1/embeddings",
		Headers: map[string]string{},
		Body:    []byte(`{"input":"hello"}`),
	}
}

func modelsRequest() domain.TestRequest {
	return domain.TestRequest{
		Method:  domain.RequestGET,
		Path:    "/v1/models",
		Headers: map[string]string{},
		Body:    []byte(`{}`),
	}
}

func nonChatRequest(stream bool) domain.TestRequest {
	request := embeddingsRequest()
	request.Body = []byte(fmt.Sprintf(`{"input":"hello","stream":%t}`, stream))
	return request
}

func mustTransportClient(t *testing.T, transport http.RoundTripper) *Client {
	t.Helper()
	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel("https://api.example.test"), lease, transport)
	_ = lease.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func staticJSONTransport(response string) http.RoundTripper {
	return roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(response)),
		}, nil
	})
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

type nilTransport struct{}

func (*nilTransport) RoundTrip(*http.Request) (*http.Response, error) { panic("must not be called") }

type typedNilBody struct{}

func (*typedNilBody) Read([]byte) (int, error) { panic("must not be called") }
func (*typedNilBody) Close() error             { panic("must not be called") }

type oneChunkThenPanicBody struct {
	payload []byte
	offset  int
	reads   atomic.Int64
}

func (body *oneChunkThenPanicBody) Read(destination []byte) (int, error) {
	body.reads.Add(1)
	if body.offset == len(body.payload) {
		panic("read after [DONE]")
	}
	written := copy(destination, body.payload[body.offset:])
	body.offset += written
	return written, nil
}

func (*oneChunkThenPanicBody) Close() error { return nil }

type panicReadBody struct{}

func (panicReadBody) Read([]byte) (int, error) { panic("super-secret read panic") }
func (panicReadBody) Close() error             { return nil }

type panicCloseBody struct{ io.Reader }

func (*panicCloseBody) Close() error { panic("super-secret close panic") }

type panicAsError struct{}

func (panicAsError) Error() string { return "provider error" }
func (panicAsError) As(any) bool   { panic("super-secret As panic") }
func (panicAsError) Unwrap() error { return nil }

type typedNilContext struct{}

func (*typedNilContext) Deadline() (time.Time, bool) { panic("must not be called") }
func (*typedNilContext) Done() <-chan struct{}       { panic("must not be called") }
func (*typedNilContext) Err() error                  { panic("must not be called") }
func (*typedNilContext) Value(any) any               { panic("must not be called") }

type panickingContext struct{}

func (panickingContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (panickingContext) Done() <-chan struct{}       { panic("super-secret context panic") }
func (panickingContext) Err() error                  { return nil }
func (panickingContext) Value(any) any               { return nil }
