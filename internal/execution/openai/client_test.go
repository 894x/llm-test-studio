package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

const (
	clientChannelID    = "123e4567-e89b-42d3-a456-426614174030"
	clientCredentialID = "123e4567-e89b-42d3-a456-426614174031"
)

func testChannel(baseURL string) domain.ChannelSnapshot {
	return domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: clientChannelID, Revision: 1},
		Name:              "test", BaseURL: baseURL, Protocol: domain.ProtocolOpenAIChat,
		UpstreamModelName: "upstream-model",
	}
}

func testLease(t *testing.T, secret string) *credentials.Lease {
	t.Helper()
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, clientCredentialID)
	if err != nil {
		t.Fatal(err)
	}
	store := credentials.NewMemoryStore()
	if err := store.Set(context.Background(), ref, []byte(secret)); err != nil {
		t.Fatal(err)
	}
	lease, err := store.Get(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}

func chatRequest(stream bool) domain.TestRequest {
	body := fmt.Sprintf(`{"messages":[{"role":"user","content":"hello"}],"stream":%t}`, stream)
	return domain.TestRequest{
		Method: domain.RequestPOST, Path: "/v1/chat/completions",
		Headers: map[string]string{"X-Test": "safe"}, Body: json.RawMessage(body),
	}
}

func TestStreamingExecutorRequiresDoneAndMeasuresFirstSemanticToken(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer super-secret" {
			t.Errorf("Authorization = %q", got)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["model"] != "upstream-model" {
			t.Errorf("model = %#v", body["model"])
		}
		streamOptions, _ := body["stream_options"].(map[string]any)
		if streamOptions["include_usage"] != true {
			t.Errorf("stream_options = %#v", streamOptions)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
		flusher.Flush()
		time.Sleep(25 * time.Millisecond)
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"think\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":4}}}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel(server.URL), lease, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}

	observation := executor(context.Background(), load.Request{Index: 7})
	if !observation.Success || !observation.StreamComplete || observation.ErrorCode != "" {
		t.Fatalf("observation = %#v", observation)
	}
	if observation.TTFT < 20*time.Millisecond {
		t.Fatalf("TTFT = %v, role-only frame was counted", observation.TTFT)
	}
	if observation.E2E < observation.TTFT {
		t.Fatalf("E2E = %v, want at least TTFT %v", observation.E2E, observation.TTFT)
	}
	if observation.PromptTokens != 10 || observation.CompletionTokens != 3 || observation.CachedTokens != 4 {
		t.Fatalf("usage = %#v", observation)
	}
}

