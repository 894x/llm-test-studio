package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

// QuickTaskSnapshot identifies an operational task invocation. PlanDocument and
// Mapping in the enclosing Run are transient execution configuration, not
// authored catalog records. CaseDefinitions retain the original Suite members.
type QuickTaskSnapshot struct {
	Suite          Suite                      `json:"suite"`
	Inputs         map[string]json.RawMessage `json:"inputs"`
	SavedChannelID string                     `json:"saved_channel_id,omitempty"`
}

func (task *QuickTaskSnapshot) validate(snapshot RunSnapshot) error {
	if task.Suite.QuickTest == nil || task.Inputs == nil || len(task.Inputs) != len(task.Suite.QuickTest.Inputs) {
		return errors.New("quick run requires a task and every resolved input")
	}
	if task.SavedChannelID != "" && (!IsUUID(task.SavedChannelID) || task.SavedChannelID != snapshot.Channel.ID) {
		return errors.New("quick run saved channel does not match its target")
	}
	if task.Suite.Protocol != snapshot.Model.Protocol || (task.Suite.ModelTarget != "" && task.Suite.ModelTarget != snapshot.Channel.UpstreamModelName) {
		return errors.New("quick run target does not match its Suite")
	}
	if snapshot.PlanDocument.SuiteID != task.Suite.ID || snapshot.PlanDocument.SuiteRevision != task.Suite.Revision ||
		len(snapshot.Cases) != len(task.Suite.Cases) || snapshot.Load.Mode != LoadFixedConcurrency || snapshot.Load.Concurrency != 1 ||
		snapshot.Load.RequestCount != uint64(len(task.Suite.Cases)) || snapshot.Load.DurationMS != 0 || snapshot.Load.RatePerSecond != 0 ||
		snapshot.Load.RequestTimeoutMS != task.Suite.QuickTest.TimeoutMS {
		return errors.New("quick run must execute its complete Suite once, sequentially")
	}
	_, _, err := task.Suite.ApplyInputs(snapshot.CaseDefinitions, task.Inputs)
	return err
}

func (task *QuickTaskSnapshot) clone() *QuickTaskSnapshot {
	if task == nil {
		return nil
	}
	result := *task
	result.Suite.Cases = append([]CaseRevisionRef(nil), task.Suite.Cases...)
	result.Suite.QuickTest = task.Suite.QuickTest.Clone()
	if task.Inputs != nil {
		result.Inputs = make(map[string]json.RawMessage, len(task.Inputs))
		for key, value := range task.Inputs {
			result.Inputs[key] = append(json.RawMessage(nil), value...)
		}
	}
	return &result
}

// ApplyInputs builds runtime copies while keeping the authored Case revisions
// intact. Resolved values include defaults, making a later replay independent
// of changes to the task's defaults.
func (suite Suite) ApplyInputs(cases []TestCase, values map[string]json.RawMessage) ([]TestCase, map[string]json.RawMessage, error) {
	if suite.QuickTest == nil {
		return nil, nil, errors.New("suite does not declare a quick task")
	}
	if err := suite.ValidateCases(cases); err != nil {
		return nil, nil, err
	}
	known := make(map[string]bool, len(suite.QuickTest.Inputs))
	for _, input := range suite.QuickTest.Inputs {
		known[input.Key] = true
	}
	for key := range values {
		if !known[key] {
			return nil, nil, fmt.Errorf("unknown quick task input %q", key)
		}
	}
	effective := cloneRunCases(cases)
	specs := make(map[string]any, len(cases))
	for _, testCase := range cases {
		spec, err := decodeTaskValue(testCase.Definition.Spec)
		if err != nil {
			return nil, nil, err
		}
		specs[testCase.Key] = spec
	}
	resolved := make(map[string]json.RawMessage, len(known))
	for _, input := range suite.QuickTest.Inputs {
		raw, found := values[input.Key]
		if !found {
			raw = input.Default
		}
		value, err := decodeTaskValue(raw)
		if err != nil || !input.Accepts(value) {
			return nil, nil, fmt.Errorf("invalid quick task input %q", input.Key)
		}
		resolved[input.Key], err = json.Marshal(value)
		if err != nil {
			return nil, nil, err
		}
		for _, binding := range input.Bindings {
			if !jsonpointer.Replace(specs[binding.CaseKey], binding.Pointer, value) {
				return nil, nil, fmt.Errorf("quick task input %q has an invalid binding", input.Key)
			}
		}
	}
	for index, testCase := range effective {
		encoded, err := json.Marshal(specs[testCase.Key])
		if err != nil {
			return nil, nil, err
		}
		effective[index].Definition.Spec = encoded
	}
	return effective, resolved, nil
}

func decodeTaskValue(raw json.RawMessage) (any, error) {
	if !json.Valid(raw) {
		return nil, errors.New("invalid task JSON value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	err := decoder.Decode(&value)
	return value, err
}
