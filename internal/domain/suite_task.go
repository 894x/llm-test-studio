package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/894x/llm-test-studio/internal/testspec"
)

// SuiteInput explicitly maps one Suite parameter to declared Case input names.
type SuiteInput struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	testspec.Input
	Bindings []SuiteInputBinding `json:"bindings"`
}

type SuiteInputBinding struct {
	CaseID string `json:"case_id"`
	Input  string `json:"input"`
}

// UnmarshalJSON keeps the current flat Suite input shape while decoding its
// embedded Input through the same strict boundary as standalone declarations.
func (input *SuiteInput) UnmarshalJSON(raw []byte) error {
	if len(raw) > testspec.MaxDocumentBytes {
		return errors.New("suite input exceeds the JSON input limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return errors.New("suite input must be an object")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return errors.New("invalid suite input field")
		}
		name, ok := token.(string)
		if !ok {
			return errors.New("invalid suite input field")
		}
		if _, duplicate := fields[name]; duplicate {
			return fmt.Errorf("duplicate suite input field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("suite input must contain one JSON object")
	}
	var candidate SuiteInput
	for name, destination := range map[string]any{
		"key": &candidate.Key, "label": &candidate.Label, "bindings": &candidate.Bindings,
	} {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("suite input requires non-null %s", name)
		}
		fieldDecoder := json.NewDecoder(bytes.NewReader(value))
		fieldDecoder.DisallowUnknownFields()
		if err := fieldDecoder.Decode(destination); err != nil {
			return err
		}
		delete(fields, name)
	}
	declaration, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(declaration, &candidate.Input); err != nil {
		return err
	}
	*input = candidate
	return nil
}

func CloneSuiteInputs(inputs []SuiteInput) []SuiteInput {
	result := make([]SuiteInput, len(inputs))
	for index, input := range inputs {
		result[index] = input
		result[index].Input = input.Input.Clone()
		result[index].Bindings = append([]SuiteInputBinding{}, input.Bindings...)
	}
	return result
}

func ValidateSuiteInputs(inputs []SuiteInput) error {
	if inputs == nil {
		return errors.New("suite inputs must be present; use an empty list for fixed cases")
	}
	names := make(map[string]struct{}, len(inputs))
	bindings := make(map[SuiteInputBinding]struct{})
	for _, input := range inputs {
		if !isSafeCaseKey(input.Key) || strings.TrimSpace(input.Label) == "" {
			return fmt.Errorf("invalid suite input %q", input.Key)
		}
		if _, exists := names[input.Key]; exists {
			return fmt.Errorf("duplicate suite input %q", input.Key)
		}
		names[input.Key] = struct{}{}
		if err := input.Input.Validate(); err != nil {
			return err
		}
		if len(input.Bindings) == 0 {
			return fmt.Errorf("suite input %q requires bindings", input.Key)
		}
		for _, binding := range input.Bindings {
			if !IsUUID(binding.CaseID) || !isSafeCaseKey(binding.Input) {
				return fmt.Errorf("invalid binding for suite input %q", input.Key)
			}
			if _, exists := bindings[binding]; exists {
				return fmt.Errorf("duplicate suite input binding %q", input.Key)
			}
			bindings[binding] = struct{}{}
		}
	}
	return nil
}

// ValidateCases resolves membership, protocol and explicit file binding types.
// Ordinary parameter and supplied-value compatibility are checked by its resolver.
func (suite Suite) ValidateCases(cases []TestCase) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	if len(cases) != len(suite.Cases) {
		return errors.New("suite requires every referenced case definition")
	}
	for index, testCase := range cases {
		if testCase.ID != suite.Cases[index].CaseID {
			return errors.New("suite case definition does not match its reference")
		}
		if err := testCase.Validate(); err != nil {
			return err
		}
		if !suite.AcceptsCase(testCase) {
			return fmt.Errorf("Case %s has no definition for Suite protocol %s", testCase.Key, suite.Protocol)
		}
		spec, err := testCase.SpecFor(suite.Protocol)
		if err != nil {
			return err
		}
		if err := suite.validateFileInputBindings(testCase.ID, spec.Inputs); err != nil {
			return err
		}
	}
	return nil
}

func (suite Suite) validateFileInputBindings(caseID string, declarations map[string]testspec.Input) error {
	for _, input := range suite.Inputs {
		for _, binding := range input.Bindings {
			if binding.CaseID != caseID {
				continue
			}
			caseInput := declarations[binding.Input]
			if (input.Type == "file") != (caseInput.Type == "file") {
				return fmt.Errorf(
					"suite input %q and Case input %q must both declare file or ordinary JSON",
					input.Key, binding.Input,
				)
			}
		}
	}
	return nil
}

func (suite Suite) AcceptsCase(testCase TestCase) bool {
	return testCase.SupportsProtocol(suite.Protocol)
}

func (suite Suite) HasFileInputs() bool {
	for _, input := range suite.Inputs {
		if input.Type == "file" {
			return true
		}
	}
	return false
}

// ResolveInputs applies Suite defaults and explicit bindings without altering Case definitions.
func (suite Suite) ResolveInputs(values map[string]json.RawMessage) (map[string]json.RawMessage, map[string]map[string]json.RawMessage, error) {
	if err := suite.Validate(); err != nil {
		return nil, nil, err
	}
	declarations := make(map[string]testspec.Input, len(suite.Inputs))
	members := make(map[string]struct{}, len(suite.Cases))
	caseInputs := make(map[string]map[string]json.RawMessage, len(suite.Cases))
	for _, member := range suite.Cases {
		members[member.CaseID] = struct{}{}
		caseInputs[member.CaseID] = map[string]json.RawMessage{}
	}
	for _, input := range suite.Inputs {
		declarations[input.Key] = input.Input
	}
	resolved, err := testspec.ValidateInputs(declarations, values)
	if err != nil {
		return nil, nil, err
	}
	frozen := resolved.SnapshotValues()
	for _, input := range suite.Inputs {
		for _, binding := range input.Bindings {
			if _, exists := members[binding.CaseID]; !exists {
				return nil, nil, fmt.Errorf("suite input %q references an unknown member", input.Key)
			}
			if value, exists := frozen[input.Key]; exists {
				caseInputs[binding.CaseID][binding.Input] = append(json.RawMessage(nil), value...)
			}
		}
	}
	return frozen, caseInputs, nil
}
