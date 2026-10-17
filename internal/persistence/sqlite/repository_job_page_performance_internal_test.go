package sqlite

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func performancePageRaw(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := marshalCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func storedPerformancePageFixture(t *testing.T) (storedJobPerformance, executionRead) {
	t.Helper()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "page-model", APIKey: "in-memory-fixture-secret",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	prepared.ReportID = "11111111-1111-4111-8111-111111111111"
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: "22222222-2222-4222-8222-222222222222",
		NodeInstallationID: "33333333-3333-4333-8333-333333333333", EngineBuildID: strings.Repeat("a", 64),
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: "44444444-4444-4444-8444-444444444444"},
		Kind:   jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
	}
	origin := domain.ExecutionOrigin{
		Kind: "remote", NodeID: spec.Target.NodeID, InstallationID: spec.NodeInstallationID,
		EngineBuildID: spec.EngineBuildID, RunnerVersion: "page-test", OS: "linux", Arch: "amd64",
	}
	at := time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC)
	terminal, err := spec.InitialSnapshot(at, origin)
	if err != nil {
		t.Fatal(err)
	}
	terminal.Receipt.Status, terminal.Receipt.Revision, terminal.LatestSequence = domain.RunCancelled, 3, 4
	terminal.UpdatedAt = at.Add(3 * time.Second)
	observation := jobcontract.PerformanceObservation{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, Revision: 1, RecordedAt: at.Add(time.Second),
		Checkpoint: quicktest.PerformanceCheckpoint{
			Stage: quicktest.PerformanceStagePreparing, Boundary: quicktest.PerformanceBoundaryEntered,
			Report: quicktest.PerformanceReport{
				SchemaVersion: quicktest.PerformanceSchemaVersion, ReportID: prepared.ReportID,
				Protocol: prepared.Protocol, AddressMode: prepared.AddressMode, BaseURL: prepared.BaseURL,
				Endpoint: prepared.Endpoint, ModelID: prepared.ModelID, Profile: prepared.Profile,
				ArchiveStatus: quicktest.PerformanceArchiveNotAttempted,
				Progress:      quicktest.PerformanceProgress{Phase: quicktest.PerformancePhaseNotStarted},
				Samples:       []quicktest.PerformanceSample{}, Failures: []quicktest.PerformanceFailure{},
			},
		},
	}
	result := jobcontract.PerformanceResult{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, ReportID: prepared.ReportID,
		Terminal: terminal, SealedAt: at.Add(4 * time.Second), ExecutionStarted: true,
		ObservationState: jobcontract.PerformanceInterrupted, Observation: &observation,
	}
	mirrored := terminal
	mirrored.LatestSequence++
	mirrored.UpdatedAt = result.SealedAt
	mirror := remoteresults.Mirror{Spec: spec, Snapshot: mirrored}
	if err := validateRemotePerformanceResult(mirror, result); err != nil {
		t.Fatalf("pure performance fixture is invalid: %v", err)
	}
	stored := storedJobPerformance{
		jobID: spec.JobID, remote: true, referenceCount: 1, imported: 1, resultCount: 1,
		reportID: prepared.ReportID, resultReportID: prepared.ReportID,
		resultDocument: performancePageRaw(t, result), started: true,
		resultEventCount: 1, resultSequence: terminal.LatestSequence + 1,
		checkpointPresent: true, checkpointSequence: 2,
	}
	reference := performanceResultReference(result, stored.resultDocument)
	stored.referenceDocument = performancePageRaw(t, reference)
	stored.resultEvent = performancePageRaw(t, jobcontract.Event{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, Sequence: stored.resultSequence,
		Kind: jobcontract.EventPerformanceResult, CreatedAt: result.SealedAt, Payload: stored.referenceDocument,
	})
	stored.checkpointEvent = performancePageRaw(t, jobcontract.Event{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, Sequence: stored.checkpointSequence,
		Kind: jobcontract.EventPerformanceCheckpoint, CreatedAt: observation.RecordedAt,
		Payload: performancePageRaw(t, jobcontract.PerformanceCheckpointEvent{
			ReportID: prepared.ReportID, Revision: observation.Revision,
			Stage: observation.Checkpoint.Stage, Boundary: observation.Checkpoint.Boundary,
		}),
	})
	read := executionRead{mirror: &mirror}
	read.Spec, read.Execution = spec, &mirrored
	return stored, read
}

