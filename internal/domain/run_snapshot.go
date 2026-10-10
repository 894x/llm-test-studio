package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/894x/llm-test-studio/internal/testspec"
)

// RunSnapshot freezes the resolved current definitions for every execution mode.
type RunSnapshot struct {
	SchemaVersion int                 `json:"schema_version"`
	Plan          EntityRevisionRef   `json:"plan"`
	Model         ModelSnapshot       `json:"model"`
	Channel       ChannelSnapshot     `json:"channel"`
	Environment   EnvironmentSnapshot `json:"environment"`
	PlanDocument  *Plan               `json:"plan_document"`
	Mapping       *ChannelModel       `json:"mapping"`
	Entries       []RunEntrySnapshot  `json:"entries"`
	QuickTask     *QuickTaskSnapshot  `json:"quick_task,omitempty"`
}

type RunEntrySnapshot struct {
	WarmupCount     uint32                                `json:"warmup_count"`
	Settings        testspec.RunSettings                  `json:"settings"`
	EntryID         string                                `json:"entry_id"`
	TargetKind      PlanTargetKind                        `json:"target_kind"`
	TargetID        string                                `json:"target_id"`
	Name            string                                `json:"name"`
	Key             string                                `json:"key"`
	Suite           *Suite                                `json:"suite,omitempty"`
	Cases           []CaseRevisionRef                     `json:"cases"`
	CaseDefinitions []TestCase                            `json:"case_definitions"`
	Parameters      map[string]json.RawMessage            `json:"parameters"`
	CaseInputs      map[string]map[string]json.RawMessage `json:"case_inputs"`
	Load            LoadProfile                           `json:"load"`
	SLA             SLAProfile                            `json:"sla"`
}

func (snapshot *RunSnapshot) UnmarshalJSON(raw []byte) error {
	type document RunSnapshot
	var candidate document
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&candidate); err != nil {
		return fmt.Errorf("unsupported run snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("run snapshot must contain one JSON value")
	}
	value := RunSnapshot(candidate)
	if err := value.Validate(); err != nil {
		return err
	}
	*snapshot = value
	return nil
}

func (snapshot RunSnapshot) Validate() error {
	if snapshot.SchemaVersion != CurrentRunSnapshotSchemaVersion {
		return fmt.Errorf("unsupported run snapshot schema version %d; start a new run using current definitions", snapshot.SchemaVersion)
	}
	if err := snapshot.Plan.Validate("plan"); err != nil {
		return err
	}
	if err := snapshot.Model.Validate(); err != nil {
		return err
	}
	if err := snapshot.Channel.Validate(); err != nil {
		return err
	}
	if err := snapshot.Environment.Validate(); err != nil {
		return err
	}
	if snapshot.PlanDocument == nil || snapshot.Mapping == nil || len(snapshot.Entries) == 0 {
		return errors.New("run requires a plan document, binding and resolved entries")
	}
	plan := snapshot.PlanDocument
	if err := plan.Validate(); err != nil {
		return fmt.Errorf("invalid run plan: %w", err)
	}
	if plan.ID != snapshot.Plan.ID || plan.Revision != snapshot.Plan.Revision {
		return errors.New("run plan identity mismatch")
	}
	if plan.Protocol != snapshot.Model.Protocol || plan.Protocol != snapshot.Channel.Protocol {
		return errors.New("run plan and binding must use one protocol")
	}
	if len(plan.Entries) != len(snapshot.Entries) {
		return errors.New("run entries do not match plan")
	}
	for index, entry := range snapshot.Entries {
		if err := entry.Validate(plan.Protocol); err != nil {
			return fmt.Errorf("invalid run entry %d: %w", index, err)
		}
		source := plan.Entries[index]
		identityMatches := source.EntryID == entry.EntryID && source.TargetKind == entry.TargetKind && source.TargetID == entry.TargetID
		profilesMatch := reflect.DeepEqual(source.Load, entry.Load) && reflect.DeepEqual(source.SLA, entry.SLA) && source.WarmupCount == entry.WarmupCount && source.Settings == entry.Settings
		if !identityMatches || !profilesMatch {
			return errors.New("run entry differs from frozen plan item")
		}
		for key, value := range source.Parameters {
			if !equalSnapshotJSON(value, entry.Parameters[key]) {
				return fmt.Errorf("run entry changed supplied parameter %q", key)
			}
		}
	}
	if err := validateRunMapping(snapshot); err != nil {
		return err
	}
	if snapshot.QuickTask != nil {
		return snapshot.QuickTask.validate(snapshot)
	}
	return nil
}

