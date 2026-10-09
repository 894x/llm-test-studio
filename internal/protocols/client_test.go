package protocols_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestProtocolExecutesCompletedAddressesAndSiblingRoutes(t *testing.T) {
	const chatSpec = `{"inputs":{},"request":{"body":{}},"assertions":[]}`
	const modelsSpec = `{"operation":"models.list","inputs":{},"request":{"body":{}},"assertions":[]}`
	const videoSpec = `{"inputs":{},"request":{"body":{"content":[]}},"workflow":{"mode":"wait"},"assertions":[]}`
	tests := []struct {
		name, input, spec string
		protocol          domain.Protocol
		paths             []string
	}{
		{name: "root", spec: chatSpec, protocol: domain.ProtocolOpenAIChat,
			paths: []string{protocol.OpenAIChatPath}},
		{name: "version slash", input: "/v1/", spec: chatSpec, protocol: domain.ProtocolOpenAIChat,
			paths: []string{protocol.OpenAIChatPath}},
		{name: "partial path", input: "/proxy/v1/ch", spec: chatSpec, protocol: domain.ProtocolOpenAIChat,
			paths: []string{"/proxy" + protocol.OpenAIChatPath}},
		{name: "full endpoint", input: "/proxy/v1/chat/completions/", spec: chatSpec,
			protocol: domain.ProtocolOpenAIChat, paths: []string{"/proxy" + protocol.OpenAIChatPath}},
		{name: "versionless endpoint", input: "/proxy/chat/completions", spec: chatSpec,
			protocol: domain.ProtocolOpenAIChat, paths: []string{"/proxy/chat/completions"}},
		{name: "model list sibling", input: "/proxy/v1/chat/completions", spec: modelsSpec,
			protocol: domain.ProtocolOpenAIChat, paths: []string{"/proxy" + protocol.OpenAIModelsPath}},
		{name: "video submit and poll", input: "/proxy" + protocol.SeedancePath, spec: videoSpec,
			protocol: domain.ProtocolSeedance,
			paths:    []string{"/proxy" + protocol.SeedancePath, "/proxy" + protocol.SeedancePath + "/task-1"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths = append(paths, r.URL.Path)
				if r.Header.Get("Authorization") != "Bearer test-address-key" {
					t.Error("missing credential")
				}
				if test.protocol == domain.ProtocolSeedance {
					if r.Method == http.MethodPost {
						io.WriteString(w, `{"id":"task-1","status":"running"}`)
						return
					}
					io.WriteString(w, `{"id":"task-1","status":"succeeded"}`)
					return
				}
				io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer server.Close()
			lease, err := credentials.NewTemporaryLease([]byte("test-address-key"))
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			channel := domain.ChannelSnapshot{
				EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
				Name:              "address",
				Protocol:          test.protocol,
				BaseURL:           server.URL + test.input,
				UpstreamModelName: "model",
			}
			client, err := protocols.NewClient(
				protocols.NewRegistry(),
				lease,
				channel,
				server.Client().Transport,
			)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			_, err = client.Execute(context.Background(), protocols.Execution{Spec: decodeSpec(t, test.spec)})
			if err != nil || strings.Join(paths, ",") != strings.Join(test.paths, ",") {
				t.Fatalf(
					"actual paths = %v, want %v, error = %v",
					paths,
					test.paths,
					err,
				)
			}
		})
	}
}

