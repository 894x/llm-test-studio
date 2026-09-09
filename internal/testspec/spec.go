package testspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

func Decode(raw json.RawMessage) (Spec, error) {
	var spec Spec
	if len(raw) > MaxDocumentBytes {
		return spec, errors.New("case spec exceeds the 4 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return spec, errors.New("case spec must use inputs, request.body, assertions and optional workflow")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return spec, errors.New("case spec must contain exactly one JSON object")
	}
	return spec, spec.Validate()
}

func (spec Spec) Validate() error {
	if spec.Inputs == nil || spec.Assertions == nil {
		return errors.New("case inputs must be an object and assertions must be an array")
	}
	if len(spec.Inputs) > 128 || len(spec.Assertions) > 256 {
		return errors.New("case exceeds the input or assertion count limit")
	}
	for name, input := range spec.Inputs {
		if !validName(name) {
			return errors.New("input names must be identifiers of at most 128 characters")
		}
		if err := input.Validate(); err != nil {
			return fmt.Errorf("input %s: %w", name, err)
		}
	}
	if err := validateTemplate(spec.Request.Body, spec.Inputs, map[string]bool{}); err != nil {
		return err
	}
	if spec.Workflow != nil {
		if err := spec.validateWorkflow(); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, assertion := range spec.Assertions {
		if err := validateAssertion(assertion, 0, seen); err != nil {
			return err
		}
	}
	return nil
}

func (spec Spec) validateWorkflow() error {
	switch spec.Workflow.Mode {
	case "submit", "wait":
		if len(spec.Workflow.Steps) != 0 {
			return errors.New("submit and wait workflows cannot contain conversation steps")
		}
	case "sequence":
		if len(spec.Workflow.Steps) == 0 || len(spec.Workflow.Steps) > 32 {
			return errors.New("sequence workflow requires 1 to 32 follow-up steps")
		}
	default:
		return errors.New("workflow mode must be submit, wait, or sequence")
	}
	seen := map[string]bool{"initial": true}
	for _, step := range spec.Workflow.Steps {
		if !validName(step.ID) || seen[step.ID] {
			return errors.New("workflow step IDs must be unique identifiers; initial is reserved")
		}
		if err := validateTemplate(step.Request.Body, spec.Inputs, seen); err != nil {
			return fmt.Errorf("step %s: %w", step.ID, err)
		}
		seen[step.ID] = true
	}
	return nil
}

func (input Input) Validate() error {
	switch input.Type {
	case "string", "integer", "number", "boolean", "object", "array":
	default:
		return errors.New("unsupported input type")
	}
	if len(input.Description) > 1024 || len(input.Unit) > 64 || len(input.Enum) > 256 {
		return errors.New("input metadata exceeds its limit")
	}
	if input.Minimum != nil && (math.IsNaN(*input.Minimum) || math.IsInf(*input.Minimum, 0)) {
		return errors.New("minimum must be finite")
	}
	if input.Maximum != nil && (math.IsNaN(*input.Maximum) || math.IsInf(*input.Maximum, 0)) {
		return errors.New("maximum must be finite")
	}
	if input.Minimum != nil && input.Maximum != nil && *input.Minimum > *input.Maximum {
		return errors.New("minimum exceeds maximum")
	}
	for _, candidate := range input.Enum {
		if err := validateInputValue(input, candidate, false); err != nil {
			return fmt.Errorf("invalid enum value: %w", err)
		}
	}
	if len(input.Default) != 0 {
		if err := validateInputValue(input, input.Default, true); err != nil {
			return fmt.Errorf("invalid default: %w", err)
		}
	}
	return nil
}

// ValidateInputs resolves declared defaults and rejects undeclared assignments.
// It validates authoring parameters, not the provider's request-body contract.
func ValidateInputs(declarations map[string]Input, supplied map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	resolved := make(map[string]json.RawMessage, len(declarations))
	for name := range supplied {
		if _, exists := declarations[name]; !exists {
			return nil, errors.New("an assigned input is not declared by this case")
		}
	}
	for name, definition := range declarations {
		value, exists := supplied[name]
		if !exists {
			value = definition.Default
		}
		if len(value) == 0 {
			if definition.Required {
				return nil, fmt.Errorf("required input %s has no value", name)
			}
			continue
		}
		if err := validateInputValue(definition, value, true); err != nil {
			return nil, fmt.Errorf("input %s: %w", name, err)
		}
		resolved[name] = append(json.RawMessage{}, value...)
	}
	return resolved, nil
}

func validateInputValue(input Input, raw json.RawMessage, checkEnum bool) error {
	value, err := decodeValue(raw)
	if err != nil {
		return errors.New("value is not bounded valid JSON")
	}
	actualType := valueType(value)
	validType := actualType == input.Type
	if input.Type == "integer" {
		number, ok := numeric(value)
		validType = ok && math.Trunc(number) == number
	}
	if !validType {
		return fmt.Errorf("value must have type %s", input.Type)
	}
	if input.Minimum != nil || input.Maximum != nil {
		number, ok := numeric(value)
		if !ok {
			return errors.New("numeric bounds require a numeric input")
		}
		if input.Minimum != nil && number < *input.Minimum {
			return errors.New("value is below minimum")
		}
		if input.Maximum != nil && number > *input.Maximum {
			return errors.New("value exceeds maximum")
		}
	}
	if checkEnum && len(input.Enum) > 0 {
		for _, allowed := range input.Enum {
			candidate, _ := decodeValue(allowed)
			if jsonEqual(value, candidate) {
				return nil
			}
		}
		return errors.New("value is not in the declared enum")
	}
	return nil
}

func decodeValue(raw json.RawMessage) (any, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes || !json.Valid(raw) {
		return nil, errors.New("invalid JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, errors.New("invalid JSON value")
	}
	return value, nil
}

func validName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_.-", character) {
			return false
		}
	}
	return true
}
