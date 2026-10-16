package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestExchangeOwnsSelectedRequestBody(t *testing.T) {
	for _, test := range []struct {
		name   string
		actual json.RawMessage
		want   string
	}{
		{name: "template body", want: `{"model":"template"}`},
		{name: "empty actual body", actual: json.RawMessage{}, want: `{"model":"template"}`},
		{name: "bound body", actual: json.RawMessage(`{"model":"upstream"}`), want: `{"model":"upstream"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := Request{Body: testspec.RenderedRequest{
				Body: json.RawMessage(`{"model":"template"}`), Files: []testspec.FileOccurrence{},
			}}
			entry := Exchange("initial", request, Response{RequestBody: test.actual})
			request.Body.Body[0] = '!'
			if len(test.actual) > 0 {
				test.actual[0] = '!'
			}
			if string(entry.RequestBody) != test.want {
				t.Fatalf("selected request body = %s, want %s", entry.RequestBody, test.want)
			}
		})
	}
	if entry := Exchange("empty", Request{}, Response{}); entry.RequestBody == nil || len(entry.RequestBody) != 0 {
		t.Fatalf("empty request body = %#v, want non-nil empty bytes", entry.RequestBody)
	}
}

type taskFileTransport struct{ calls int }

func (transport *taskFileTransport) Send(context.Context, Request) (Response, error) {
	transport.calls++
	return Response{}, errors.New("unexpected transport call")
}

func TestTaskProtocolKeepsDeclaredAndResolvedFilesUnsupported(t *testing.T) {
	protocol := TaskProtocol{ID: "task", SubmitPath: "/tasks"}
	for _, declared := range []bool{false, true} {
		inputs := testspec.ResolvedInputs{JSONValues: map[string]json.RawMessage{}, Files: map[string]testspec.FileBinding{}}
		spec := testspec.Spec{Inputs: map[string]testspec.Input{}, Request: testspec.Request{Body: json.RawMessage(`{}`)}}
		if declared {
			spec.Inputs["image"] = testspec.Input{Type: "file", Required: true, File: &testspec.FileInput{
				Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
			}}
			if !errors.Is(protocol.ValidateProtocol(spec), testspec.ErrFileInputsUnsupported) {
				t.Fatal("task protocol accepted a file declaration")
			}
		} else {
			inputs.Files["image"] = testspec.FileBinding{AssetRef: "image-1"}
		}
		transport := &taskFileTransport{}
		observation := protocol.Execute(context.Background(), Execution{Spec: spec, Inputs: inputs, Transport: transport})
		if transport.calls != 0 || len(observation.Exchanges) != 0 || len(observation.Issues) != 1 ||
			observation.Issues[0].Code != "file_inputs_unsupported" {
			t.Fatalf("task file execution: calls=%d observation=%+v", transport.calls, observation)
		}
	}
}

func TestPreparedWireCloneOwnsFilesAndStaysOutOfPersistedExchange(t *testing.T) {
	wire := PreparedWire{SHA256: "wire-digest", Bytes: 123, Files: []PreparedFile{{
		InputName: "image", Pointer: "/messages/0/content/0/image_url/url", AssetRef: "image-1",
		SHA256: "asset-digest", Size: 3, MediaType: "image/png", Encoding: "image_data_url",
	}}}
	cloned := wire.Clone()
	cloned.Files[0].SHA256 = "changed"
	if wire.Files[0].SHA256 != "asset-digest" || cloned.SHA256 != wire.SHA256 || cloned.Bytes != wire.Bytes {
		t.Fatalf("clone does not own its prepared files: source=%+v clone=%+v", wire, cloned)
	}
	if empty := (PreparedWire{}).Clone(); empty.Files == nil || len(empty.Files) != 0 {
		t.Fatalf("empty prepared files = %#v", empty.Files)
	}
	request := Request{Body: testspec.RenderedRequest{
		Body:  json.RawMessage(`{"image":null}`),
		Files: []testspec.FileOccurrence{{InputName: "image", Pointer: "/image", AssetRef: "image-1"}},
	}}
	response := Response{
		RequestBody: json.RawMessage(`{"model":"upstream","image":"redacted_asset"}`), PreparedWire: &wire,
	}
	entry := Exchange("initial", request, response)
	if string(entry.RequestBody) != string(response.RequestBody) {
		t.Fatalf("exchange retained a file placeholder instead of the redacted view: %s", entry.RequestBody)
	}
	for _, value := range []any{response, entry} {
		document, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(document, []byte("wire-digest")) || bytes.Contains(document, []byte("asset-digest")) {
			t.Fatalf("internal prepared-wire metadata leaked into JSON: %s", document)
		}
	}
}
