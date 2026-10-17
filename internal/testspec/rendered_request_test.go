package testspec

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

const nativeFileTemplate = `{
	"messages":[{"role":"user","content":[
		{"type":"text","text":"Describe this image"},
		{"type":"image_url","image_url":{"url":{"$input":"image"}}}
	]}]
}`

func TestRenderNativeFileProducesOwnedNullOccurrence(t *testing.T) {
	input := imageRenderInput(nativeFileTemplate)
	request, err := Render(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("rendered request rejected: %v", err)
	}
	if len(request.Files) != 1 {
		t.Fatalf("file occurrences = %+v", request.Files)
	}
	occurrence := request.Files[0]
	if occurrence.InputName != "image" || occurrence.AssetRef != "image-1.png" {
		t.Fatalf("wrong file identity: %+v", occurrence)
	}
	if occurrence.Pointer != "/messages/0/content/1/image_url/url" {
		t.Fatalf("wrong file pointer: %q", occurrence.Pointer)
	}
	if occurrence.File.Encoding != "image_data_url" || occurrence.File.MaxBytes != MaxFileInputBytes {
		t.Fatalf("wrong file metadata: %+v", occurrence.File)
	}
	if !slices.Equal(occurrence.File.MediaTypes, []string{"image/png", "image/jpeg"}) {
		t.Fatalf("wrong media types: %v", occurrence.File.MediaTypes)
	}
	root, err := decodeValue(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if value, found := jsonpointer.Lookup(root, occurrence.Pointer); !found || value != nil {
		t.Fatalf("file value is not an existing null placeholder: %v, %v", value, found)
	}
	if strings.Contains(string(request.Body), "asset_ref") || strings.Contains(string(request.Body), "image-1.png") {
		t.Fatal("logical binding escaped into the provider body")
	}
	input.Declarations["image"].File.MediaTypes[0] = "changed"
	if request.Files[0].File.MediaTypes[0] != "image/png" {
		t.Fatal("rendered file metadata aliases its declaration")
	}
	clone := request.Clone()
	clone.Body[0] = '['
	clone.Files[0].InputName = "changed"
	clone.Files[0].File.MediaTypes[0] = "changed"
	if request.Body[0] != '{' || request.Files[0].InputName != "image" {
		t.Fatal("cloned request aliases the original body or occurrence")
	}
	if request.Files[0].File.MediaTypes[0] != "image/png" {
		t.Fatal("cloned request aliases file media types")
	}
}

func TestRenderRejectsUnsupportedFileTemplateNodes(t *testing.T) {
	tests := map[string]string{
		"root input":    `{"$input":"image"}`,
		"ordinary path": `{"image":{"$input":"image"}}`,
		"unused":        `{"messages":[]}`,
		"extra input field": strings.Replace(
			nativeFileTemplate,
			`{"$input":"image"}`,
			`{"$input":"image","extra":true}`,
			1,
		),
		"wrong content kind": strings.Replace(
			nativeFileTemplate,
			`"type":"image_url"`,
			`"type":"text"`,
			1,
		),
		"repeated": `{"messages":[{"content":[
			{"type":"image_url","image_url":{"url":{"$input":"image"}}},
			{"type":"image_url","image_url":{"url":{"$input":"image"}}}
		]}]}`,
		"messages object": `{"messages":{"0":{"content":[
			{"type":"image_url","image_url":{"url":{"$input":"image"}}}
		]}}}`,
		"content object": `{"messages":[{"content":{"0":
			{"type":"image_url","image_url":{"url":{"$input":"image"}}}
		}}]}`,
		"response reference": strings.Replace(
			nativeFileTemplate,
			`"Describe this image"`,
			`{"$response":"initial","pointer":""}`,
			1,
		),
		"generator ancestor": strings.Replace(
			nativeFileTemplate,
			`"image_url":{"url":`,
			`"image_url":{"$generate":"concat","url":`,
			1,
		),
		"concat": `{"messages":[{"content":[{"type":"image_url","image_url":{"url":
			{"$generate":"concat","parts":[{"$input":"image"}]}
		}}]}]}`,
		"repeat": `{"messages":[{"content":[{"type":"image_url","image_url":{"url":
			{"$generate":"repeat","text":{"$input":"image"},"count":1}
		}}]}]}`,
		"repeat text": `{"messages":[{"content":[{"type":"image_url","image_url":{"url":
			{"$generate":"repeat_text","text":{"$input":"image"},"length":1}
		}}]}]}`,
		"random text": `{"messages":[{"content":[{"type":"image_url","image_url":{"url":
			{"$generate":"random_text","alphabet":{"$input":"image"},"length":1}
		}}]}]}`,
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Render(imageRenderInput(body)); !errors.Is(err, ErrFileInputsUnsupported) {
				t.Fatalf("unsupported file template error = %v", err)
			}
		})
	}
}

func TestRenderRejectsInvalidFileBindings(t *testing.T) {
	tests := map[string]func(*RenderInput){
		"undeclared": func(input *RenderInput) { delete(input.Declarations, "image") },
		"ordinary declaration": func(input *RenderInput) {
			input.Declarations["image"] = Input{Type: "object"}
		},
		"invalid declaration": func(input *RenderInput) { input.Declarations["image"].File.MaxBytes = 0 },
		"missing":             func(input *RenderInput) { delete(input.Values.Files, "image") },
		"unsafe reference": func(input *RenderInput) {
			input.Values.Files["image"] = FileBinding{AssetRef: "../image.png"}
		},
		"JSON overlap": func(input *RenderInput) {
			input.Values.JSONValues["image"] = json.RawMessage(`{"asset_ref":"image-1.png"}`)
		},
		"multiple bindings": func(input *RenderInput) {
			input.Declarations["second"] = input.Declarations["image"].Clone()
			input.Values.Files["second"] = FileBinding{AssetRef: "image-2.png"}
		},
		"response values": func(input *RenderInput) {
			input.Responses["initial"] = json.RawMessage(`{}`)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			input := imageRenderInput(nativeFileTemplate)
			mutate(&input)
			if _, err := Render(input); !errors.Is(err, ErrFileInputsUnsupported) {
				t.Fatalf("invalid binding error = %v", err)
			}
		})
	}
}

