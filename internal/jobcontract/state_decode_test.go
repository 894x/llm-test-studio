package jobcontract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestStateDecodersRequireCurrentExactFields(t *testing.T) {
	snapshot, err := performanceSpec(t).InitialSnapshot(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	command := Command{Contract: CurrentContract, CommandID: testMeta(t).ID,
		JobID: snapshot.Receipt.JobID, Kind: CommandCancel, ExpectedRevision: 1}
	cancelled := snapshot
	cancelled.Receipt.Status, cancelled.Receipt.Revision, cancelled.LatestSequence = domain.RunCancelled, 2, 2
	confirmation := CommandConfirmation{Command: command, AcceptedAt: snapshot.UpdatedAt, Snapshot: cancelled}
	event := Event{Contract: CurrentContract, JobID: command.JobID, Sequence: 1,
		Kind: EventProgress, CreatedAt: snapshot.UpdatedAt, Payload: json.RawMessage(`{"count":1}`)}
	for _, test := range []struct {
		name   string
		value  any
		decode func([]byte) error
	}{
		{name: "receipt", value: snapshot.Receipt, decode: func(raw []byte) error {
			_, err := DecodeReceipt(bytes.NewReader(raw))
			return err
		}},
		{name: "snapshot", value: snapshot, decode: func(raw []byte) error {
			_, err := DecodeSnapshot(bytes.NewReader(raw))
			return err
		}},
		{name: "event", value: event, decode: func(raw []byte) error {
			_, err := DecodeEvent(bytes.NewReader(raw))
			return err
		}},
		{name: "confirmation", value: confirmation, decode: func(raw []byte) error {
			_, err := DecodeCommandConfirmation(bytes.NewReader(raw))
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.value)
			if err != nil || test.decode(raw) != nil {
				t.Fatal("valid current document rejected")
			}
			fields := map[string]json.RawMessage{}
			if err := json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			for name, value := range fields {
				for _, replacement := range []string{"missing", "null", "case", "duplicate"} {
					t.Run(name+"/"+replacement, func(t *testing.T) {
						changed := map[string]json.RawMessage{}
						for key, value := range fields {
							changed[key] = value
						}
						switch replacement {
						case "missing":
							delete(changed, name)
						case "null":
							changed[name] = json.RawMessage("null")
						case "case":
							delete(changed, name)
							changed[strings.ToUpper(name)] = value
						}
						document, _ := json.Marshal(changed)
						if replacement == "duplicate" {
							key, _ := json.Marshal(name)
							document = append(append(document[:len(document)-1], ','), key...)
							document = append(append(append(document, ':'), value...), '}')
						}
						if err := test.decode(document); err == nil {
							t.Fatal("malformed current field accepted")
						}
					})
				}
			}
			if err := test.decode(append(raw, []byte(` {}`)...)); err == nil {
				t.Fatal("trailing JSON accepted")
			}
		})
	}
}
