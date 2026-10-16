package testspec

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const fileDeclarationJSON = `{"type":"file","required":true,"file":{"encoding":"image_data_url","media_types":["image/png","image/jpeg"],"max_bytes":1048576}}`

func TestFileInputDeclarationCurrentShape(t *testing.T) {
	for _, raw := range []string{
		fileDeclarationJSON,
		strings.Replace(fileDeclarationJSON, `1048576`, `1`, 1),
		strings.Replace(fileDeclarationJSON, `["image/png","image/jpeg"]`, `["image/png"]`, 1),
		strings.Replace(fileDeclarationJSON, `["image/png","image/jpeg"]`, `["image/jpeg"]`, 1),
	} {
		var input Input
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			t.Fatalf("valid declaration rejected: %s: %v", raw, err)
		}
		if input.Type != "file" || !input.Required || input.File == nil {
			t.Fatalf("declaration lost file contract: %+v", input)
		}
	}
	invalid := []string{
		`null`,
		`{"type":"file","required":true}`,
		`{"type":"file","required":true,"file":null}`,
		strings.Replace(fileDeclarationJSON, `"required":true`, `"required":false`, 1),
		strings.Replace(fileDeclarationJSON, `"required":true,`, ``, 1),
		strings.Replace(fileDeclarationJSON, `"file"`, `"File"`, 1),
		strings.Replace(fileDeclarationJSON, `"type":"file"`, `"type":"object"`, 1),
		strings.Replace(fileDeclarationJSON, `1048576`, `0`, 1),
		strings.Replace(fileDeclarationJSON, `1048576`, `1048577`, 1),
		strings.Replace(fileDeclarationJSON, `1048576`, `1.5`, 1),
		strings.Replace(fileDeclarationJSON, `1048576`, `null`, 1),
		strings.Replace(fileDeclarationJSON, `image_data_url`, `base64`, 1),
		strings.Replace(fileDeclarationJSON, `["image/png","image/jpeg"]`, `[]`, 1),
		strings.Replace(fileDeclarationJSON, `["image/png","image/jpeg"]`, `null`, 1),
		strings.Replace(fileDeclarationJSON, `"image/jpeg"`, `"image/png"`, 1),
		strings.Replace(fileDeclarationJSON, `"image/jpeg"`, `"image/gif"`, 1),
		strings.Replace(fileDeclarationJSON, `"image/jpeg"`, `null`, 1),
		strings.Replace(fileDeclarationJSON, `"encoding":`, `"old":true,"encoding":`, 1),
		strings.Replace(fileDeclarationJSON, `"encoding":`, `"encoding":"image_data_url","encoding":`, 1),
		strings.Replace(fileDeclarationJSON, `"max_bytes":1048576`, `"max_bytes":1048576,"max_bytes":1`, 1),
		strings.Replace(fileDeclarationJSON, `"media_types":`, `"media_types":["image/png"],"media_types":`, 1),
		strings.Replace(fileDeclarationJSON, `"type":`, `"type":"object","type":`, 1),
		strings.Replace(fileDeclarationJSON, `"file":`, `"file":{},"file":`, 1),
		strings.Replace(fileDeclarationJSON, `"required":`, `"required":true,"required":`, 1),
		strings.Replace(fileDeclarationJSON, `"required":`, `"unknown":true,"required":`, 1),
	}
	for _, field := range []string{`"default":{}`, `"enum":[]`, `"minimum":0`, `"maximum":1`, `"unit":""`} {
		invalid = append(invalid, strings.Replace(fileDeclarationJSON, `"required":`, field+`,"required":`, 1))
	}
	for _, raw := range invalid {
		original := Input{Type: "string", Default: json.RawMessage(`"keep"`)}
		candidate := original.Clone()
		if err := json.Unmarshal([]byte(raw), &candidate); err == nil {
			t.Errorf("invalid declaration accepted: %s", raw)
		}
		if !reflect.DeepEqual(candidate, original.Clone()) {
			t.Errorf("rejected declaration mutated destination: %s", raw)
		}
	}
}

