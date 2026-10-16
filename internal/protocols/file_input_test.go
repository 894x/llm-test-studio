package protocols_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/protocols/openaichat"
	"github.com/894x/llm-test-studio/internal/protocols/openairesponses"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const fileSpecJSON = `{"inputs":{"image":{"type":"file","required":true,"file":{"encoding":"image_data_url","media_types":["image/png"],"max_bytes":1048576}}},"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`

func TestFileExecutionRejectsBeforeAnyHTTPTransport(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		var body map[string]json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if string(body["image"]) != `{"asset_ref":"ordinary-object"}` {
			t.Errorf("ordinary asset_ref object changed: %s", body["image"])
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer server.Close()
	client := testClient(t, server, domain.ProtocolOpenAIChat)
	inputs := map[string]json.RawMessage{"image": json.RawMessage(`{"asset_ref":"image-1"}`)}
	_, err := client.Execute(context.Background(), protocols.Execution{Spec: decodeSpec(t, fileSpecJSON), Inputs: inputs})
	if !errors.Is(err, testspec.ErrFileInputsUnsupported) || calls.Load() != 0 {
		t.Fatalf("file execution error = %v, HTTP calls = %d", err, calls.Load())
	}
	ordinary := decodeSpec(t, `{"inputs":{"image":{"type":"object","required":true}},"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`)
	inputs["image"] = json.RawMessage(`{"asset_ref":"ordinary-object"}`)
	if _, err := client.Execute(context.Background(), protocols.Execution{Spec: ordinary, Inputs: inputs}); err != nil || calls.Load() != 1 {
		t.Fatalf("ordinary object execution error = %v, HTTP calls = %d", err, calls.Load())
	}
}

type rejectFileTransport struct{ calls int }

func (transport *rejectFileTransport) Send(context.Context, runtime.Request) (runtime.Response, error) {
	transport.calls++
	return runtime.Response{}, errors.New("unexpected transport call")
}

func TestProtocolModulesRejectDeclaredFilesWithoutResolvedPins(t *testing.T) {
	spec := decodeSpec(t, fileSpecJSON)
	// Even a request that does not reference its file must not bypass this gate.
	spec.Request.Body = json.RawMessage(`{}`)
	for _, module := range []runtime.Module{
		openaichat.New(), openairesponses.New(),
		runtime.TaskProtocol{ID: "task", SubmitPath: "/tasks"},
	} {
		transport := &rejectFileTransport{}
		observation := module.Execute(context.Background(), runtime.Execution{Spec: spec, Transport: transport})
		if transport.calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 || observation.Issues[0].Code != "file_inputs_unsupported" {
			t.Fatalf("module %T file execution: calls=%d, observation=%+v", module, transport.calls, observation)
		}
	}
}
