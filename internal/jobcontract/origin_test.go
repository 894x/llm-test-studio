package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestInitialSnapshotRequiresOriginMatchingTheFrozenDestination(t *testing.T) {
	spec := performanceSpec(t)
	for _, change := range []func(*domain.ExecutionOrigin){
		func(origin *domain.ExecutionOrigin) { *origin = domain.ExecutionOrigin{} },
		func(origin *domain.ExecutionOrigin) { origin.Kind = "remote" },
		func(origin *domain.ExecutionOrigin) { origin.InstallationID = spec.JobID },
		func(origin *domain.ExecutionOrigin) { origin.EngineBuildID = spec.JobID },
	} {
		origin := originFixture(spec)
		change(&origin)
		if _, err := spec.InitialSnapshot(time.Now().UTC(), origin); err == nil {
			t.Fatal("incomplete or foreign origin was inferred from the submitted input")
		}
	}
	spec.Target = ExecutionTarget{Kind: TargetRemote, NodeID: validOriginFixture().NodeID}
	origin := originFixture(spec)
	if _, err := spec.InitialSnapshot(time.Now().UTC(), origin); err != nil {
		t.Fatal(err)
	}
	origin.NodeID = spec.JobID
	if _, err := spec.InitialSnapshot(time.Now().UTC(), origin); err == nil {
		t.Fatal("remote node identity differs from the frozen destination")
	}
}

func TestReceiptDecoderRequiresEveryCurrentOriginField(t *testing.T) {
	spec := performanceSpec(t)
	snapshot, err := spec.InitialSnapshot(time.Now().UTC(), originFixture(spec))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(snapshot.Receipt)
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	origin := map[string]json.RawMessage{}
	if err := json.Unmarshal(fields["origin"], &origin); err != nil {
		t.Fatal(err)
	}
	for name := range origin {
		for _, replacement := range []string{"missing", "null", "duplicate"} {
			t.Run(name+"/"+replacement, func(t *testing.T) {
				changed := map[string]json.RawMessage{}
				for key, value := range origin {
					changed[key] = value
				}
				if replacement == "missing" {
					delete(changed, name)
				}
				if replacement == "null" {
					changed[name] = json.RawMessage("null")
				}
				document, _ := json.Marshal(changed)
				if replacement == "duplicate" {
					key, _ := json.Marshal(name)
					document = append(append(document[:len(document)-1], ','), key...)
					document = append(append(append(document, ':'), origin[name]...), '}')
				}
				fields["origin"] = document
				receipt, _ := json.Marshal(fields)
				if _, err := DecodeReceipt(bytes.NewReader(receipt)); err == nil {
					t.Fatal("malformed current execution origin accepted")
				}
			})
		}
	}
	delete(fields, "origin")
	fields["node_installation_id"], _ = json.Marshal(spec.NodeInstallationID)
	fields["engine_build_id"], _ = json.Marshal(spec.EngineBuildID)
	previousShape, _ := json.Marshal(fields)
	if _, err := DecodeReceipt(bytes.NewReader(previousShape)); err == nil {
		t.Fatal("removed receipt shape was accepted as the current format")
	}
}

func TestNewSubmissionRequiresExecutionOriginCapability(t *testing.T) {
	capabilities := RunnerCapabilities{JobKinds: []Kind{KindPerformance}, Protocols: []string{"openai-chat"},
		Features: []string{"warmup", "ramp", "capacity", "open_loop", "time_slices", "normal_workload", "random_input"}}
	spec := performanceSpec(t)
	if capabilities.SupportsJob(spec) {
		t.Fatal("runner without immutable execution origin accepted a new submission")
	}
	capabilities.Features = append(capabilities.Features, FeatureExecutionOrigin)
	if !capabilities.SupportsJob(spec) {
		t.Fatal("current origin-capable runner rejected supported execution")
	}
}

