package jobcontract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func TestSnapshotStateValidationBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
		valid  bool
	}{
		{name: "initial", valid: true},
		{name: "revision zero", change: func(state *Snapshot) { state.Receipt.Revision = 0 }},
		{name: "queued advanced revision", change: func(state *Snapshot) {
			state.Receipt.Revision, state.LatestSequence = 2, 2
		}},
		{name: "running initial revision", change: func(state *Snapshot) { state.Receipt.Status = domain.RunRunning }},
		{name: "maximum counters", change: func(state *Snapshot) {
			state.Receipt.Status = domain.RunRunning
			state.Receipt.Revision, state.LatestSequence = math.MaxInt64, math.MaxInt64
		}, valid: true},
		{name: "revision overflow", change: func(state *Snapshot) {
			state.Receipt.Revision, state.LatestSequence = math.MaxInt64+1, math.MaxInt64+1
		}},
		{name: "sequence zero", change: func(state *Snapshot) { state.LatestSequence = 0 }},
		{name: "sequence overflow", change: func(state *Snapshot) { state.LatestSequence = math.MaxInt64 + 1 }},
		{name: "sequence behind revision", change: func(state *Snapshot) {
			state.Receipt.Status, state.Receipt.Revision = domain.RunRunning, 2
		}},
		{name: "zero time", change: func(state *Snapshot) { state.Receipt.AcceptedAt = time.Time{} }},
		{name: "non UTC accepted", change: func(state *Snapshot) {
			state.Receipt.AcceptedAt = state.Receipt.AcceptedAt.In(time.FixedZone("test", 3600))
		}},
		{name: "backward time", change: func(state *Snapshot) {
			state.UpdatedAt = state.Receipt.AcceptedAt.Add(-time.Nanosecond)
		}},
		{name: "nil run ids", change: func(state *Snapshot) { state.Receipt.RunIDs = nil }},
		{name: "nil report ids", change: func(state *Snapshot) { state.Receipt.ReportIDs = nil }},
		{name: "duplicate child ids", change: func(state *Snapshot) {
			state.Receipt.RunIDs = []string{testReportID, testReportID}
		}},
		{name: "failed without failure", change: func(state *Snapshot) {
			state.Receipt.Status, state.Receipt.Revision, state.LatestSequence = domain.RunFailed, 2, 2
		}},
		{name: "failure on queued", change: func(state *Snapshot) {
			state.Failure = &domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}
		}},
		{name: "failed with stable failure", change: func(state *Snapshot) {
			state.Receipt.Status, state.Receipt.Revision, state.LatestSequence = domain.RunFailed, 2, 2
			state.Failure = &domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}
		}, valid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := performanceSpec(t).InitialSnapshot(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if test.change != nil {
				test.change(&state)
			}
			if err := state.ValidateState(); (err == nil) != test.valid {
				t.Fatalf("ValidateState()=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestEventPayloadAndCompleteEnvelopeByteBoundaries(t *testing.T) {
	event := Event{
		Contract: CurrentContract, JobID: "77777777-7777-4777-8777-777777777770", Sequence: 1,
		Kind: EventProgress, CreatedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
		Payload: json.RawMessage(`""`),
	}
	base, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	headerBytes := len(base) - 2
	targetPayloadBytes := MaxEventBytes - headerBytes
	event.Payload = json.RawMessage(`"` + strings.Repeat("x", targetPayloadBytes-2) + `"`)
	encoded, err := json.Marshal(event)
	if err != nil || len(encoded) != MaxEventBytes {
		t.Fatalf("invalid maximum-envelope fixture: bytes=%d err=%v", len(encoded), err)
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("maximum complete envelope rejected: %v", err)
	}
	event.Payload = append(event.Payload[:len(event.Payload)-1], 'x', '"')
	if err := event.Validate(); err == nil {
		t.Fatal("complete envelope one byte beyond its limit was accepted")
	}
	event.Payload = json.RawMessage(`"` + strings.Repeat("<", MaxEventBytes/2) + `"`)
	if err := event.Validate(); err == nil {
		t.Fatal("JSON escaping expanded the event past its complete-envelope budget")
	}
}
