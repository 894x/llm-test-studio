package jobs

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func sharedJobFixture(t *testing.T) JobRead {
	t.Helper()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "https://example.invalid", ModelID: "model", APIKey: "fixture-only",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
	})
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	prepared.ReportID = "83000000-0000-4000-8000-000000000011"
	spec := jobcontract.JobSpec{Contract: jobcontract.CurrentContract,
		JobID: "83000000-0000-4000-8000-000000000010", NodeInstallationID: "83000000-0000-4000-8000-000000000012",
		EngineBuildID: strings.Repeat("a", 64), Target: jobcontract.ExecutionTarget{
			Kind: jobcontract.TargetRemote, NodeID: "83000000-0000-4000-8000-000000000013"},
		Kind: jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{}}
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: spec.Target.NodeID, InstallationID: spec.NodeInstallationID,
		EngineBuildID: spec.EngineBuildID, RunnerVersion: "original-r4", OS: "linux", Arch: "amd64"}
	snapshot, err := spec.InitialSnapshot(time.Date(2026, 10, 16, 1, 0, 0, 0, time.UTC), origin)
	if err != nil {
		t.Fatal(err)
	}
	return JobRead{Spec: spec, Location: Location{Kind: jobcontract.TargetRemote,
		ConfigurationID: "83000000-0000-4000-8000-000000000014"}, Execution: &snapshot}
}

func ownJobFixture(t *testing.T, read JobRead, state SubmissionState) JobRead {
	t.Helper()
	origin := read.Execution.Receipt.Origin
	runner := jobcontract.RunnerInfo{Contract: jobcontract.CurrentContract, NodeID: origin.NodeID,
		NodeInstallationID: origin.InstallationID, EngineBuildID: origin.EngineBuildID,
		RunnerVersion: "prepared-probe", OS: "linux", Arch: "amd64", Ready: true, Healthy: true,
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 1,
			QueueTimeoutNS: int64(time.Minute), ExecutionTimeoutNS: int64(time.Minute)},
		Capabilities: jobcontract.RunnerCapabilities{JobKinds: []jobcontract.Kind{jobcontract.KindPerformance},
			Protocols: []string{string(domain.ProtocolOpenAIChat)}, Features: []string{jobcontract.FeatureExecutionOrigin}}}
	record, err := NewSubmission(PreparedJob{ConfigurationID: read.Location.ConfigurationID, Runner: runner, Spec: read.Spec},
		read.Execution.Receipt.AcceptedAt.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	switch state {
	case SubmissionPending:
		record.State, record.Revision = state, 2
	case SubmissionAccepted:
		record.State, record.Revision, record.Receipt = state, 3, &read.Execution.Receipt
	}
	read.Submission = &record
	if err := read.Validate(); err != nil {
		t.Fatal(err)
	}
	return read
}

func TestSharedJobViewUsesActualExecutionWithoutInventingSubmission(t *testing.T) {
	read := sharedJobFixture(t)
	view, err := ProjectJob(read, nil, false)
	if err != nil || view.Submission != nil || view.Execution == nil || view.Executor.Version != "original-r4" {
		t.Fatalf("shared execution projection: %v", err)
	}
	document, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(document, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 12 || string(fields["submission"]) != "null" {
		t.Fatal("shared view omitted mandatory null submission or changed current root shape")
	}
	for _, obsolete := range []string{"delivery_revision", "delivery", "observation", "prepared_at", "delivery_updated_at", "confirmation"} {
		if _, present := fields[obsolete]; present {
			t.Fatalf("obsolete root field %s", obsolete)
		}
	}
	read = ownJobFixture(t, read, SubmissionAccepted)
	view, err = ProjectJob(read, nil, false)
	if err != nil || view.Submission == nil || view.Submission.Confirmation == nil || view.Executor.Version != "original-r4" {
		t.Fatal("own delivery lost actual admission origin")
	}
}

func TestJobReadRejectsUnsupportedOrConflictingFacts(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*JobRead)
	}{
		{name: "missing execution", change: func(read *JobRead) { read.Execution = nil }},
		{name: "local without submission", change: func(read *JobRead) {
			read.Spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
			read.Location = Location{Kind: jobcontract.TargetLocal}
		}},
		{name: "unsupported contract", change: func(read *JobRead) { read.Spec.Contract++ }},
		{name: "wrong location", change: func(read *JobRead) { read.Location = Location{Kind: jobcontract.TargetLocal} }},
		{name: "wrong origin", change: func(read *JobRead) { read.Execution.Receipt.Origin.NodeID = read.Location.ConfigurationID }},
		{name: "changed payload", change: func(read *JobRead) {
			*read = ownJobFixture(t, *read, SubmissionAccepted)
			read.Spec.Spec.Performance = new(quicktest.PreparedPerformance)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			read := sharedJobFixture(t)
			test.change(&read)
			if _, err := ProjectJob(read, nil, false); err == nil {
				t.Fatal("invalid source facts became a job view")
			}
		})
	}
}

func TestOwnPendingCanObserveExecutionAndNewerConfirmationHidesOlderMirror(t *testing.T) {
	read := ownJobFixture(t, sharedJobFixture(t), SubmissionPending)
	view, err := ProjectJob(read, nil, false)
	if err != nil || view.Submission.Confirmation != nil || view.Execution == nil || view.Executor.Version != "original-r4" {
		t.Fatal("pending delivery hid actual execution or invented confirmation")
	}
	read = ownJobFixture(t, read, SubmissionAccepted)
	confirmation := *read.Submission.Receipt
	confirmation.Status, confirmation.Revision = domain.RunRunning, 2
	read.Submission.Receipt = &confirmation
	view, err = ProjectJob(read, nil, false)
	if err != nil || view.Execution != nil || view.Submission.Confirmation.Revision != 2 {
		t.Fatal("older remote journal replaced newer admission confirmation")
	}
	read.Submission.State, read.Submission.Revision = SubmissionPrepared, 1
	read.Submission.Receipt = nil
	if _, err := ProjectJob(read, nil, false); !errors.Is(err, ErrConflict) {
		t.Fatal("prepared delivery acquired an execution")
	}
}

func TestSharedPerformanceResultRetainsActualOriginAndRequiresSameAcceptance(t *testing.T) {
	read := sharedJobFixture(t)
	terminal := *read.Execution
	terminal.Receipt.Status, terminal.Receipt.Revision = domain.RunCancelled, 2
	terminal.LatestSequence, terminal.UpdatedAt = 2, terminal.UpdatedAt.Add(time.Second)
	read.Execution = &terminal
	result := jobcontract.PerformanceResult{Contract: jobcontract.CurrentContract, JobID: read.Spec.JobID,
		ReportID: read.Spec.Spec.Performance.ReportID, Terminal: terminal, SealedAt: terminal.UpdatedAt,
		ObservationState: jobcontract.PerformanceNotStarted}
	view, err := ProjectPerformanceResult(read, result)
	if err != nil || view.Executor.Version != "original-r4" || view.ReportID != result.ReportID {
		t.Fatalf("shared sealed result unavailable: %v", err)
	}
	job, err := ProjectJob(read, nil, true)
	if err != nil || !job.PerformanceResultReady || job.Submission != nil {
		t.Fatal("shared sealed result readiness required a local submission")
	}
	result.Terminal.Receipt.Origin.RunnerVersion = "replacement"
	if _, err := ProjectPerformanceResult(read, result); !errors.Is(err, ErrConflict) {
		t.Fatal("sealed result changed the accepted executor")
	}
}
