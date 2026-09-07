package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestQuickTaskInputsChangeExecutionWithoutChangingAuthoredDefinitions(t *testing.T) {
	snapshot := validRunSnapshot()
	testCase := snapshot.CaseDefinitions[0]
	testCase.Definition.Spec = json.RawMessage(`{"request":{"body":{"messages":[{"role":"user","content":"original"}],"large":9007199254740993}}}`)
	testCase.Enabled = true
	testCase.ExecutionMode = CaseExecutionAutomatic
	suite := Suite{EntityMeta: validEntityMeta(testPlanID), Key: "connection", Name: "Connection", Protocol: testCase.Protocol, ModelTarget: snapshot.Channel.UpstreamModelName,
		Cases: []CaseRevisionRef{{CaseID: testCase.ID, Revision: testCase.Revision}},
		QuickTest: &SuiteQuickTest{Description: "Connection", TimeoutMS: 30000, Inputs: []SuiteInput{{
			Key: "prompt", Label: "Message", Type: "text", Default: json.RawMessage(`"default"`),
			Bindings: []SuiteInputBinding{{CaseKey: testCase.Key, Pointer: "/request/body/messages/0/content"}},
		}}},
	}
	for _, values := range []map[string]json.RawMessage{nil, {"prompt": json.RawMessage(`"edited"`)}} {
		effective, resolved, err := suite.ApplyInputs([]TestCase{testCase}, values)
		if err != nil {
			t.Fatal(err)
		}
		want := `"default"`
		if values != nil {
			want = `"edited"`
		}
		if string(resolved["prompt"]) != want || !bytes.Contains(effective[0].Definition.Spec, []byte(want)) || !bytes.Contains(effective[0].Definition.Spec, []byte("9007199254740993")) {
			t.Fatalf("effective inputs lost: %s, %s", effective[0].Definition.Spec, resolved["prompt"])
		}
		effective[0].Definition.Spec[0] = '['
		if !bytes.Contains(testCase.Definition.Spec, []byte(`"original"`)) {
			t.Fatal("authored request changed")
		}
	}
	for _, invalid := range []map[string]json.RawMessage{{"unknown": json.RawMessage(`"x"`)}, {"prompt": json.RawMessage(`7`)}, {"prompt": json.RawMessage(`null`)}} {
		if _, _, err := suite.ApplyInputs([]TestCase{testCase}, invalid); err == nil {
			t.Fatalf("invalid input accepted: %v", invalid)
		}
	}
}

func quickRunSnapshot(t *testing.T) RunSnapshot {
	t.Helper()
	snapshot := validRunSnapshot()
	snapshot.Plan.ID = testRunID
	snapshot.PlanDocument.ID = testRunID
	snapshot.PlanDocument.ModelIDs, snapshot.PlanDocument.ChannelIDs = nil, nil
	snapshot.Load.Concurrency, snapshot.Load.RequestCount = 1, uint64(len(snapshot.Cases))
	snapshot.PlanDocument.Load = snapshot.Load
	suite := Suite{EntityMeta: validEntityMeta(testPlanID), Key: "connection", Name: "Connection", Protocol: snapshot.Model.Protocol,
		Cases: append([]CaseRevisionRef(nil), snapshot.Cases...), QuickTest: &SuiteQuickTest{Description: "Connect", TimeoutMS: snapshot.Load.RequestTimeoutMS, Inputs: []SuiteInput{}}}
	snapshot.PlanDocument.SuiteID, snapshot.PlanDocument.SuiteRevision = suite.ID, suite.Revision
	snapshot.QuickTask = &QuickTaskSnapshot{Suite: suite, Inputs: map[string]json.RawMessage{}}
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestQuickRunSnapshotRejectsTamperedProvenance(t *testing.T) {
	for name, mutate := range map[string]func(*RunSnapshot){
		"missing task":             func(s *RunSnapshot) { s.QuickTask.Suite.QuickTest = nil },
		"missing inputs":           func(s *RunSnapshot) { s.QuickTask.Inputs = nil },
		"unknown input":            func(s *RunSnapshot) { s.QuickTask.Inputs["unknown"] = json.RawMessage(`true`) },
		"different channel":        func(s *RunSnapshot) { s.QuickTask.SavedChannelID = testModelID },
		"different suite revision": func(s *RunSnapshot) { s.QuickTask.Suite.Revision++ },
		"different case revision":  func(s *RunSnapshot) { s.QuickTask.Suite.Cases[0].Revision++ },
		"different model":          func(s *RunSnapshot) { s.QuickTask.Suite.ModelTarget = "another-model" },
		"different timeout":        func(s *RunSnapshot) { s.QuickTask.Suite.QuickTest.TimeoutMS++ },
		"repeated execution":       func(s *RunSnapshot) { s.Load.RequestCount++; s.PlanDocument.Load = s.Load },
		"legacy schema": func(s *RunSnapshot) {
			s.SchemaVersion = legacyRunSnapshotSchemaVersion
			s.PlanDocument = nil
			s.Mapping = nil
			s.CaseDefinitions = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := quickRunSnapshot(t)
			mutate(&snapshot)
			if err := snapshot.Validate(); err == nil {
				t.Fatal("tampered quick task accepted")
			}
		})
	}
	snapshot := quickRunSnapshot(t)
	if _, err := NewRun(validEntityMeta(testEvidenceID), snapshot.Plan.ID, snapshot); err == nil {
		t.Fatal("quick run accepted an unrelated transient Plan identity")
	}
}