func TestClientAcceptsKimiModelOverOpenAIChat(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["model"] != "kimi-k3" {
			t.Errorf("request model = %v, error = %v", body["model"], err)
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	channel := testChannel(server.URL)
	channel.Protocol = domain.ProtocolOpenAIChat
	channel.UpstreamModelName = "kimi-k3"
	lease := testLease(t, "super-secret")
	client, err := NewClient(channel, lease, server.Client().Transport)
	if err != nil {
		t.Fatalf("NewClient(Kimi K3) error = %v", err)
	}
	_ = lease.Close()
	t.Cleanup(func() { _ = client.Close() })
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	if observation := executor(context.Background(), load.Request{}); !observation.Success {
		t.Fatalf("Kimi K3 observation = %#v", observation)
	}
}

func TestProbeExecutorReturnsOnlyClassifierDimensions(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
		}
		if body["model"] != "upstream-model" {
			t.Errorf("model = %#v", body["model"])
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"provider":"provider-secret","choices":[]}`)
	}))
	defer server.Close()
	client := mustClient(t, server)
	executor, err := client.ProbeExecutor(chatRequest(false), func(encoded []byte) (map[string]string, error) {
		if !strings.Contains(string(encoded), `"provider":"provider-secret"`) {
			t.Fatalf("classifier body = %q", encoded)
		}
		return map[string]string{"probe_bucket": "provider-a", "probe_shape": "sha256:test"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	observation := executor(context.Background(), load.Request{Index: 3})
	if !observation.Success || observation.HTTPStatus != http.StatusOK || observation.ErrorCode != "" {
		t.Fatalf("probe observation = %#v", observation)
	}
	if observation.Dimensions["probe_bucket"] != "provider-a" || observation.Dimensions["probe_shape"] != "sha256:test" {
		t.Fatalf("probe dimensions = %#v", observation.Dimensions)
	}
	if strings.Contains(fmt.Sprintf("%#v", observation), "provider-secret") {
		t.Fatalf("probe observation leaked response body: %#v", observation)
	}
}

func TestStreamingExecutorRejectsHTTP200WithoutDone(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()
	client := mustClient(t, server)
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}

	observation := executor(context.Background(), load.Request{})
	if observation.Success || observation.StreamComplete || observation.ErrorCode != "incomplete_stream" {
		t.Fatalf("observation = %#v", observation)
	}
	if !observation.Streaming || observation.TTFB <= 0 || observation.TTFTAny <= 0 ||
		observation.TTFT != observation.TTFTAny || observation.TTFTVisible != observation.TTFTAny ||
		observation.SemanticChunkCount != 1 || observation.TTST != 0 || observation.ObservedICL != 0 {
		t.Fatalf("partial stream telemetry = %#v", observation)
	}
	metrics := load.ComputeMetrics([]load.Observation{observation}, time.Second)
	if metrics.TTFBSamples != 0 || metrics.TTFTSamples != 0 || metrics.TTFTAnySamples != 0 ||
		metrics.TTFTVisibleSamples != 0 || metrics.TTSTSamples != 0 || metrics.ObservedICLSamples != 0 ||
		metrics.SemanticChunkCountSamples != 0 {
		t.Fatalf("failed partial stream entered aggregate cohorts: %#v", metrics)
	}
}

func TestSynchronousChatRequiresSemanticOutputAndReadsUsage(t *testing.T) {
	responses := make(chan string, 2)
	responses <- `{"choices":[{"message":{"content":"answer"}}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`
	responses <- `{"choices":[]}`
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, <-responses)
	}))
	defer server.Close()
	client := mustClient(t, server)
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}

	valid := executor(context.Background(), load.Request{})
	if !valid.Success || valid.PromptTokens != 5 || valid.CompletionTokens != 2 {
		t.Fatalf("valid observation = %#v", valid)
	}
	empty := executor(context.Background(), load.Request{Index: 1})
	if empty.Success || empty.ErrorCode != "semantic_empty" {
		t.Fatalf("empty observation = %#v", empty)
	}
}

func TestExecutorClassifiesHTTPAndCancellationWithoutReturningBodiesOrSecrets(t *testing.T) {
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Test-Mode") == "http-error" {
			writer.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(writer, "super-secret provider body")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusOK)
		writer.(http.Flusher).Flush()
		close(started)
		<-request.Context().Done()
		close(cancelObserved)
	}))
	defer server.Close()
	client := mustClient(t, server)

	httpErrorRequest := chatRequest(false)
	httpErrorRequest.Headers["X-Test-Mode"] = "http-error"
	httpExecutor, err := client.Executor(httpErrorRequest)
	if err != nil {
		t.Fatal(err)
	}
	httpObservation := httpExecutor(context.Background(), load.Request{})
	if httpObservation.Success || httpObservation.HTTPStatus != 429 || httpObservation.ErrorCode != "rate_limited" {
		t.Fatalf("HTTP observation = %#v", httpObservation)
	}
	if strings.Contains(fmt.Sprintf("%#v", httpObservation), "super-secret") {
		t.Fatalf("HTTP observation leaked response or credential: %#v", httpObservation)
	}

	cancelRequest := chatRequest(false)
	cancelRequest.Headers["X-Test-Mode"] = "cancel"
	cancelExecutor, err := client.Executor(cancelRequest)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan load.Observation, 1)
	go func() { done <- cancelExecutor(ctx, load.Request{}) }()
	<-started
	cancel()
	observation := <-done
	if observation.Success || observation.ErrorCode != "cancelled" {
		t.Fatalf("cancel observation = %#v", observation)
	}
	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach the live HTTP request")
	}
}

func TestExecutorBoundsResponseAndClientCloseRevokesCopiedSecret(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, strings.Repeat("x", 256))
	}))
	defer server.Close()
	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel(server.URL), lease, server.Client().Transport, WithMaxResponseBytes(64))
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}
	observation := executor(context.Background(), load.Request{})
	if observation.ErrorCode != "response_too_large" {
		t.Fatalf("oversized observation = %#v", observation)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	closed := executor(context.Background(), load.Request{})
	if closed.ErrorCode != "client_closed" {
		t.Fatalf("closed observation = %#v", closed)
	}
	if _, err := json.Marshal(client); !errors.Is(err, ErrSecretSerialization) {
		t.Fatalf("json.Marshal(client) error = %v", err)
	}
}

func TestStreamingExecutorBoundsAnOversizedSSEFrame(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", strings.Repeat("x", 256))
	}))
	defer server.Close()
	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel(server.URL), lease, server.Client().Transport, WithMaxResponseBytes(64))
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	t.Cleanup(func() { _ = client.Close() })
	executor, err := client.Executor(chatRequest(true))
	if err != nil {
		t.Fatal(err)
	}

	observation := executor(context.Background(), load.Request{})
	if observation.Success || observation.ErrorCode != load.ErrorResponseTooLarge {
		t.Fatalf("oversized stream observation = %#v", observation)
	}
}

func TestNewClientRejectsUnsafeResponseLimits(t *testing.T) {
	for _, limit := range []int64{0, -1, maxAllowedResponseBytes + 1, math.MaxInt64} {
		lease := testLease(t, "super-secret")
		client, err := NewClient(testChannel("https://api.example.test"), lease, nil, WithMaxResponseBytes(limit))
		_ = lease.Close()
		if err == nil {
			_ = client.Close()
			t.Fatalf("WithMaxResponseBytes(%d) succeeded", limit)
		}
	}
}

func TestExecutorClosesResponseBodyWhenDoerReturnsAnError(t *testing.T) {
	body := &closeProbe{Reader: strings.NewReader("provider-secret")}
	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel("https://api.example.test"), lease, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{Body: body}, errors.New("provider error containing provider-secret")
	}))
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	t.Cleanup(func() { _ = client.Close() })
	executor, err := client.Executor(chatRequest(false))
	if err != nil {
		t.Fatal(err)
	}

	observation := executor(context.Background(), load.Request{})
	if !body.closed || observation.ErrorCode != load.ErrorNetwork {
		t.Fatalf("body closed=%t observation=%#v", body.closed, observation)
	}
	if strings.Contains(fmt.Sprintf("%#v", observation), "provider-secret") {
		t.Fatalf("observation leaked provider error: %#v", observation)
	}
}

func mustClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	lease := testLease(t, "super-secret")
	client, err := NewClient(testChannel(server.URL), lease, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	t.Cleanup(func() { _ = client.Close() })
	return client
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type closeProbe struct {
	io.Reader
	closed bool
}

func (body *closeProbe) Close() error {
	body.closed = true
	return nil
}