func TestStoredJobPerformancePureDecodeRejectsCorruption(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*storedJobPerformance, *executionRead)
		ready  bool
		valid  bool
	}{
		{name: "current", ready: true, valid: true},
		{name: "unimported reference", valid: true, change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.imported, stored.resultCount = 0, 0
		}},
		{name: "no reference", valid: true, change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.referenceCount, stored.imported, stored.resultCount = 0, 0, 0
		}},
		{name: "duplicate references", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.referenceCount = 2
		}},
		{name: "orphan result", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.referenceCount, stored.imported = 0, 0
		}},
		{name: "missing imported result", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultCount = 0
		}},
		{name: "wrong report", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultReportID = stored.jobID
		}},
		{name: "changed result bytes", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultDocument[0] = '['
		}},
		{name: "wrong current spec", change: func(_ *storedJobPerformance, read *executionRead) {
			read.Spec.Spec.Performance.ModelID = "different-model"
		}},
		{name: "nonterminal reference", change: func(_ *storedJobPerformance, read *executionRead) {
			read.Execution.Receipt.Status = domain.RunRunning
		}},
		{name: "missing start fact", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.started = false
		}},
		{name: "missing result anchor", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultEventCount = 0
		}},
		{name: "duplicate result anchors", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultEventCount = 2
		}},
		{name: "wrong result sequence", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.resultSequence++
		}},
		{name: "missing checkpoint", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.checkpointPresent = false
		}},
		{name: "wrong checkpoint column", change: func(stored *storedJobPerformance, _ *executionRead) {
			stored.checkpointSequence++
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			stored, read := storedPerformancePageFixture(t)
			if test.change != nil {
				test.change(&stored, &read)
			}
			ready, err := stored.decode(&read)
			if (err == nil) != test.valid || ready != test.ready {
				t.Fatalf("decode ready=%v error=%v; want ready=%v valid=%v", ready, err, test.ready, test.valid)
			}
			if !test.valid && read.performanceResult != nil {
				t.Fatal("rejected result was retained as validated")
			}
		})
	}
}

func TestStoredJobPerformancePureCheckpointAnchors(t *testing.T) {
	for _, field := range []string{"time", "revision", "stage", "boundary", "rung", "future-sequence"} {
		t.Run(field, func(t *testing.T) {
			stored, read := storedPerformancePageFixture(t)
			var event jobcontract.Event
			if err := json.Unmarshal(stored.checkpointEvent, &event); err != nil {
				t.Fatal(err)
			}
			var checkpoint jobcontract.PerformanceCheckpointEvent
			if err := json.Unmarshal(event.Payload, &checkpoint); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "time":
				event.CreatedAt = event.CreatedAt.Add(time.Second)
			case "revision":
				checkpoint.Revision++
			case "stage":
				checkpoint.Stage = quicktest.PerformanceStageMeasured
			case "boundary":
				checkpoint.Boundary = quicktest.PerformanceBoundaryReturned
			case "rung":
				index := uint32(1)
				checkpoint.RungIndex = &index
			case "future-sequence":
				stored.checkpointSequence = stored.resultSequence + 1
				event.Sequence = stored.checkpointSequence
			}
			event.Payload = performancePageRaw(t, checkpoint)
			stored.checkpointEvent = performancePageRaw(t, event)
			if ready, err := stored.decode(&read); err == nil || ready {
				t.Fatalf("changed checkpoint %s accepted: ready=%v error=%v", field, ready, err)
			}
		})
	}
}

func TestStoredJobPerformancePureResultEventAnchors(t *testing.T) {
	for _, field := range []string{"time", "sequence", "owner", "reference"} {
		t.Run(field, func(t *testing.T) {
			stored, read := storedPerformancePageFixture(t)
			var event jobcontract.Event
			if err := json.Unmarshal(stored.resultEvent, &event); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "time":
				event.CreatedAt = event.CreatedAt.Add(time.Second)
			case "sequence":
				event.Sequence++
			case "owner":
				event.JobID = stored.reportID
			case "reference":
				var reference jobcontract.PerformanceResultReference
				if err := json.Unmarshal(event.Payload, &reference); err != nil {
					t.Fatal(err)
				}
				reference.SHA256 = strings.Repeat("b", 64)
				event.Payload = performancePageRaw(t, reference)
			}
			stored.resultEvent = performancePageRaw(t, event)
			if ready, err := stored.decode(&read); err == nil || ready {
				t.Fatalf("changed result event %s accepted: ready=%v error=%v", field, ready, err)
			}
		})
	}
}

