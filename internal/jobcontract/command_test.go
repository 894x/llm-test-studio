package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func validCommand() Command {
	return Command{
		Contract: CurrentContract, CommandID: "77777777-7777-4777-8777-777777777771",
		JobID: "77777777-7777-4777-8777-777777777770", Kind: CommandCancel, ExpectedRevision: 1,
	}
}

func TestCommandValidationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Command)
		valid  bool
	}{
		{name: "cancel", valid: true},
		{name: "stop", change: func(value *Command) { value.Kind = CommandStopSending }, valid: true},
		{name: "missing contract", change: func(value *Command) { value.Contract = 0 }},
		{name: "unknown contract", change: func(value *Command) { value.Contract++ }},
		{name: "missing command id", change: func(value *Command) { value.CommandID = "" }},
		{name: "noncanonical command id", change: func(value *Command) {
			value.CommandID = strings.ToUpper(value.CommandID)
		}},
		{name: "missing job id", change: func(value *Command) { value.JobID = "" }},
		{name: "unknown kind", change: func(value *Command) { value.Kind = "resume" }},
		{name: "zero revision", change: func(value *Command) { value.ExpectedRevision = 0 }},
		{name: "maximum writable revision", change: func(value *Command) {
			value.ExpectedRevision = math.MaxInt64 - 1
		}, valid: true},
		{name: "overflowing next revision", change: func(value *Command) { value.ExpectedRevision = math.MaxInt64 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := validCommand()
			// Use a letter to exercise UUID case rather than only numeric digits.
			value.CommandID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			if test.change != nil {
				test.change(&value)
			}
			if err := value.Validate(); (err == nil) != test.valid {
				t.Fatalf("command boundary valid=%v err=%v", test.valid, err)
			}
		})
	}
}

func TestCommandDecoderStrictFieldsAndByteLimits(t *testing.T) {
	raw, err := json.Marshal(validCommand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCommand(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`null`, `[]`, `{}`, string(raw) + `{}`,
		strings.ReplaceAll(string(raw), `"contract":1`, `"contract":"1"`),
		strings.ReplaceAll(string(raw), `"command_id"`, `"Command_id"`),
		strings.ReplaceAll(string(raw), `"contract":1`, `"contract":1,"contract":1`),
		strings.ReplaceAll(string(raw), `"kind":"cancel"`, `"kind":"cancel","extra":1`),
		strings.ReplaceAll(string(raw), `"expected_revision":1`, `"expected_revision":-1`),
	} {
		if _, err := DecodeCommand(strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid command JSON was accepted")
		}
	}
	padded := append(raw, bytes.Repeat([]byte(" "), MaxCommandBytes-len(raw))...)
	if _, err := DecodeCommand(bytes.NewReader(padded)); err != nil {
		t.Fatalf("exact command byte limit rejected: %v", err)
	}
	if _, err := DecodeCommand(bytes.NewReader(append(padded, ' '))); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized command result=%v", err)
	}
}

func TestCommandConfirmationStateDecisionTable(t *testing.T) {
	for _, test := range []struct {
		name     string
		kind     CommandKind
		expected uint64
		revision uint64
		status   domain.RunStatus
		valid    bool
	}{
		{name: "cancel queued", kind: CommandCancel, expected: 1, revision: 2, status: domain.RunCancelled, valid: true},
		{name: "stop queued", kind: CommandStopSending, expected: 1, revision: 2, status: domain.RunCancelled, valid: true},
		{name: "stop starting", kind: CommandStopSending, expected: 2, revision: 3, status: domain.RunDraining, valid: true},
		{name: "stop running", kind: CommandStopSending, expected: 3, revision: 4, status: domain.RunDraining, valid: true},
		{name: "stop already draining", kind: CommandStopSending, expected: 3, revision: 3,
			status: domain.RunDraining, valid: true},
		{name: "queued cannot drain", kind: CommandStopSending, expected: 1, revision: 2, status: domain.RunDraining},
		{name: "starting cannot already drain", kind: CommandStopSending, expected: 2, revision: 2,
			status: domain.RunDraining},
		{name: "stop cannot cancel starting", kind: CommandStopSending, expected: 2, revision: 3,
			status: domain.RunCancelled},
		{name: "cancel cannot drain", kind: CommandCancel, expected: 2, revision: 3, status: domain.RunDraining},
		{name: "cancel must advance", kind: CommandCancel, expected: 2, revision: 2, status: domain.RunCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			at := time.Date(
				2026,
				time.October,
				15,
				12,
				0,
				0,
				0,
				time.UTC,
			)
			spec := performanceSpec(t)
			snapshot, err := spec.InitialSnapshot(at, originFixture(spec))
			if err != nil {
				t.Fatal(err)
			}
			snapshot.Receipt.Revision = test.revision
			snapshot.Receipt.Status = test.status
			snapshot.LatestSequence = test.revision + 1
			command := validCommand()
			command.JobID, command.Kind, command.ExpectedRevision = snapshot.Receipt.JobID, test.kind, test.expected
			confirmation := CommandConfirmation{Command: command, AcceptedAt: at, Snapshot: snapshot}
			if err := confirmation.Validate(); (err == nil) != test.valid {
				t.Fatalf("confirmation boundary valid=%v err=%v", test.valid, err)
			}
		})
	}
}