func TestEventDecoderRejectsRemovedAndIncompleteNestedReceiptOrigins(t *testing.T) {
	spec := performanceSpec(t)
	snapshot, err := spec.InitialSnapshot(time.Now().UTC(), originFixture(spec))
	if err != nil {
		t.Fatal(err)
	}
	cancelled := snapshot
	cancelled.Receipt.Status, cancelled.Receipt.Revision, cancelled.LatestSequence = domain.RunCancelled, 2, 2
	command := Command{Contract: CurrentContract, CommandID: testMeta(t).ID, JobID: spec.JobID,
		Kind: CommandCancel, ExpectedRevision: 1}
	for _, test := range []struct {
		kind    EventKind
		payload any
		path    []string
	}{
		{kind: EventAccepted, payload: AcceptedEvent{Spec: spec, Snapshot: snapshot}, path: []string{"snapshot", "receipt"}},
		{kind: EventState, payload: cancelled, path: []string{"receipt"}},
		{kind: EventCommand, payload: CommandConfirmation{
			Command: command, AcceptedAt: cancelled.UpdatedAt, Snapshot: cancelled,
		}, path: []string{"snapshot", "receipt"}},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			payload, _ := json.Marshal(test.payload)
			event := Event{Contract: CurrentContract, JobID: spec.JobID, Sequence: 2,
				Kind: test.kind, CreatedAt: snapshot.UpdatedAt, Payload: payload}
			raw, _ := json.Marshal(event)
			if _, err := DecodeEvent(bytes.NewReader(raw)); err != nil {
				t.Fatalf("current receipt-bearing event rejected: %v", err)
			}
			for _, change := range []string{"missing", "null", "partial", "removed fields"} {
				t.Run(change, func(t *testing.T) {
					document := map[string]any{}
					if err := json.Unmarshal(payload, &document); err != nil {
						t.Fatal(err)
					}
					receipt := document
					for _, field := range test.path {
						receipt = receipt[field].(map[string]any)
					}
					switch change {
					case "missing":
						delete(receipt, "origin")
					case "null":
						receipt["origin"] = nil
					case "partial":
						delete(receipt["origin"].(map[string]any), "runner_version")
					case "removed fields":
						delete(receipt, "origin")
						receipt["node_installation_id"] = spec.NodeInstallationID
						receipt["engine_build_id"] = spec.EngineBuildID
					}
					event.Payload, _ = json.Marshal(document)
					raw, _ := json.Marshal(event)
					if _, err := DecodeEvent(bytes.NewReader(raw)); err == nil {
						t.Fatal("event payload bypassed the current receipt contract")
					}
				})
			}
		})
	}
}

func TestAcceptedEventUsesTheCurrentSpecDecoderBeforeTypedExpansion(t *testing.T) {
	spec := performanceSpec(t)
	snapshot, err := spec.InitialSnapshot(time.Now().UTC(), originFixture(spec))
	if err != nil {
		t.Fatal(err)
	}
	current, _ := json.Marshal(spec)
	oversized := `{"spec":{"run":{"snapshot":{"entries":[` +
		strings.Repeat(`{},`, MaxOwnedItems) + `{}]}}}}`
	for _, test := range []struct {
		name     string
		spec     []byte
		tooLarge bool
	}{
		{name: "unknown root field", spec: append([]byte(`{"removed_spec_field":0,`), current[1:]...)},
		{name: "duplicate root field", spec: append([]byte(`{"contract":1,`), current[1:]...)},
		{name: "owned collection budget", spec: []byte(oversized), tooLarge: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, _ := json.Marshal(struct {
				Spec     json.RawMessage `json:"spec"`
				Snapshot Snapshot        `json:"snapshot"`
			}{Spec: test.spec, Snapshot: snapshot})
			event, _ := json.Marshal(Event{Contract: CurrentContract, JobID: spec.JobID, Sequence: 1,
				Kind: EventAccepted, CreatedAt: snapshot.UpdatedAt, Payload: payload})
			_, err := DecodeEvent(bytes.NewReader(event))
			if err == nil {
				t.Fatal("accepted event bypassed the current spec decoder")
			}
			if test.tooLarge && !errors.Is(err, ErrTooLarge) {
				t.Fatalf("owned collections reached typed expansion: %v", err)
			}
		})
	}
}