func TestStoredJobPerformancePureDecodeAllowsNoObservationWithoutCheckpoint(t *testing.T) {
	for _, started := range []bool{false, true} {
		stored, read := storedPerformancePageFixture(t)
		var result jobcontract.PerformanceResult
		if err := json.Unmarshal(stored.resultDocument, &result); err != nil {
			t.Fatal(err)
		}
		result.Observation, result.ExecutionStarted = nil, started
		result.ObservationState = jobcontract.PerformanceNotStarted
		if started {
			result.ObservationState = jobcontract.PerformanceInterrupted
		}
		stored.resultDocument = performancePageRaw(t, result)
		stored.started, stored.checkpointPresent, stored.checkpointEvent = started, false, nil
		stored.referenceDocument = performancePageRaw(t, performanceResultReference(result, stored.resultDocument))
		var event jobcontract.Event
		if err := json.Unmarshal(stored.resultEvent, &event); err != nil {
			t.Fatal(err)
		}
		event.Payload = stored.referenceDocument
		stored.resultEvent = performancePageRaw(t, event)
		if ready, err := stored.decode(&read); err != nil || !ready {
			t.Fatalf("no-observation started=%v: ready=%v error=%v", started, ready, err)
		}
	}
}

func TestStoredJobPerformancePureLocalScalarPresence(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*storedJobPerformance)
		ready  bool
		valid  bool
	}{
		{name: "current", ready: true, valid: true},
		{name: "not sealed", valid: true, change: func(stored *storedJobPerformance) { stored.localPresent = false }},
		{name: "wrong report", change: func(stored *storedJobPerformance) { stored.reportID = stored.jobID }},
		{name: "wrong revision", change: func(stored *storedJobPerformance) { stored.localRevision++ }},
		{name: "wrong status", change: func(stored *storedJobPerformance) { stored.localStatus = string(domain.RunRunning) }},
		{name: "invalid digest", change: func(stored *storedJobPerformance) { stored.localDigest = "invalid" }},
		{name: "size mismatch", change: func(stored *storedJobPerformance) { stored.localActualSize++ }},
		{name: "invalid size", change: func(stored *storedJobPerformance) { stored.localSize = 0 }},
		{name: "invalid stamp", change: func(stored *storedJobPerformance) {
			stored.localSealedAt = "2026-10-16T01:00:03+00:00"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, read := storedPerformancePageFixture(t)
			read.mirror = nil
			read.Spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
			read.Execution.Receipt.Origin.Kind = "local"
			stored := storedJobPerformance{
				jobID: read.Spec.JobID, reportID: read.Spec.Spec.Performance.ReportID,
				localPresent: true, localDigest: strings.Repeat("a", 64), localSize: 1, localActualSize: 1,
				localStatus: string(read.Execution.Receipt.Status), localRevision: read.Execution.Receipt.Revision,
				localSealedAt: formatTime(read.Execution.UpdatedAt),
			}
			if test.change != nil {
				test.change(&stored)
			}
			ready, err := stored.decode(&read)
			if ready != test.ready || (err == nil) != test.valid {
				t.Fatalf("local scalar decode ready=%v error=%v; want ready=%v valid=%v", ready, err, test.ready, test.valid)
			}
			if read.performanceResult != nil {
				t.Fatal("scalar local presence fabricated a full result")
			}
		})
	}
}

func TestStoredJobPerformanceByteSizeIncludesEveryRawDocument(t *testing.T) {
	stored := storedJobPerformance{
		referenceDocument: []byte("ref"), resultDocument: []byte("result"),
		resultEvent: []byte("event"), checkpointEvent: []byte("checkpoint"),
	}
	if got := stored.byteSize(); got != 24 {
		t.Fatalf("retained raw bytes=%d, want 24", got)
	}
}
