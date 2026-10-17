package testspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
)

const MaxFileInputBytes = 1 << 20

var ErrFileInputsUnsupported = errors.New(
	"file inputs require an admitted asset resolver; file execution is unsupported",
)

var logicalAssetRef = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,79}$`)

type FileInput struct {
	Encoding   string   `json:"encoding"`
	MediaTypes []string `json:"media_types"`
	MaxBytes   int64    `json:"max_bytes"`
}

// FileBinding identifies owned content logically; it never names a local path.
type FileBinding struct {
	AssetRef string `json:"asset_ref"`
}

// ResolvedInputs keeps file bindings out of ordinary JSON template values.
type ResolvedInputs struct {
	JSONValues map[string]json.RawMessage
	Files      map[string]FileBinding
}

// SnapshotValues projects the current logical values into the snapshot's single
// Parameters/CaseInputs representation. It does not resolve or read content.
func (inputs ResolvedInputs) SnapshotValues() map[string]json.RawMessage {
	values := make(map[string]json.RawMessage, len(inputs.JSONValues)+len(inputs.Files))
	for name, value := range inputs.JSONValues {
		values[name] = bytes.Clone(value)
	}
	for name, binding := range inputs.Files {
		values[name], _ = json.Marshal(binding)
	}
	return values
}

func (input Input) Clone() Input {
	input.Default = bytes.Clone(input.Default)
	values := make([]json.RawMessage, len(input.Enum))
	for index, value := range input.Enum {
		values[index] = bytes.Clone(value)
	}
	input.Enum = values
	if input.Minimum != nil {
		value := *input.Minimum
		input.Minimum = &value
	}
	if input.Maximum != nil {
		value := *input.Maximum
		input.Maximum = &value
	}
	if input.File != nil {
		file := *input.File
		file.MediaTypes = append([]string{}, file.MediaTypes...)
		input.File = &file
	}
	return input
}

func (input *Input) UnmarshalJSON(raw []byte) error {
	fields, err := strictObject(raw, []string{
		"type", "description", "unit", "default", "required", "minimum", "maximum", "enum", "file",
	})
	if err != nil {
		return fmt.Errorf("invalid input declaration: %w", err)
	}
	if err := rejectNullFields(fields); err != nil {
		return err
	}
	type declaration Input
	var candidate declaration
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return fmt.Errorf("invalid input declaration: %w", err)
	}
	value := Input(candidate)
	if value.Type == "file" {
		for _, field := range []string{"default", "enum", "minimum", "maximum", "unit"} {
			if _, exists := fields[field]; exists {
				return fmt.Errorf("file inputs cannot declare %s", field)
			}
		}
	}
	if err := value.Validate(); err != nil {
		return err
	}
	*input = value
	return nil
}

func (file *FileInput) UnmarshalJSON(raw []byte) error {
	fields, err := strictObject(raw, []string{"encoding", "media_types", "max_bytes"})
	if err != nil {
		return fmt.Errorf("invalid file input: %w", err)
	}
	if len(fields) != 3 {
		return errors.New("file input requires encoding, media_types and max_bytes")
	}
	if err := rejectNullFields(fields); err != nil {
		return err
	}
	type declaration FileInput
	var candidate declaration
	if err := json.Unmarshal(raw, &candidate); err != nil {
		return fmt.Errorf("invalid file input: %w", err)
	}
	value := FileInput(candidate)
	if err := value.Validate(); err != nil {
		return err
	}
	*file = value
	return nil
}

func (file FileInput) Validate() error {
	if file.Encoding != "image_data_url" {
		return errors.New("file encoding must be image_data_url")
	}
	if file.MaxBytes < 1 || file.MaxBytes > MaxFileInputBytes {
		return errors.New("file max_bytes must be between 1 and 1048576")
	}
	if len(file.MediaTypes) < 1 || len(file.MediaTypes) > 2 {
		return errors.New("file media_types must be a nonempty PNG/JPEG subset")
	}
	seen := make(map[string]bool, len(file.MediaTypes))
	for _, mediaType := range file.MediaTypes {
		if mediaType != "image/png" && mediaType != "image/jpeg" {
			return errors.New("file media_types supports only image/png and image/jpeg")
		}
		if seen[mediaType] {
			return errors.New("file media_types must not contain duplicates")
		}
		seen[mediaType] = true
	}
	return nil
}

func (binding *FileBinding) UnmarshalJSON(raw []byte) error {
	fields, err := strictObject(raw, []string{"asset_ref"})
	if err != nil {
		return fmt.Errorf("invalid file binding: %w", err)
	}
	if len(fields) != 1 {
		return errors.New("file binding requires only asset_ref")
	}
	if err := rejectNullFields(fields); err != nil {
		return err
	}
	var reference string
	if err := json.Unmarshal(fields["asset_ref"], &reference); err != nil || !logicalAssetRef.MatchString(reference) {
		return errors.New(
			"file asset_ref must be a logical identifier of 1 to 80 lowercase letters, digits, dots, underscores or hyphens",
		)
	}
	*binding = FileBinding{AssetRef: reference}
	return nil
}

// strictObject validates owned object keys without interpreting native JSON leaves.
func strictObject(raw []byte, allowed []string) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes {
		return nil, errors.New("object exceeds the JSON input limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, errors.New("value must be a JSON object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errors.New("invalid object field")
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("invalid object field")
		}
		if _, duplicate := fields[name]; duplicate {
			return nil, fmt.Errorf("duplicate field %q", name)
		}
		if allowed != nil && !slices.Contains(allowed, name) {
			return nil, fmt.Errorf("unknown field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, errors.New("invalid object value")
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errors.New("invalid object ending")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("object must contain exactly one JSON value")
	}
	return fields, nil
}

func rejectNullFields(fields map[string]json.RawMessage) error {
	for name, value := range fields {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("field %q must not be null", name)
		}
	}
	return nil
}
