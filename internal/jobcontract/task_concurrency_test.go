package jobcontract_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestTaskConcurrencyCommandBoundariesAndClosedShape(t *testing.T) {
	command := jobcontract.TaskConcurrencyCommand{
		Contract: jobcontract.CurrentContract, NodeID: "11111111-1111-4111-8111-111111111111",
		NodeInstallationID: "22222222-2222-4222-8222-222222222222", ExpectedMaxActiveJobs: 1,
	}
	for _, limit := range []uint32{0, 1, 4, 128, 129} {
		command.MaxActiveJobs = limit
		document, _ := json.Marshal(command)
		decoded, err := jobcontract.DecodeTaskConcurrencyCommand(bytes.NewReader(document))
		valid := limit >= 1 && limit <= 128
		if valid && (err != nil || decoded != command) {
			t.Fatalf("valid limit %d rejected: %v", limit, err)
		}
		if !valid && err == nil {
			t.Fatalf("invalid limit %d accepted", limit)
		}
	}
	command.MaxActiveJobs = 4
	for _, expected := range []uint32{0, 1, 4, 128, 129} {
		command.ExpectedMaxActiveJobs = expected
		document, _ := json.Marshal(command)
		_, err := jobcontract.DecodeTaskConcurrencyCommand(bytes.NewReader(document))
		valid := expected >= 1 && expected <= 128
		if valid != (err == nil) {
			t.Fatalf("expected limit %d validity differs", expected)
		}
	}
	command.ExpectedMaxActiveJobs = 1
	raw, _ := json.Marshal(command)
	document := string(raw)
	atLimit := strings.Repeat(" ", jobcontract.MaxCommandBytes-len(document)) + document
	if _, err := jobcontract.DecodeTaskConcurrencyCommand(strings.NewReader(atLimit)); err != nil {
		t.Fatal("exact maximum command size rejected")
	}
	invalid := []string{
		"null", "[]", document + "{}", document + " true",
		strings.Replace(document, `"max_active_jobs":4`, `"max_active_jobs":null`, 1),
		strings.Replace(document, `"max_active_jobs":4`, `"max_active_jobs":"4"`, 1),
		strings.Replace(document, `"max_active_jobs":4`, `"max_active_jobs":1.5`, 1),
		strings.Replace(document, `"max_active_jobs":4`, `"max_active_jobs":-1`, 1),
		strings.Replace(document, `"max_active_jobs":4`, `"max_active_jobs":4,"max_active_jobs":8`, 1),
		strings.Replace(document, `"max_active_jobs":4`, `"unexpected":4`, 1),
		strings.Replace(document, `"expected_max_active_jobs":1`, `"expected_max_active_jobs":0`, 1),
		strings.Replace(document, command.NodeID, "not-a-node", 1),
		strings.Replace(document, `"contract":1`, `"contract":2`, 1),
		strings.Repeat(" ", jobcontract.MaxCommandBytes+1),
	}
	for i, value := range invalid {
		if _, err := jobcontract.DecodeTaskConcurrencyCommand(strings.NewReader(value)); err == nil {
			t.Fatalf("invalid shape %d accepted", i)
		}
	}
	if _, err := jobcontract.DecodeTaskConcurrencyCommand(nil); err == nil {
		t.Fatal("nil body accepted")
	}
}
