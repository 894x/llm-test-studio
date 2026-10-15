package jobcontract_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestMaintenanceCommandCurrentFieldsAndByteLimits(t *testing.T) {
	valid := []byte(`{"contract":1,"operation_id":"55555555-5555-4555-8555-555555555555","action":"pause"}`)
	for _, action := range []string{"pause", "resume"} {
		document := bytes.Replace(
			valid,
			[]byte(`"pause"`),
			[]byte(`"`+action+`"`),
			1,
		)
		document = append(document, bytes.Repeat([]byte(" "), jobcontract.MaxCommandBytes-len(document))...)
		command, err := jobcontract.DecodeMaintenanceCommand(bytes.NewReader(document))
		if err != nil || string(command.Action) != action {
			t.Fatalf("current command at exact byte limit: %v", err)
		}
		if _, err := jobcontract.DecodeMaintenanceCommand(bytes.NewReader(append(document, ' '))); !errors.Is(err, jobcontract.ErrTooLarge) {
			t.Fatal("oversized maintenance command accepted")
		}
	}
	for _, document := range []string{
		`{}`, `null`, `[]`, string(valid) + ` {}`,
		strings.Replace(
			string(valid),
			`"contract":1`,
			`"contract":1,"contract":1`,
			1,
		),
		strings.Replace(
			string(valid),
			`"contract":1`,
			`"Contract":1`,
			1,
		),
		strings.Replace(
			string(valid),
			`"contract":1`,
			`"contract":1,"extra":1`,
			1,
		),
		strings.Replace(
			string(valid),
			`"pause"`,
			`"unknown"`,
			1,
		),
		strings.Replace(
			string(valid),
			`"operation_id"`,
			`"OperationID"`,
			1,
		),
		strings.Replace(
			string(valid),
			`55555555-5555-4555-8555-555555555555`,
			`invalid`,
			1,
		),
	} {
		if _, err := jobcontract.DecodeMaintenanceCommand(strings.NewReader(document)); err == nil {
			t.Fatal("invalid command shape was accepted")
		}
	}
	unsupported := bytes.Replace(
		valid,
		[]byte(`"contract":1`),
		[]byte(`"contract":2`),
		1,
	)
	if _, err := jobcontract.DecodeMaintenanceCommand(bytes.NewReader(unsupported)); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported command contract was not rejected explicitly")
	}
}

func TestMaintenanceRecordRequiresExplicitPausedStateAndCurrentIdentity(t *testing.T) {
	const installation = "22222222-2222-4222-8222-222222222222"
	for _, paused := range []bool{false, true} {
		record := jobcontract.MaintenanceRecord{
			Contract: jobcontract.CurrentContract, NodeInstallationID: installation,
			OperationID: "55555555-5555-4555-8555-555555555555", Paused: paused, ChangedAt: time.Now().UTC(),
		}
		document, _ := json.Marshal(record)
		var decoded jobcontract.MaintenanceRecord
		if err := json.Unmarshal(document, &decoded); err != nil || decoded.Validate(installation) != nil {
			t.Fatalf("valid current maintenance record rejected: %v", err)
		}
		for _, changed := range [][]byte{
			bytes.Replace(
				document,
				[]byte(`"paused":false,`),
				nil,
				1,
			),
			bytes.Replace(
				document,
				[]byte(`"paused":true,`),
				nil,
				1,
			),
			bytes.Replace(
				document,
				[]byte(`"paused":false`),
				[]byte(`"paused":null`),
				1,
			),
			bytes.Replace(
				document,
				[]byte(`"paused":true`),
				[]byte(`"paused":null`),
				1,
			),
		} {
			if bytes.Equal(changed, document) {
				continue
			}
			if err := json.Unmarshal(changed, &decoded); err == nil {
				t.Fatal("missing/null paused field was inferred as a supported state")
			}
		}
		if record.Validate("33333333-3333-4333-8333-333333333333") == nil {
			t.Fatal("maintenance record accepted for a different installation")
		}
	}
}
