package domain

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestQuickTaskInputsChangeExecutionWithoutChangingAuthoredDefinitions(t *testing.T) {
	snapshot := validRunSnapshot()
	testCase := snapshot.Suites[0].CaseDefinitions[0]
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
	prepared := snapshot.Suites[0]
	snapshot.SchemaVersion = FlatRunSnapshotSchemaVersion
	snapshot.Plan.ID = testRunID
	snapshot.PlanDocument = nil
	snapshot.Cases = append([]CaseRevisionRef(nil), prepared.Cases...)
	snapshot.CaseDefinitions = cloneRunCases(prepared.CaseDefinitions)
	snapshot.Load = prepared.Load
	snapshot.SLA = prepared.SLA
	snapshot.Load.Concurrency, snapshot.Load.RequestCount = 1, uint64(len(snapshot.Cases))
	suite := Suite{EntityMeta: validEntityMeta(testPlanID), Key: "connection", Name: "Connection", Protocol: snapshot.Model.Protocol,
		Cases: append([]CaseRevisionRef(nil), snapshot.Cases...), QuickTest: &SuiteQuickTest{Description: "Connect", TimeoutMS: snapshot.Load.RequestTimeoutMS, Inputs: []SuiteInput{}}}
	snapshot.QuickTask = &QuickTaskSnapshot{Suite: suite, Inputs: map[string]json.RawMessage{}}
	snapshot.Suites = nil
	if err := snapshot.Validate(); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestQuickRunSnapshotRejectsTamperedProvenance(t *testing.T) {
	for name, mutate := range map[string]func(*RunSnapshot){
		"missing task":            func(s *RunSnapshot) { s.QuickTask.Suite.QuickTest = nil },
		"missing inputs":          func(s *RunSnapshot) { s.QuickTask.Inputs = nil },
		"unknown input":           func(s *RunSnapshot) { s.QuickTask.Inputs["unknown"] = json.RawMessage(`true`) },
		"different channel":       func(s *RunSnapshot) { s.QuickTask.SavedChannelID = testModelID },
		"different case revision": func(s *RunSnapshot) { s.QuickTask.Suite.Cases[0].Revision++ },
		"different model":         func(s *RunSnapshot) { s.QuickTask.Suite.ModelTarget = "another-model" },
		"different timeout":       func(s *RunSnapshot) { s.QuickTask.Suite.QuickTest.TimeoutMS++ },
		"repeated execution":      func(s *RunSnapshot) { s.Load.RequestCount++ },
		"unsupported schema":      func(s *RunSnapshot) { s.SchemaVersion = 1 },
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

func TestFlatRunSnapshotRequiresQuickTaskProvenance(t *testing.T) {
	snapshot := quickRunSnapshot(t)
	snapshot.QuickTask = nil
	if err := snapshot.Validate(); err == nil {
		t.Fatal("ordinary flat v2 Run snapshot validated")
	}
}

func TestRunSnapshotJSONSeparatesAuthoredSuitesFromQuickTaskFields(t *testing.T) {
	authoredJSON, err := json.Marshal(validRunSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	var authored map[string]json.RawMessage
	if err := json.Unmarshal(authoredJSON, &authored); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"cases", "load", "sla", "case_definitions", "quick_task"} {
		if _, exists := authored[legacy]; exists {
			t.Fatalf("authored v3 snapshot contains flat field %q: %s", legacy, authoredJSON)
		}
	}
	if _, exists := authored["suites"]; !exists {
		t.Fatalf("authored v3 snapshot omitted suites: %s", authoredJSON)
	}
	var authoredPlan map[string]json.RawMessage
	if err := json.Unmarshal(authored["plan_document"], &authoredPlan); err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []string{"suite_id", "suite_revision", "cases", "load", "sla"} {
		if _, exists := authoredPlan[legacy]; exists {
			t.Fatalf("authored v3 plan document contains flat field %q: %s", legacy, authored["plan_document"])
		}
	}
	if _, exists := authoredPlan["suites"]; !exists {
		t.Fatalf("authored v3 plan document omitted suites: %s", authored["plan_document"])
	}

	quickJSON, err := json.Marshal(quickRunSnapshot(t))
	if err != nil {
		t.Fatal(err)
	}
	var quick map[string]json.RawMessage
	if err := json.Unmarshal(quickJSON, &quick); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"cases", "load", "sla", "case_definitions", "quick_task"} {
		if _, exists := quick[field]; !exists {
			t.Fatalf("quick-task v2 snapshot omitted flat field %q: %s", field, quickJSON)
		}
	}
	if _, exists := quick["suites"]; exists {
		t.Fatalf("quick-task v2 snapshot contains suites: %s", quickJSON)
	}
	if _, exists := quick["plan_document"]; exists {
		t.Fatalf("quick-task v2 snapshot contains authored Plan document: %s", quickJSON)
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