func TestGenerateNativeFileRejectsWorkflows(t *testing.T) {
	input := imageRenderInput(nativeFileTemplate)
	spec := Spec{Inputs: input.Declarations, Request: input.Request, Assertions: []Assertion{}}
	values := map[string]json.RawMessage{"image": json.RawMessage(`{"asset_ref":"image-1.png"}`)}
	if request, err := Generate(spec, values, RandomContext{}); err != nil || len(request.Files) != 1 {
		t.Fatalf("native file generation = %+v, %v", request, err)
	}
	workflows := []Workflow{
		{Mode: "submit", Steps: []Step{}},
		{Mode: "wait", Steps: []Step{}},
		{Mode: "sequence", Steps: []Step{{ID: "followup", Request: Request{Body: json.RawMessage(`{}`)}}}},
	}
	for _, workflow := range workflows {
		spec.Workflow = &workflow
		if err := spec.Validate(); err != nil {
			t.Fatalf("file workflow fixture is invalid: %v", err)
		}
		if _, err := Generate(spec, values, RandomContext{}); !errors.Is(err, ErrFileInputsUnsupported) {
			t.Fatalf("file workflow %q error = %v", workflow.Mode, err)
		}
	}
}

func TestRenderedRequestValidateRejectsInvalidBody(t *testing.T) {
	for _, body := range []string{
		``, `null`, `[]`, `"string"`, `{} {}`, `{"model":"bound"}`,
		strings.Repeat(" ", MaxDocumentBytes+1),
	} {
		request := RenderedRequest{Body: json.RawMessage(body), Files: []FileOccurrence{}}
		if err := request.Validate(); err == nil {
			t.Fatalf("invalid rendered body accepted: %.80q", body)
		}
	}
	if err := (RenderedRequest{Body: json.RawMessage(`{}`), Files: []FileOccurrence{}}).Validate(); err != nil {
		t.Fatalf("ordinary empty object rejected: %v", err)
	}
}

func TestRenderedRequestValidateRejectsInvalidOccurrence(t *testing.T) {
	base, err := Render(imageRenderInput(nativeFileTemplate))
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*RenderedRequest){
		"duplicate occurrence": func(request *RenderedRequest) {
			request.Files = append(request.Files, request.Files[0])
		},
		"invalid input name": func(request *RenderedRequest) { request.Files[0].InputName = "../image" },
		"unsafe reference":   func(request *RenderedRequest) { request.Files[0].AssetRef = "C:\\image.png" },
		"invalid metadata":   func(request *RenderedRequest) { request.Files[0].File.Encoding = "base64" },
		"invalid media type": func(request *RenderedRequest) { request.Files[0].File.MediaTypes = []string{"image/gif"} },
		"invalid byte bound": func(request *RenderedRequest) { request.Files[0].File.MaxBytes = MaxFileInputBytes + 1 },
		"wrong location": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/0/content/1/image_url"
		},
		"leading zero index": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/00/content/1/image_url/url"
		},
		"negative index": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/-1/content/1/image_url/url"
		},
		"missing slot": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/0/content/2/image_url/url"
		},
		"invalid pointer escape": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/0/content/1/image_url/u~2rl"
		},
		"nonnull slot": func(request *RenderedRequest) {
			request.Body = json.RawMessage(strings.Replace(
				string(request.Body),
				`"url":null`,
				`"url":"inline"`,
				1,
			))
		},
		"wrong content kind": func(request *RenderedRequest) {
			request.Body = json.RawMessage(strings.Replace(
				string(request.Body),
				`"type":"image_url"`,
				`"type":"text"`,
				1,
			))
		},
		"messages object": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/0/content/0/image_url/url"
			request.Body = json.RawMessage(`{"messages":{"0":{"content":[
				{"type":"image_url","image_url":{"url":null}}
			]}}}`)
		},
		"content object": func(request *RenderedRequest) {
			request.Files[0].Pointer = "/messages/0/content/0/image_url/url"
			request.Body = json.RawMessage(`{"messages":[{"content":{"0":
				{"type":"image_url","image_url":{"url":null}}
			}}]}`)
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := base.Clone()
			mutate(&request)
			if err := request.Validate(); !errors.Is(err, ErrFileInputsUnsupported) {
				t.Fatalf("invalid occurrence error = %v", err)
			}
		})
	}
}

func TestRandomSeedPathRemainsIndependentOfJSONPointerEscaping(t *testing.T) {
	position := renderPosition{seedPath: "body"}.child("a/b").child("c~d")
	if position.seedPath != "body/a/b/c~d" || position.pointer != "/a~1b/c~0d" {
		t.Fatalf("seed or JSON pointer path changed: %+v", position)
	}
}

func imageRenderInput(body string) RenderInput {
	return RenderInput{
		Request: Request{Body: json.RawMessage(body)},
		Declarations: map[string]Input{"image": {
			Type: "file", Required: true,
			File: &FileInput{
				Encoding: "image_data_url", MediaTypes: []string{"image/png", "image/jpeg"}, MaxBytes: MaxFileInputBytes,
			},
		}},
		Values: ResolvedInputs{
			JSONValues: map[string]json.RawMessage{},
			Files:      map[string]FileBinding{"image": {AssetRef: "image-1.png"}},
		},
		Responses: map[string]json.RawMessage{},
	}
}
