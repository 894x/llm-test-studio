package openaichat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type moduleTransport func(context.Context, runtime.Request) (runtime.Response, error)

func (send moduleTransport) Send(ctx context.Context, request runtime.Request) (runtime.Response, error) {
	return send(ctx, request)
}

type moduleAssets struct{}

func (*moduleAssets) Open(context.Context, string) (runtime.AssetStream, error) {
	return nil, errors.New("module must leave asset reads to the transport")
}

func fileExecution() runtime.Execution {
	return runtime.Execution{
		Spec: testspec.Spec{
			Inputs: map[string]testspec.Input{"image": {Type: "file", Required: true, File: &testspec.FileInput{
				Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
			}}},
			Request: testspec.Request{Body: json.RawMessage(
				`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":{"$input":"image"}}}]}]}`,
			)},
			Assertions: []testspec.Assertion{},
		},
		Inputs: testspec.ResolvedInputs{
			JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{"image": {AssetRef: "image-1"}},
		},
	}
}

func TestExecutePassesNativeFileOccurrencesAndResolverToTransport(t *testing.T) {
	execution := fileExecution()
	assets := &moduleAssets{}
	execution.Assets = assets
	var calls int
	execution.Transport = moduleTransport(func(_ context.Context, request runtime.Request) (runtime.Response, error) {
		calls++
		if request.Assets != assets || request.Method != http.MethodPost || len(request.Body.Files) != 1 {
			t.Fatalf("file request lost its resolver or occurrence: %+v", request)
		}
		file := request.Body.Files[0]
		validBinding := file.InputName == "image" && file.AssetRef == "image-1"
		validEncoding := file.Pointer == "/messages/0/content/0/image_url/url" && file.File.Encoding == "image_data_url"
		if !validBinding || !validEncoding {
			t.Fatalf("file occurrence = %+v", file)
		}
		hasPlaceholder := strings.Contains(string(request.Body.Body), `"url":null`)
		if !hasPlaceholder || strings.Contains(string(request.Body.Body), "asset_ref") {
			t.Fatalf("file binding entered JSON instead of an out-of-band occurrence: %s", request.Body.Body)
		}
		return runtime.Response{
			Started: time.Now(), RequestBody: json.RawMessage(`{"model":"upstream","image":"redacted_asset"}`),
			HTTP: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(
				strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`),
			)},
		}, nil
	})
	if err := New().ValidateProtocol(execution.Spec); err != nil {
		t.Fatal(err)
	}
	observation := New().Execute(context.Background(), execution)
	if calls != 1 || len(observation.Issues) != 0 || len(observation.Exchanges) != 1 {
		t.Fatalf("native image execution: calls=%d observation=%+v", calls, observation)
	}
	if string(observation.Exchanges[0].RequestBody) != `{"model":"upstream","image":"redacted_asset"}` {
		t.Fatalf("exchange did not retain the transport's redacted request view: %+v", observation.Exchanges)
	}
}

func TestExecuteRejectsFileWorkflowAndNonCreateBeforeTransport(t *testing.T) {
	for _, mode := range []string{"sequence", "wait", "models.list"} {
		t.Run(mode, func(t *testing.T) {
			execution := fileExecution()
			execution.Assets = &moduleAssets{}
			if mode == "models.list" {
				execution.Spec.Operation = mode
			} else {
				execution.Spec.Workflow = &testspec.Workflow{Mode: mode, Steps: []testspec.Step{}}
			}
			var calls int
			execution.Transport = moduleTransport(func(context.Context, runtime.Request) (runtime.Response, error) {
				calls++
				return runtime.Response{}, errors.New("unexpected transport call")
			})
			if err := New().ValidateProtocol(execution.Spec); err == nil {
				t.Fatal("file workflow or non-create operation was accepted")
			}
			observation := New().Execute(context.Background(), execution)
			if calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 ||
				observation.Issues[0].Code != "file_inputs_unsupported" {
				t.Fatalf("unsupported file execution: calls=%d observation=%+v", calls, observation)
			}
		})
	}
}

func TestExecuteRequiresExplicitResolverForFilesBeforeTransport(t *testing.T) {
	execution := fileExecution()
	var calls int
	execution.Transport = moduleTransport(func(context.Context, runtime.Request) (runtime.Response, error) {
		calls++
		return runtime.Response{}, errors.New("unexpected transport call")
	})
	observation := New().Execute(context.Background(), execution)
	if calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 ||
		observation.Issues[0].Code != "file_inputs_unsupported" {
		t.Fatalf("file request without resolver: calls=%d observation=%+v", calls, observation)
	}
}

func TestExecuteRejectsFilesOutsideNativeImagePositionBeforeTransport(t *testing.T) {
	execution := fileExecution()
	execution.Assets = &moduleAssets{}
	execution.Spec.Request.Body = json.RawMessage(`{"image":{"$input":"image"}}`)
	var calls int
	execution.Transport = moduleTransport(func(context.Context, runtime.Request) (runtime.Response, error) {
		calls++
		return runtime.Response{}, errors.New("unexpected transport call")
	})
	observation := New().Execute(context.Background(), execution)
	if calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 ||
		observation.Issues[0].Code != "request_generation_failed" {
		t.Fatalf("file request outside native image position: calls=%d observation=%+v", calls, observation)
	}
}

func TestExecuteModelsListUsesZeroRenderedBody(t *testing.T) {
	execution := runtime.Execution{Spec: testspec.Spec{
		Operation: "models.list", Inputs: map[string]testspec.Input{}, Assertions: []testspec.Assertion{},
		Request: testspec.Request{Body: json.RawMessage(`{}`)},
	}, Inputs: testspec.ResolvedInputs{JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{}}}
	var calls int
	execution.Transport = moduleTransport(func(_ context.Context, request runtime.Request) (runtime.Response, error) {
		calls++
		bodyless := len(request.Body.Body) == 0 && len(request.Body.Files) == 0
		validRequest := request.Method == http.MethodGet && bodyless && request.Assets == nil
		if !validRequest {
			t.Fatalf("models.list retained a rendered POST body: %+v", request)
		}
		return runtime.Response{HTTP: &http.Response{StatusCode: http.StatusOK, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"data":[]}`))}}, nil
	})
	observation := New().Execute(context.Background(), execution)
	if calls != 1 || len(observation.Issues) != 0 {
		t.Fatalf("models.list: calls=%d observation=%+v", calls, observation)
	}
}