func testClient(t *testing.T, server *httptest.Server, protocol domain.Protocol) *protocols.Client {
	t.Helper()
	lease, err := credentials.NewTemporaryLease([]byte("private-test-api-key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lease.Close() })
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
		Name:              "test", Protocol: protocol, BaseURL: server.URL, UpstreamModelName: "bound-model",
	}
	client, err := protocols.NewClient(protocols.NewRegistry(), lease, channel, server.Client().Transport)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	return client
}

func decodeSpec(t *testing.T, body string) testspec.Spec {
	t.Helper()
	spec, err := testspec.Decode(json.RawMessage(body))
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestHTTPRejectionCanPassAndNegativeBodyReachesTransport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["temperature"] != "invalid" || body["model"] != "bound-model" {
			t.Errorf("unexpected request: %v", body)
		}
		if r.Header.Get("Authorization") != "Bearer private-test-api-key" {
			t.Error("missing run credential")
		}
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"code":"invalid_parameter","message":"private-test-api-key"}}`)
	}))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{"temperature":"invalid"}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":400},{"id":"code","source":"response","pointer":"/error/code","operator":"equals","value":"invalid_parameter"}]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Status != testspec.VerdictPassed {
		t.Fatalf("configured rejection failed: %+v", result)
	}
	raw, _ := json.Marshal(result)
	if strings.Contains(string(raw), "private-test-api-key") {
		t.Fatal("credential leaked in result")
	}
	if !strings.Contains(string(result.Observation.Exchanges[0].RequestBody), "bound-model") {
		t.Fatal("actual request binding not recorded")
	}
	if strings.Contains(fmt.Sprintf("%#v", *client), "private-test-api-key") {
		t.Fatal("client formatting leaked secret")
	}
}

func TestPartialJSONObservationStillAllowsStatusAssertion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400); io.WriteString(w, "invalid JSON") }))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":400}]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Status != testspec.VerdictPassed || len(result.Observation.Issues) != 1 {
		t.Fatalf("partial observation lost: %+v", result)
	}
}

func TestEmptyAssertionsDoNotCreateImplicitResponseChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503); io.WriteString(w, `{}`) }))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{}},"assertions":[]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Status != testspec.VerdictNotApplicable || *result.Observation.HTTPStatus != 503 {
		t.Fatalf("observation-only result: %+v", result)
	}
}

func TestStreamCollectsToolsUsageAndCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call-a\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\"}}]}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":\"tool_calls\"}],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{"stream":true}},"assertions":[{"id":"done","source":"stream.completed","operator":"equals","value":true},{"id":"tool","source":"response","pointer":"/choices/0/message/tool_calls/0/function/name","operator":"equals","value":"weather"},{"id":"args","source":"response","pointer":"/choices/0/message/tool_calls/0/function/arguments","operator":"json_valid"}]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Status != testspec.VerdictPassed || result.Observation.Metrics["prompt_tokens"] != 9 {
		t.Fatalf("stream result: %+v", result)
	}
}

func TestAsyncFailedTerminalIsDataForConfiguredAssertion(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method == "POST" {
			io.WriteString(w, `{"id":"task-1"}`)
			return
		}
		if r.URL.Path != "/api/v3/contents/generations/tasks/task-1" {
			t.Errorf("poll path: %s", r.URL.Path)
		}
		io.WriteString(w, `{"id":"task-1","status":"failed","error":{"code":"invalid_content"}}`)
	}))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolSeedance)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{"content":[]}},"workflow":{"mode":"wait"},"assertions":[{"id":"terminal","source":"task","pointer":"/status","operator":"equals","value":"failed"}]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict.Status != testspec.VerdictPassed || requests.Load() != 2 || !result.Observation.Task.Terminal {
		t.Fatalf("task result: %+v", result)
	}
}

func TestRedirectIsObservedWithoutCredentialForwarding(t *testing.T) {
	var forwarded atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Store(true) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{}},"assertions":[{"id":"redirect","source":"http.status","operator":"equals","value":307}]}`)
	result, err := client.Execute(context.Background(), protocols.Execution{Spec: spec})
	if err != nil {
		t.Fatal(err)
	}
	if forwarded.Load() || result.Verdict.Status != testspec.VerdictPassed {
		t.Fatal("redirect policy lost")
	}
}

func TestCloseCancelsTaskWaitAndRevokesCopiedClient(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			io.WriteString(w, `{"id":"task-1"}`)
			return
		}
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolSeedance)
	copy := *client
	spec := decodeSpec(t, `{"inputs":{},"request":{"body":{}},"workflow":{"mode":"wait"},"assertions":[]}`)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.Execute(context.Background(), protocols.Execution{Spec: spec})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("task did not start")
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel active task")
	}
	if _, err := copy.Execute(context.Background(), protocols.Execution{Spec: spec}); err != protocols.ErrClientClosed {
		t.Fatalf("copied handle still usable: %v", err)
	}
}
