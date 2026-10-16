package domain

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/testspec"
)

func fileSuiteInput(caseID string) SuiteInput {
	return SuiteInput{
		Key: "image", Label: "Image",
		Input: testspec.Input{
			Type: "file", Required: true,
			File: &testspec.FileInput{
				Encoding: "image_data_url", MediaTypes: []string{"image/png", "image/jpeg"},
				MaxBytes: testspec.MaxFileInputBytes,
			},
		},
		Bindings: []SuiteInputBinding{{CaseID: caseID, Input: "image"}},
	}
}

func TestSuiteFileInputStrictFlatRoundTripAndClone(t *testing.T) {
	input := fileSuiteInput(testCaseID)
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SuiteInput
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Key != input.Key || decoded.Label != input.Label || decoded.File == nil || decoded.Bindings[0] != input.Bindings[0] {
		t.Fatalf("flat suite declaration lost fields: %+v", decoded)
	}
	clone := CloneSuiteInputs([]SuiteInput{input})
	clone[0].File.MediaTypes[0] = "changed"
	clone[0].File.MaxBytes = 1
	if input.File.MediaTypes[0] != "image/png" || input.File.MaxBytes != testspec.MaxFileInputBytes {
		t.Fatal("suite clone aliases file declaration")
	}
	for _, invalid := range []string{
		strings.Replace(string(raw), `"key":`, `"key":"other","key":`, 1),
		strings.Replace(string(raw), `"type":`, `"unknown":true,"type":`, 1),
		strings.Replace(string(raw), `"file":`, `"file":null,"file":`, 1),
		strings.Replace(string(raw), `"bindings":`, `"bindings":null,"bindings":`, 1),
		strings.Replace(string(raw), `"media_types":`, `"media_types":[],"media_types":`, 1),
	} {
		before := decoded.Key
		if err := json.Unmarshal([]byte(invalid), &decoded); err == nil {
			t.Errorf("invalid suite input accepted: %s", invalid)
		}
		if decoded.Key != before {
			t.Fatal("rejected suite declaration mutated destination")
		}
	}
	ordinary := inputSuite().Inputs[0]
	raw, err = json.Marshal(ordinary)
	if err != nil || json.Unmarshal(raw, &decoded) != nil || string(decoded.Default) != "1000" || decoded.Key != "size" {
		t.Fatalf("ordinary flat input did not round trip: %s, %v", raw, err)
	}
}

func fileInputRunSnapshot(t *testing.T) RunSnapshot {
	t.Helper()
	snapshot := validRunSnapshot()
	entry := &snapshot.Entries[0]
	entry.Suite.Inputs = []SuiteInput{fileSuiteInput(testCaseID)}
	entry.CaseDefinitions[0].Definitions[ProtocolOpenAIChat] = json.RawMessage(`{
		"inputs":{"image":{"type":"file","required":true,"file":{"encoding":"image_data_url","media_types":["image/png"],"max_bytes":1048576}}},
		"request":{"body":{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":{"$input":"image"}}}]}]}},
		"assertions":[]}`)
	values := map[string]json.RawMessage{"image": json.RawMessage(`{"asset_ref":"image-1"}`)}
	parameters, cases, err := entry.Suite.ResolveInputs(values)
	if err != nil {
		t.Fatal(err)
	}
	entry.Parameters, entry.CaseInputs = parameters, cases
	snapshot.PlanDocument.Entries[0].Parameters = values
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("logical file snapshot rejected: %v", err)
	}
	return snapshot
}

func TestFileBindingsFreezeOnlyInExistingSnapshotValues(t *testing.T) {
	snapshot := fileInputRunSnapshot(t)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RunSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded.Entries[0].CaseInputs[testCaseID]["image"]) != `{"asset_ref":"image-1"}` {
		t.Fatal("file logical binding was not frozen in CaseInputs")
	}
	cloned := snapshot.clone()
	cloned.Entries[0].Suite.Inputs[0].File.MediaTypes[0] = "changed"
	cloned.Entries[0].CaseInputs[testCaseID]["image"][0] = '['
	if snapshot.Entries[0].Suite.Inputs[0].File.MediaTypes[0] != "image/png" || snapshot.Entries[0].CaseInputs[testCaseID]["image"][0] != '{' {
		t.Fatal("frozen file declaration or binding aliases a clone")
	}
	for _, rejected := range []string{`{"asset_ref":"image-1","path":"secret.png"}`, `{"asset_ref":null}`} {
		decoded.Entries[0].CaseInputs[testCaseID]["image"] = json.RawMessage(rejected)
		if err := decoded.Validate(); err == nil {
			t.Fatal("invalid logical file binding accepted by snapshot validation")
		}
	}
}

func TestSuiteAndSnapshotRejectFileBindingDeclarationMismatchBothDirections(t *testing.T) {
	for _, direction := range []string{"suite-file-case-object", "suite-object-case-file"} {
		t.Run(direction, func(t *testing.T) {
			snapshot := fileInputRunSnapshot(t)
			entry := &snapshot.Entries[0]
			if direction == "suite-file-case-object" {
				spec, err := entry.CaseDefinitions[0].SpecFor(ProtocolOpenAIChat)
				if err != nil {
					t.Fatal(err)
				}
				spec.Inputs["image"] = testspec.Input{Type: "object", Required: true}
				entry.CaseDefinitions[0].Definitions[ProtocolOpenAIChat], _ = json.Marshal(spec)
			} else {
				entry.Suite.Inputs[0].Type = "object"
				entry.Suite.Inputs[0].File = nil
			}
			if err := entry.Suite.ValidateCases(entry.CaseDefinitions); err == nil {
				t.Fatal("mismatched Suite/Case file declarations accepted")
			}
			if err := snapshot.Validate(); err == nil {
				t.Fatal("mismatched frozen Suite/Case file declarations accepted")
			}
			// Both declarations ordinary must keep the exact same asset_ref
			// value as plain JSON, regardless of the object's shape.
			entry.Suite.Inputs[0].Type, entry.Suite.Inputs[0].File = "object", nil
			spec, err := entry.CaseDefinitions[0].SpecFor(ProtocolOpenAIChat)
			if err != nil {
				t.Fatal(err)
			}
			spec.Inputs["image"] = testspec.Input{Type: "object", Required: true}
			entry.CaseDefinitions[0].Definitions[ProtocolOpenAIChat], _ = json.Marshal(spec)
			if err := snapshot.Validate(); err != nil {
				t.Fatalf("ordinary object declaration rejected: %v", err)
			}
		})
	}
}
