package domain

import (
	"encoding/json"
	"errors"
	"fmt"
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

func CloneSuiteInputs(inputs []SuiteInput) []SuiteInput {
	result := make([]SuiteInput, len(inputs))
	for index, input := range inputs {
		result[index] = input
		result[index].Default = append(json.RawMessage(nil), input.Default...)
		result[index].Enum = make([]json.RawMessage, len(input.Enum))
		for j, value := range input.Enum {
			result[index].Enum[j] = append(json.RawMessage(nil), value...)
		}
		if input.Minimum != nil {
			value := *input.Minimum
			result[index].Minimum = &value
		}
		if input.Maximum != nil {
			value := *input.Maximum
			result[index].Maximum = &value
		}
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

// ValidateCases resolves membership and protocol only when a Run is prepared.
// Parameter declaration and supplied-value compatibility are checked by its resolver.
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
			return errors.New("suite case protocol does not match suite protocol")
		}
	}
	return nil
}

func (suite Suite) AcceptsCase(testCase TestCase) bool { return testCase.Protocol == suite.Protocol }

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
	for _, input := range suite.Inputs {
		for _, binding := range input.Bindings {
			if _, exists := members[binding.CaseID]; !exists {
				return nil, nil, fmt.Errorf("suite input %q references an unknown member", input.Key)
			}
			if value, exists := resolved[input.Key]; exists {
				caseInputs[binding.CaseID][binding.Input] = append(json.RawMessage(nil), value...)
			}
		}
	}
	return resolved, caseInputs, nil
}
