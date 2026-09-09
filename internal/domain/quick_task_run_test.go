package domain

import (
	"encoding/json"
	"testing"
)

func quickRunSnapshot(t *testing.T) RunSnapshot {
	t.Helper()
	s := validRunSnapshot()
	s.Plan.ID = testRunID
	s.PlanDocument.ID = testRunID
	s.QuickTask = &QuickTaskSnapshot{}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestQuickTaskUsesSameSnapshotAndValidatesProvenance(t *testing.T) {
	s := quickRunSnapshot(t)
	run, err := NewRun(validEntityMeta(testRunID), testRunID, s)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var restored Run
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	s.QuickTask.SavedChannelID = testModelID
	if err := s.Validate(); err == nil {
		t.Fatal("wrong channel accepted")
	}
	if run.Snapshot().QuickTask.SavedChannelID != "" {
		t.Fatal("mutable provenance leaked")
	}
}
func TestRunSnapshotRejectsOldFormatWithoutMutation(t *testing.T) {
	s := validRunSnapshot()
	before, _ := json.Marshal(s)
	if err := json.Unmarshal([]byte(`{"schema_version":3,"suites":[]}`), &s); err == nil {
		t.Fatal("old shape accepted")
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("rejected decode changed snapshot")
	}
}