func TestFileBindingUsesOnlyStrictLogicalReference(t *testing.T) {
	var input Input
	if err := json.Unmarshal([]byte(fileDeclarationJSON), &input); err != nil {
		t.Fatal(err)
	}
	for _, reference := range []string{"x", "image-1.png", strings.Repeat("a", 80)} {
		value, _ := json.Marshal(FileBinding{AssetRef: reference})
		resolved, err := ValidateInputs(map[string]Input{"image": input}, map[string]json.RawMessage{"image": value})
		if err != nil || resolved.Files["image"].AssetRef != reference || len(resolved.JSONValues) != 0 {
			t.Fatalf("logical binding resolution: %+v, %v", resolved, err)
		}
	}
	for _, raw := range []string{
		`null`, `{}`, `"image.png"`, `{"asset_ref":null}`, `{"asset_ref":1}`,
		`{"asset_ref":"x","path":"image.png"}`, `{"asset_ref":"x","asset_ref":"y"}`,
		`{"Asset_Ref":"x"}`, `{"asset_ref":""}`, `{"asset_ref":"../image.png"}`,
		`{"asset_ref":"C:\\image.png"}`, `{"asset_ref":"/tmp/image.png"}`, `{"asset_ref":"image/a"}`,
		`{"asset_ref":"https://example.test/image.png"}`, `{"asset_ref":"image\\a"}`,
		`{"asset_ref":"data:image/png;base64,AA=="}`, `{"asset_ref":"IMAGE"}`,
		`{"asset_ref":"` + strings.Repeat("a", 81) + `"}`,
		`{"asset_ref":"x"} {"asset_ref":"y"}`,
	} {
		supplied := map[string]json.RawMessage{"image": json.RawMessage(raw)}
		if _, err := ValidateInputs(map[string]Input{"image": input}, supplied); err == nil {
			t.Errorf("invalid binding accepted: %s", raw)
		}
		if string(supplied["image"]) != raw {
			t.Fatal("binding validation mutated supplied data")
		}
	}
	if _, err := ValidateInputs(map[string]Input{"image": input}, nil); err == nil {
		t.Fatal("missing required file binding accepted")
	}
}

func TestResolvedInputsKeepOrdinaryAssetObjectAndInlineValuesAsJSON(t *testing.T) {
	declarations := map[string]Input{
		"object": {Type: "object"}, "url": {Type: "string"}, "inline": {Type: "string"},
	}
	supplied := map[string]json.RawMessage{
		"object": json.RawMessage(`{"asset_ref":"../ordinary-value","extra":null}`),
		"url":    json.RawMessage(`"https://example.test/image.png"`),
		"inline": json.RawMessage(`"data:image/png;base64,AA=="`),
	}
	resolved, err := ValidateInputs(declarations, supplied)
	if err != nil || len(resolved.Files) != 0 || len(resolved.JSONValues) != 3 {
		t.Fatalf("ordinary inputs changed meaning: %+v, %v", resolved, err)
	}
	request := Request{Body: json.RawMessage(`{"object":{"$input":"object"},"url":{"$input":"url"},"inline":{"$input":"inline"}}`)}
	body, err := Render(
		request,
		resolved,
		RandomContext{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	for name, raw := range supplied {
		var original, generated any
		_ = json.Unmarshal(raw, &original)
		_ = json.Unmarshal(decoded[name], &generated)
		if !reflect.DeepEqual(original, generated) {
			t.Fatalf("ordinary value %s changed", name)
		}
	}
	frozen := resolved.SnapshotValues()
	frozen["object"][0] = '['
	if resolved.JSONValues["object"][0] != '{' || supplied["object"][0] != '{' {
		t.Fatal("snapshot values alias input storage")
	}
}

func TestFileInputsCannotRenderOrGenerate(t *testing.T) {
	raw := `{"inputs":{"image":` + fileDeclarationJSON + `},"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`
	spec, err := Decode(json.RawMessage(raw))
	if err != nil || !spec.HasFileInputs() {
		t.Fatalf("file fixture rejected: %v", err)
	}
	supplied := map[string]json.RawMessage{"image": json.RawMessage(`{"asset_ref":"image"}`)}
	resolved, err := ValidateInputs(spec.Inputs, supplied)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(spec, supplied, RandomContext{}); !errors.Is(err, ErrFileInputsUnsupported) {
		t.Fatalf("file generation error = %v", err)
	}
	if _, err := Render(
		Request{Body: json.RawMessage(`{}`)},
		resolved,
		RandomContext{},
		nil,
	); !errors.Is(err, ErrFileInputsUnsupported) {
		t.Fatalf("unused file binding rendering error = %v", err)
	}
	for _, duplicate := range []string{
		strings.Replace(raw, `"inputs":`, `"inputs":{},"inputs":`, 1),
		strings.Replace(raw, `"image":`+fileDeclarationJSON, `"image":`+fileDeclarationJSON+`,"image":`+fileDeclarationJSON, 1),
	} {
		if _, err := Decode(json.RawMessage(duplicate)); err == nil {
			t.Fatal("duplicate input declaration accepted")
		}
	}
}

func TestInputCloneOwnsFileDeclaration(t *testing.T) {
	var input Input
	if err := json.Unmarshal([]byte(fileDeclarationJSON), &input); err != nil {
		t.Fatal(err)
	}
	clone := input.Clone()
	clone.File.Encoding = "changed"
	clone.File.MediaTypes[0] = "changed"
	clone.File.MaxBytes = 1
	if input.File.Encoding != "image_data_url" || input.File.MediaTypes[0] != "image/png" || input.File.MaxBytes != MaxFileInputBytes {
		t.Fatal("cloned file declaration aliases source")
	}
}