func (entry RunEntrySnapshot) Validate(protocol Protocol) error {
	if !IsUUID(entry.EntryID) || !IsUUID(entry.TargetID) || entry.Name == "" {
		return errors.New("run entry requires identifiers and name")
	}
	switch entry.TargetKind {
	case "case":
		if entry.Suite != nil || len(entry.Cases) != 1 || entry.Cases[0].CaseID != entry.TargetID {
			return errors.New("direct Case entry must contain its referenced Case")
		}
	case "suite":
		if entry.Suite == nil || entry.Suite.ID != entry.TargetID || entry.Suite.Protocol != protocol {
			return errors.New("Suite entry requires its same-protocol document")
		}
		if err := entry.Suite.Validate(); err != nil {
			return err
		}
		if len(entry.Suite.Cases) != len(entry.Cases) {
			return errors.New("run must include complete Suite")
		}
		for index, ref := range entry.Suite.Cases {
			if ref.CaseID != entry.Cases[index].CaseID {
				return errors.New("run Suite member order mismatch")
			}
		}
	default:
		return errors.New("unsupported run entry target kind")
	}
	if err := validateCaseRevisionRefs(entry.Cases); err != nil {
		return err
	}
	if len(entry.CaseDefinitions) != len(entry.Cases) || len(entry.CaseInputs) != len(entry.Cases) || entry.Parameters == nil {
		return errors.New("run entry requires complete definitions and resolved inputs")
	}
	for index, testCase := range entry.CaseDefinitions {
		if err := testCase.Validate(); err != nil {
			return err
		}
		ref := entry.Cases[index]
		if testCase.ID != ref.CaseID || testCase.Revision != ref.Revision || !testCase.SupportsProtocol(protocol) {
			return errors.New("run Case identity or protocol mismatch")
		}
		spec, err := testCase.SpecFor(protocol)
		if err != nil {
			return err
		}
		inputs, found := entry.CaseInputs[testCase.ID]
		if !found || inputs == nil {
			return errors.New("missing resolved Case inputs")
		}
		resolved, err := testspec.ValidateInputs(spec.Inputs, inputs)
		if err != nil || !equalSnapshotMap(resolved, inputs) {
			return errors.New("run Case inputs are invalid or unresolved")
		}
	}
	if err := entry.Load.Validate(); err != nil {
		return err
	}
	return entry.SLA.Validate()
}

func validateRunMapping(snapshot RunSnapshot) error {
	if snapshot.Mapping == nil {
		return errors.New("run requires binding document")
	}
	if err := snapshot.Mapping.Validate(); err != nil {
		return err
	}
	if !snapshot.Mapping.SupportsProtocol(snapshot.Channel.Protocol) {
		return errors.New("run mapping protocol does not match target")
	}
	if snapshot.Mapping.ChannelID != snapshot.Channel.ID || snapshot.Mapping.ModelID != snapshot.Model.ID || snapshot.Mapping.UpstreamModelName != snapshot.Channel.UpstreamModelName {
		return errors.New("run binding does not match target")
	}
	return nil
}

func (snapshot RunSnapshot) clone() RunSnapshot {
	snapshot.Model = snapshot.Model.clone()
	if snapshot.PlanDocument != nil {
		plan := cloneRunPlan(*snapshot.PlanDocument)
		snapshot.PlanDocument = &plan
	}
	if snapshot.Mapping != nil {
		mapping := *snapshot.Mapping
		mapping.Protocols = append([]Protocol{}, mapping.Protocols...)
		snapshot.Mapping = &mapping
	}
	snapshot.QuickTask = snapshot.QuickTask.clone()
	entries := make([]RunEntrySnapshot, len(snapshot.Entries))
	for index, entry := range snapshot.Entries {
		entries[index] = entry.clone()
	}
	snapshot.Entries = entries
	return snapshot
}

func (entry RunEntrySnapshot) clone() RunEntrySnapshot {
	if entry.Suite != nil {
		suite := *entry.Suite
		suite.Cases = append([]CaseRef{}, suite.Cases...)
		suite.Inputs = CloneSuiteInputs(suite.Inputs)
		entry.Suite = &suite
	}
	entry.Cases = append([]CaseRevisionRef{}, entry.Cases...)
	entry.CaseDefinitions = cloneRunCases(entry.CaseDefinitions)
	entry.Parameters = cloneRawMessageMap(entry.Parameters)
	inputs := make(map[string]map[string]json.RawMessage, len(entry.CaseInputs))
	for key, values := range entry.CaseInputs {
		inputs[key] = cloneRawMessageMap(values)
	}
	entry.CaseInputs = inputs
	entry.SLA = entry.SLA.clone()
	return entry
}

func cloneRunPlan(plan Plan) Plan {
	entries := make([]PlanEntry, len(plan.Entries))
	for index, entry := range plan.Entries {
		entry.Parameters = cloneRawMessageMap(entry.Parameters)
		entry.SLA = entry.SLA.clone()
		entries[index] = entry
	}
	plan.Entries = entries
	return plan
}

func cloneRunCases(values []TestCase) []TestCase {
	result := make([]TestCase, len(values))
	for index, value := range values {
		value.Definitions = value.Definitions.Clone()
		result[index] = value
	}
	return result
}

func equalSnapshotJSON(left, right json.RawMessage) bool {
	var a, b any
	return json.Unmarshal(left, &a) == nil && json.Unmarshal(right, &b) == nil && reflect.DeepEqual(a, b)
}

func equalSnapshotMap(left, right map[string]json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if !equalSnapshotJSON(value, right[key]) {
			return false
		}
	}
	return true
}
