package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

// SuiteQuickTest makes a Suite available as a lightweight testing task. The
// editable inputs bind only to existing request-body fields in its Cases.
type SuiteQuickTest struct {
	Description string       `json:"description"`
	TimeoutMS   uint64       `json:"timeout_ms"`
	Inputs      []SuiteInput `json:"inputs"`
}

type SuiteInput struct {
	Key      string              `json:"key"`
	Label    string              `json:"label"`
	Type     string              `json:"type"`
	Default  json.RawMessage     `json:"default"`
	Bindings []SuiteInputBinding `json:"bindings"`
}

type SuiteInputBinding struct {
	CaseKey string `json:"case_key"`
	Pointer string `json:"pointer"`
}

func (profile *SuiteQuickTest) Clone() *SuiteQuickTest {
	if profile == nil {
		return nil
	}
	result := *profile
	if profile.Inputs != nil {
		result.Inputs = make([]SuiteInput, len(profile.Inputs))
		copy(result.Inputs, profile.Inputs)
	}
	for i := range result.Inputs {
		result.Inputs[i].Default = append(json.RawMessage(nil), profile.Inputs[i].Default...)
		result.Inputs[i].Bindings = append([]SuiteInputBinding(nil), profile.Inputs[i].Bindings...)
	}
	return &result
}

func (profile *SuiteQuickTest) Validate() error {
	if profile == nil {
		return nil
	}
	if strings.TrimSpace(profile.Description) == "" || profile.TimeoutMS == 0 || profile.TimeoutMS > 3_600_000 {
		return errors.New("quick test requires a description and timeout up to one hour")
	}
	if profile.Inputs == nil {
		return errors.New("quick test requires an input list; use an empty list for a fixed task")
	}
	keys := make(map[string]bool)
	bindings := make(map[SuiteInputBinding]bool)
	for _, input := range profile.Inputs {
		if !isSafeCaseKey(input.Key) || keys[input.Key] || strings.TrimSpace(input.Label) == "" || len(input.Bindings) == 0 {
			return fmt.Errorf("invalid quick test input %q", input.Key)
		}
		keys[input.Key] = true
		var value any
		if json.Unmarshal(input.Default, &value) != nil || !input.Accepts(value) {
			return fmt.Errorf("invalid default for quick test input %q", input.Key)
		}
		for _, binding := range input.Bindings {
			tokens, valid := jsonpointer.Parse(binding.Pointer)
			if !isSafeCaseKey(binding.CaseKey) || !valid || !strings.HasPrefix(binding.Pointer, "/request/body/") || bindings[binding] || (len(tokens) > 2 && tokens[2] == "model") {
				return fmt.Errorf("invalid binding for quick test input %q", input.Key)
			}
			bindings[binding] = true
		}
	}
	return nil
}

func (input SuiteInput) Accepts(value any) bool {
	switch input.Type {
	case "text":
		_, ok := value.(string)
		return ok
	case "number":
		switch value.(type) {
		case float64, json.Number:
			return true
		}
		return false
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

// ValidateCases checks exact membership and model applicability, then checks
// editable inputs against those pinned definitions. Definitions follow the
// same order as suite.Cases.
func (suite Suite) ValidateCases(cases []TestCase) error {
	if err := suite.Validate(); err != nil {
		return err
	}
	if len(cases) != len(suite.Cases) {
		return errors.New("suite requires every pinned case definition")
	}
	byKey := make(map[string]TestCase, len(cases))
	for index, testCase := range cases {
		ref := suite.Cases[index]
		if testCase.ID != ref.CaseID || testCase.Revision != ref.Revision || testCase.Validate() != nil || !suite.AcceptsCase(testCase) {
			return errors.New("suite case definition does not match its pinned reference or model")
		}
		if suite.QuickTest != nil && (!testCase.Enabled || testCase.ExecutionMode != CaseExecutionAutomatic) {
			return errors.New("quick test requires enabled automatic cases")
		}
		byKey[testCase.Key] = testCase
	}
	if suite.QuickTest == nil {
		return nil
	}
	for _, input := range suite.QuickTest.Inputs {
		for _, binding := range input.Bindings {
			testCase, found := byKey[binding.CaseKey]
			if !found {
				return fmt.Errorf("quick test input %q references unknown case %q", input.Key, binding.CaseKey)
			}
			var spec any
			if json.Unmarshal(testCase.Definition.Spec, &spec) != nil {
				return errors.New("invalid quick test case spec")
			}
			value, found := jsonpointer.Lookup(spec, binding.Pointer)
			if !found || !input.Accepts(value) {
				return fmt.Errorf("quick test input %q does not match case field %q", input.Key, binding.Pointer)
			}
		}
	}
	return nil
}

// AcceptsCase preserves explicit model applicability while allowing a generic
// quick Suite to use only Cases that apply to every model of its protocol.
func (suite Suite) AcceptsCase(testCase TestCase) bool {
	if testCase.Protocol != suite.Protocol {
		return false
	}
	if suite.ModelTarget == "" {
		return suite.QuickTest != nil && len(testCase.ModelTargets) == 0
	}
	return testCase.AppliesToModel(suite.ModelTarget)
}
