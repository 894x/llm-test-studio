package testspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

type RenderInput struct {
	Request      Request
	Declarations map[string]Input
	Values       ResolvedInputs
	Random       RandomContext
	Responses    map[string]json.RawMessage
}

type FileOccurrence struct {
	InputName string
	Pointer   string
	AssetRef  string
	File      FileInput
}

// RenderedRequest contains unresolved file slots, never file bytes. Each file's
// JSON value is null until the protocol boundary prepares its owned wire body.
type RenderedRequest struct {
	Body  json.RawMessage
	Files []FileOccurrence
}

func (request RenderedRequest) Clone() RenderedRequest {
	request.Body = bytes.Clone(request.Body)
	files := make([]FileOccurrence, len(request.Files))
	for index, occurrence := range request.Files {
		occurrence.File.MediaTypes = append([]string{}, occurrence.File.MediaTypes...)
		files[index] = occurrence
	}
	request.Files = files
	return request
}

func (request RenderedRequest) Validate() error {
	root, err := decodeValue(request.Body)
	if err != nil {
		return errors.New("rendered request must contain bounded valid JSON")
	}
	object, ok := root.(map[string]any)
	if !ok || object == nil {
		return errors.New("rendered request body must be an object")
	}
	if _, exists := object["model"]; exists {
		return errors.New("rendered model conflicts with run binding")
	}
	return request.validateFiles(object)
}

func (request RenderedRequest) validateFiles(root map[string]any) error {
	if len(request.Files) > 1 {
		return unsupportedRenderedFile("a request supports at most one file occurrence")
	}
	for _, occurrence := range request.Files {
		if !validName(occurrence.InputName) || !logicalAssetRef.MatchString(occurrence.AssetRef) {
			return unsupportedRenderedFile("file occurrence requires a declared input and logical asset reference")
		}
		if err := occurrence.File.Validate(); err != nil {
			return unsupportedRenderedFile("file occurrence metadata is invalid")
		}
		tokens, valid := imageFilePointer(occurrence.Pointer)
		if !valid {
			return unsupportedRenderedFile("file occurrence must replace an image_url.url value")
		}
		if _, ok := root["messages"].([]any); !ok {
			return unsupportedRenderedFile("file occurrence requires a native messages array")
		}
		content, _ := jsonpointer.Lookup(root, "/messages/"+tokens[1]+"/content")
		if _, ok := content.([]any); !ok {
			return unsupportedRenderedFile("file occurrence requires a native content array")
		}
		value, found := jsonpointer.Lookup(root, occurrence.Pointer)
		if !found || value != nil {
			return unsupportedRenderedFile("file occurrence must have an existing null placeholder")
		}
		contentPointer := "/" + strings.Join(tokens[:4], "/")
		content, found = jsonpointer.Lookup(root, contentPointer)
		object, ok := content.(map[string]any)
		nativeImage := found && ok && object["type"] == "image_url"
		if !nativeImage {
			return unsupportedRenderedFile("file occurrence requires native image_url content")
		}
	}
	return nil
}

func (input RenderInput) validateFileBindings() error {
	if len(input.Values.Files) > 1 {
		return unsupportedRenderedFile("a request supports at most one file binding")
	}
	if len(input.Values.Files) != 0 && len(input.Responses) != 0 {
		return unsupportedRenderedFile("file inputs do not support response or workflow rendering")
	}
	for name, declaration := range input.Declarations {
		if declaration.Type != "file" {
			continue
		}
		if err := declaration.Validate(); err != nil {
			return unsupportedRenderedFile("file input declaration is invalid")
		}
		if _, exists := input.Values.Files[name]; !exists {
			return unsupportedRenderedFile("declared file input has no file binding")
		}
	}
	for name, binding := range input.Values.Files {
		declaration, exists := input.Declarations[name]
		declaredFile := exists && declaration.Type == "file"
		if !declaredFile || !validName(name) {
			return unsupportedRenderedFile("file binding must name an explicit file declaration")
		}
		if _, overlaps := input.Values.JSONValues[name]; overlaps {
			return unsupportedRenderedFile("a file binding cannot also be an ordinary JSON value")
		}
		if !logicalAssetRef.MatchString(binding.AssetRef) {
			return unsupportedRenderedFile("file binding must use a logical asset reference")
		}
	}
	return nil
}

func imageFilePointer(pointer string) ([]string, bool) {
	if len(pointer) > 128 {
		return nil, false
	}
	tokens, valid := jsonpointer.Parse(pointer)
	if !valid || len(tokens) != 6 {
		return nil, false
	}
	validParents := tokens[0] == "messages" && tokens[2] == "content" && tokens[4] == "image_url"
	if !validParents || tokens[5] != "url" {
		return nil, false
	}
	for _, token := range []string{tokens[1], tokens[3]} {
		index, err := strconv.Atoi(token)
		canonicalIndex := index >= 0 && strconv.Itoa(index) == token
		if err != nil || !canonicalIndex {
			return nil, false
		}
	}
	return tokens, true
}

func unsupportedRenderedFile(reason string) error {
	return fmt.Errorf("%w: %s", ErrFileInputsUnsupported, reason)
}