func TestQuickRunOwnsItsSourceAndResolvedInputs(t *testing.T) {
	snapshot := quickRunSnapshot(t)
	original := snapshot.QuickTask.clone()
	run, err := NewRun(validEntityMeta(testRunID), snapshot.Plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.QuickTask.Suite.Cases[0].Revision++
	snapshot.QuickTask.Suite.QuickTest.Description = "changed"
	snapshot.QuickTask.Inputs["extra"] = json.RawMessage(`false`)
	copy := run.Snapshot()
	copy.QuickTask.Suite.Cases[0].Revision++
	copy.QuickTask.Suite.QuickTest.TimeoutMS++
	copy.QuickTask.Inputs["extra"] = json.RawMessage(`true`)
	if !reflect.DeepEqual(run.Snapshot().QuickTask, original) {
		t.Fatal("caller mutated the recorded task")
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var restored Run
	if err := json.Unmarshal(encoded, &restored); err != nil || !reflect.DeepEqual(restored.Snapshot().QuickTask, original) {
		t.Fatalf("fixed task changed during persistence: %v", err)
	}
}

func TestQuickRunDoesNotShareInputBytesWithCallers(t *testing.T) {
	snapshot := quickRunSnapshot(t)
	snapshot.CaseDefinitions[0].Definition.Spec = json.RawMessage(`{"request":{"body":{"prompt":"original"}}}`)
	snapshot.QuickTask.Suite.QuickTest.Inputs = []SuiteInput{{Key: "prompt", Label: "Prompt", Type: "text", Default: json.RawMessage(`"default"`),
		Bindings: []SuiteInputBinding{{CaseKey: snapshot.CaseDefinitions[0].Key, Pointer: "/request/body/prompt"}}}}
	snapshot.QuickTask.Inputs["prompt"] = json.RawMessage(`"edited"`)
	run, err := NewRun(validEntityMeta(testRunID), snapshot.Plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.QuickTask.Inputs["prompt"][1] = 'X'
	snapshot.QuickTask.Suite.QuickTest.Inputs[0].Default[1] = 'X'
	copy := run.Snapshot()
	copy.QuickTask.Inputs["prompt"][1] = 'Y'
	copy.QuickTask.Suite.QuickTest.Inputs[0].Bindings[0].Pointer = "/request/body/missing"
	actual := run.Snapshot().QuickTask
	if string(actual.Inputs["prompt"]) != `"edited"` || string(actual.Suite.QuickTest.Inputs[0].Default) != `"default"` || actual.Suite.QuickTest.Inputs[0].Bindings[0].Pointer != "/request/body/prompt" {
		t.Fatal("Run shares mutable task values with its caller")
	}
}
