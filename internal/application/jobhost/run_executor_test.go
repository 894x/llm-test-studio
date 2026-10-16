package jobhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func coreMeta(t *testing.T) domain.EntityMeta {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func coreRunInput(t *testing.T, baseURL string) runs.PreparedRun {
	t.Helper()
	model, channel := coreMeta(t), coreMeta(t)
	testCase := domain.TestCase{
		EntityMeta: coreMeta(t), Key: "node", Name: "Node request", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definitions: domain.ProtocolDefinitions{domain.ProtocolOpenAIChat: json.RawMessage(
			`{"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"inputs":{},"assertions":[]}`,
		)},
	}
	suite := domain.Suite{
		EntityMeta: coreMeta(t), Key: "node", Name: "Node suite", Protocol: domain.ProtocolOpenAIChat,
		Inputs: []domain.SuiteInput{}, Cases: []domain.CaseRef{{CaseID: testCase.ID}},
	}
	entry := domain.PlanEntry{
		EntryID: coreMeta(t).ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
		Parameters: map[string]json.RawMessage{},
		Load: domain.LoadProfile{
			Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 10_000,
		},
		SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 10_000}},
	}
	plan := domain.Plan{
		EntityMeta: coreMeta(t), Name: "Node plan", Protocol: domain.ProtocolOpenAIChat,
		Seed: 1, Entries: []domain.PlanEntry{entry},
	}
	mapping := domain.ChannelModel{
		EntityMeta: coreMeta(t), ChannelID: channel.ID, ModelID: model.ID,
		Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, UpstreamModelName: "model",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: 1},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: 1},
			Name:              "Model", Protocol: domain.ProtocolOpenAIChat,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: 1},
			Name:              "Node channel", BaseURL: baseURL, Protocol: domain.ProtocolOpenAIChat,
			UpstreamModelName: "model",
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "linux", Arch: "amd64", Region: "mock-node", NetworkEgress: "test",
			AppVersion: "test", EngineVersion: "test",
		},
		PlanDocument: &plan, Mapping: &mapping,
		Entries: []domain.RunEntrySnapshot{{
			EntryID: entry.EntryID, TargetKind: entry.TargetKind, TargetID: entry.TargetID,
			Name: suite.Name, Key: suite.Key, Suite: &suite,
			Cases:           []domain.CaseRevisionRef{{CaseID: testCase.ID, Revision: 1}},
			CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{},
			CaseInputs: map[string]map[string]json.RawMessage{testCase.ID: {}}, Load: entry.Load, SLA: entry.SLA,
		}},
	}
	return runs.PreparedRun{Meta: coreMeta(t), PlanID: plan.ID, Snapshot: snapshot}
}

func coreSubmission(t *testing.T, baseURL string, comparison bool) jobcontract.Submission {
	t.Helper()
	spec := testSubmission(t).Job
	first := coreRunInput(t, baseURL)
	inputs := []runs.PreparedRun{first}
	spec.Kind, spec.Spec = jobcontract.KindRun, jobcontract.Payload{Run: &first}
	if comparison {
		run, err := domain.NewRun(coreMeta(t), first.PlanID, first.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := run.Snapshot()
		snapshot.Channel.ID = coreMeta(t).ID
		snapshot.Mapping.ChannelID, snapshot.Mapping.ID = snapshot.Channel.ID, coreMeta(t).ID
		inputs = append(inputs, runs.PreparedRun{Meta: run.Meta(), PlanID: run.PlanID(), Snapshot: snapshot})
		spec.Kind, spec.Spec = jobcontract.KindComparison, jobcontract.Payload{Comparison: &comparisons.PreparedComparison{
			Meta: coreMeta(t), Plan: first.Snapshot.Plan, Model: first.Snapshot.Model.EntityRevisionRef, Runs: inputs,
		}}
	}
	secrets := make([]jobcontract.Secret, 0, len(inputs))
	for _, input := range inputs {
		secrets = append(secrets, jobcontract.Secret{Slot: input.Meta.ID, Value: []byte("node-secret")})
	}
	if err := spec.Validate(); err != nil {
		t.Fatal(err)
	}
	return jobcontract.Submission{Job: spec, Secrets: secrets}
}

func newRunCore(t *testing.T, repository jobhost.RunRepository) *jobhost.RunExecutor {
	t.Helper()
	core, err := jobhost.NewRunExecutor(jobhost.RunExecutorDependencies{
		Repository: repository, Executor: runs.NewProtocolExecutor(http.DefaultTransport), Clock: wallClock{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = core.Close() })
	return core
}

func writeCoreResponse(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json")
	_, _ = writer.Write([]byte(`{"id":"mock","choices":[{"message":{"content":"ok"}}],
		"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
}

func waitSettled(t *testing.T, repository jobhost.Repository) {
	t.Helper()
	waitHost(t, func() bool {
		pending, err := repository.RecoveryJobs(t.Context())
		return err == nil && len(pending) == 0
	})
}

func TestRunExecutorUsesRealCoreAndSettlesReportsWithoutClient(t *testing.T) {
	for _, comparison := range []bool{false, true} {
		t.Run(map[bool]string{false: "run", true: "comparison"}[comparison], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				if request.Header.Get("Authorization") != "Bearer node-secret" || request.URL.Path != "/v1/chat/completions" {
					t.Error("Core did not use the admitted channel or credential")
				}
				writeCoreResponse(writer)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			core := newRunCore(t, repository)
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			submission := coreSubmission(t, server.URL, comparison)
			spec := submission.Job
			ctx, cancel := context.WithCancel(t.Context())
			if _, _, err := host.Submit(ctx, &submission); err != nil {
				t.Fatal(err)
			}
			cancel()
			waitJob(
				t,
				repository,
				spec.JobID,
				domain.RunCompleted,
			)
			waitSettled(t, repository)
			ids := []string{}
			if comparison {
				for _, input := range spec.Spec.Comparison.Runs {
					ids = append(ids, input.Meta.ID)
				}
				group, err := repository.GetComparison(t.Context(), spec.Spec.Comparison.Meta.ID)
				if err != nil || group.Status() != domain.ComparisonCompleted {
					t.Fatalf("node did not advance the group: %s, %v", group.Status(), err)
				}
			} else {
				ids = append(ids, spec.Spec.Run.Meta.ID)
			}
			reports, err := repository.ListReportsForRuns(t.Context(), ids)
			if err != nil || len(reports) != len(ids) || requests.Load() != int32(len(ids)) {
				t.Fatalf(
					"reports=%d requests=%d err=%v",
					len(reports),
					requests.Load(),
					err,
				)
			}
			for _, id := range ids {
				results, err := repository.ListResults(t.Context(), id)
				if err != nil || len(results) != 3 {
					t.Fatalf("request, summary and entry facts = %d, %v", len(results), err)
				}
			}
			before, _ := repository.GetJob(t.Context(), spec.JobID)
			for range 2 {
				if err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted}); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := repository.GetJob(t.Context(), spec.JobID)
			if before.LatestSequence != after.LatestSequence || requests.Load() != int32(len(ids)) {
				t.Fatal("idempotent settlement created new events or replayed upstream traffic")
			}
		})
	}
}

func TestRunExecutorStopDrainsAndCancelAbortsRealRequests(t *testing.T) {
	for _, kind := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
		t.Run(string(kind), func(t *testing.T) {
			entered, aborted, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				first := requests.Add(1) == 1
				if first {
					close(entered)
				}
				select {
				case <-request.Context().Done():
					if first {
						close(aborted)
					}
				case <-release:
					writeCoreResponse(writer)
				}
			}))
			defer server.Close()
			releaseRequest := sync.OnceFunc(func() { close(release) })
			defer releaseRequest()
			repository := openHostRepository(t)
			core := newRunCore(t, repository)
			host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
			submission := coreSubmission(t, server.URL, false)
			input := submission.Job.Spec.Run
			input.Snapshot.Entries[0].Load.Mode = domain.LoadFixedConcurrency
			input.Snapshot.Entries[0].Load.RequestCount = 10
			input.Snapshot.PlanDocument.Entries[0].Load = input.Snapshot.Entries[0].Load
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("Core did not start an upstream request")
			}
			snapshot, _ := repository.GetJob(t.Context(), spec.JobID)
			_, _, err := host.ApplyCommand(t.Context(), jobcontract.Command{
				Contract: jobcontract.CurrentContract, CommandID: coreMeta(t).ID, JobID: spec.JobID,
				Kind: kind, ExpectedRevision: snapshot.Receipt.Revision,
			})
			if err != nil {
				t.Fatal(err)
			}
			want := domain.RunCancelled
			if kind == jobcontract.CommandStopSending {
				select {
				case <-aborted:
					t.Fatal("stop-sending aborted the in-flight request")
				case <-time.After(25 * time.Millisecond):
				}
				releaseRequest()
				want = domain.RunCompleted
			}
			waitJob(
				t,
				repository,
				spec.JobID,
				want,
			)
			waitSettled(t, repository)
			if requests.Load() != 1 {
				t.Fatalf("control allowed %d requests, want one", requests.Load())
			}
			child, err := repository.GetRun(t.Context(), input.Meta.ID)
			if err != nil || child.Status() != want {
				t.Fatalf("child terminal state = %s, %v", child.Status(), err)
			}
		})
	}
}

func TestRunExecutorRecoverySealsInterruptedJobsWithoutHTTP(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	spec := coreSubmission(t, server.URL, true).Job
	if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{ContentClaims: []jobcontent.Claim{},
		Spec: spec, Origin: acceptedOrigin(spec), SecretCommitment: strings.Repeat("e", 64), AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	core := newRunCore(t, repository)
	_ = newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	job := waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunFailed,
	)
	if job.Failure == nil || job.Failure.ErrorCode != "runner_interrupted" {
		t.Fatalf("recovered job reason = %+v", job.Failure)
	}
	waitSettled(t, repository)
	ids := []string{}
	for _, input := range spec.Spec.Comparison.Runs {
		ids = append(ids, input.Meta.ID)
		child, err := repository.GetRun(t.Context(), input.Meta.ID)
		if err != nil || child.Status() != domain.RunFailed {
			t.Fatalf("recovered child state = %s, %v", child.Status(), err)
		}
		if child.Failure() == nil || child.Failure().ErrorCode != "runner_interrupted" {
			t.Fatalf("recovered child reason = %+v", child.Failure())
		}
	}
	reports, err := repository.ListReportsForRuns(t.Context(), ids)
	if err != nil || len(reports) != 2 || requests.Load() != 0 {
		t.Fatalf(
			"interrupted reports=%d traffic=%d err=%v",
			len(reports),
			requests.Load(),
			err,
		)
	}
	if err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted}); err != nil {
		t.Fatal("settlement did not preserve already terminal children:", err)
	}
	invalid := spec
	invalid.Spec.Comparison.Runs[0].Snapshot.Channel.Name = "changed after admission"
	err = core.Settle(t.Context(), invalid, jobhost.Outcome{Status: domain.RunCompleted})
	if !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("changed frozen input was accepted: %v", err)
	}
}

type runFaultRepository struct {
	*sqlite.Repository
	failTerminal     bool
	loseTerminal     bool
	terminalOnce     atomic.Bool
	loseReport       bool
	reportOnce       atomic.Bool
	failReportNumber int32
	reportWrites     atomic.Int32
}

func (repository *runFaultRepository) UpdateRun(ctx context.Context, revision uint64, run domain.Run) error {
	if repository.failTerminal && jobcontract.Terminal(run.Status()) {
		return errors.New("injected terminal write outage")
	}
	if err := repository.Repository.UpdateRun(ctx, revision, run); err != nil {
		return err
	}
	if repository.loseTerminal && jobcontract.Terminal(run.Status()) && !repository.terminalOnce.Swap(true) {
		return errors.New("injected lost terminal confirmation")
	}
	return nil
}

type stoppingRunRepository struct {
	*sqlite.Repository
	onRead func()
}

func (repository stoppingRunRepository) GetRun(ctx context.Context, id string) (domain.Run, error) {
	run, err := repository.Repository.GetRun(ctx, id)
	repository.onRead()
	return run, err
}

func TestRunExecutorHandlesStopBeforeActivationWithoutRequests(t *testing.T) {
	for _, duringAdoption := range []bool{false, true} {
		t.Run(map[bool]string{false: "already_stopped", true: "stopped_during_adoption"}[duringAdoption], func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				writeCoreResponse(writer)
			}))
			defer server.Close()
			repository := openHostRepository(t)
			stop := make(chan struct{})
			stopOnce := sync.OnceFunc(func() { close(stop) })
			if !duringAdoption {
				stopOnce()
			}
			core := newRunCore(t, stoppingRunRepository{Repository: repository, onRead: stopOnce})
			submission := coreSubmission(t, server.URL, true)
			defer submission.Clear()
			spec := submission.Job
			if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{ContentClaims: []jobcontent.Claim{},
				Spec: spec, Origin: acceptedOrigin(spec), SecretCommitment: strings.Repeat("f", 64), AcceptedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatal(err)
			}
			leases := map[string]*credentials.Lease{}
			for _, secret := range submission.Secrets {
				lease, err := credentials.NewTemporaryLease(secret.Value)
				if err != nil {
					t.Fatal(err)
				}
				defer lease.Close()
				leases[secret.Slot] = lease
			}
			outcome, err := core.Execute(t.Context(), jobhost.Execution{Spec: spec, Secrets: leases, StopSending: stop})
			if err != nil || outcome.Validate() != nil || requests.Load() != 0 {
				t.Fatalf(
					"early stop: outcome=%+v requests=%d err=%v",
					outcome,
					requests.Load(),
					err,
				)
			}
			if err := core.Settle(t.Context(), spec, outcome); err != nil {
				t.Fatal(err)
			}
			if requests.Load() != 0 {
				t.Fatal("settling early stop sent upstream requests")
			}
		})
	}
}

func TestRunExecutorSettlementReconcilesCommittedTerminalError(t *testing.T) {
	repository := openHostRepository(t)
	spec := coreSubmission(t, "https://must-not-be-requested.example.test", false).Job
	if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{ContentClaims: []jobcontent.Claim{},
		Spec: spec, Origin: acceptedOrigin(spec), SecretCommitment: strings.Repeat("d", 64), AcceptedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ChangeJob(t.Context(), jobhost.Change{
		JobID: spec.JobID, ExpectedRevision: 1, Status: domain.RunCancelled, At: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	core := newRunCore(t, &runFaultRepository{Repository: repository, loseTerminal: true})
	outcome := jobhost.Outcome{Status: domain.RunCancelled}
	if err := core.Settle(t.Context(), spec, outcome); err != nil {
		t.Fatal(err)
	}
	before, _ := repository.GetJob(t.Context(), spec.JobID)
	if err := core.Settle(t.Context(), spec, outcome); err != nil {
		t.Fatal(err)
	}
	after, _ := repository.GetJob(t.Context(), spec.JobID)
	child, err := repository.GetRun(t.Context(), spec.Spec.Run.Meta.ID)
	if err != nil || child.Status() != domain.RunCancelled {
		t.Fatalf("terminal reconciliation = %s, %v", child.Status(), err)
	}
	if before.LatestSequence != after.LatestSequence || child.Meta().Revision != 2 {
		t.Fatal("settlement rewrote a committed terminal child or its report")
	}
}

func TestRunExecutorSnapshotComparisonKeepsNativeObjectAndIntegerIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newRunCore(t, repository)
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, false)
	input := submission.Job.Spec.Run
	input.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
		`{"request":{"body":{"messages":[{"role":"user","content":"hi"}],"seed":9007199254740993}},
		"inputs":{},"assertions":[]}`,
	)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	input.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
		`{"assertions":[],"inputs":{},"request":{"body":{"seed":9007199254740993,
		"messages":[{"content":"hi","role":"user"}]}}}`,
	)
	if err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted}); err != nil {
		t.Fatal("reordered native object was treated as another Job:", err)
	}
	before, _ := repository.GetJob(t.Context(), spec.JobID)
	input.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(
		`{"assertions":[],"inputs":{},"request":{"body":{"seed":9007199254740992,
		"messages":[{"content":"hi","role":"user"}]}}}`,
	)
	err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted})
	if !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("different large integers were treated as equal: %v", err)
	}
	after, _ := repository.GetJob(t.Context(), spec.JobID)
	if before.LatestSequence != after.LatestSequence {
		t.Fatal("rejected snapshot changed the durable Job")
	}
}

func (repository *runFaultRepository) CreateReport(ctx context.Context, report domain.Report) error {
	if repository.failReportNumber > 0 && repository.reportWrites.Add(1) == repository.failReportNumber {
		return errors.New("injected failure before report write")
	}
	if err := repository.Repository.CreateReport(ctx, report); err != nil {
		return err
	}
	if repository.loseReport && !repository.reportOnce.Swap(true) {
		return errors.New("injected lost report confirmation")
	}
	return nil
}

func TestRunExecutorReconcilesLostReportConfirmation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newRunCore(t, &runFaultRepository{Repository: repository, loseReport: true})
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, false)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunCompleted,
	)
	waitSettled(t, repository)
	if err := core.Settle(t.Context(), spec, jobhost.Outcome{Status: domain.RunCompleted}); err != nil {
		t.Fatal(err)
	}
	reports, err := repository.ListReportsForRuns(t.Context(), []string{spec.Spec.Run.Meta.ID})
	if err != nil || len(reports) != 1 || requests.Load() != 1 {
		t.Fatalf(
			"lost confirmation produced reports=%d traffic=%d err=%v",
			len(reports),
			requests.Load(),
			err,
		)
	}
	if !host.Status().Ready {
		t.Fatal("a confirmed durable report made the host unavailable")
	}
}

func TestRunExecutorStorageOutageStopsRetryAndRecoversWithoutReplay(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeCoreResponse(writer)
	}))
	defer server.Close()
	repository := openHostRepository(t)
	core := newRunCore(t, &runFaultRepository{Repository: repository, failTerminal: true})
	host := newTestHost(t, hostDependencies(repository, core, &countedGuard{}))
	submission := coreSubmission(t, server.URL, false)
	spec := submission.Job
	if _, _, err := host.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	waitHost(t, func() bool { return !host.Status().Ready })
	waitJob(
		t,
		repository,
		spec.JobID,
		domain.RunFailed,
	)
	_ = host.Close()
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	child, err := repository.GetRun(t.Context(), spec.Spec.Run.Meta.ID)
	if err != nil || child.Status() != domain.RunRunning {
		t.Fatalf("outage fabricated a durable terminal state: %s, %v", child.Status(), err)
	}
	restartedCore := newRunCore(t, repository)
	restarted := newTestHost(t, hostDependencies(repository, restartedCore, &countedGuard{}))
	waitSettled(t, repository)
	child, err = repository.GetRun(t.Context(), spec.Spec.Run.Meta.ID)
	if err != nil || child.Status() != domain.RunFailed {
		t.Fatalf("recovered terminal state = %s, %v", child.Status(), err)
	}
	if child.Failure() == nil || child.Failure().ErrorCode != "job_storage_unavailable" {
		t.Fatalf("recovered failure reason = %+v", child.Failure())
	}
	if requests.Load() != 1 || !restarted.Status().Ready {
		t.Fatal("recovery replayed upstream traffic or did not become ready")
	}
}

func TestRunExecutorRemoteShutdownAndDeadlinePreserveOwnerFailure(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "deadline", true: "shutdown"}[shutdown], func(t *testing.T) {
			entered := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				close(entered)
				<-request.Context().Done()
			}))
			defer server.Close()
			repository := openHostRepository(t)
			core := newRunCore(t, repository)
			dependencies := hostDependencies(repository, core, &countedGuard{})
			if !shutdown {
				dependencies.Policy.ExecutionTimeout = 2 * time.Second
			}
			host := newTestHost(t, dependencies)
			defer func() { _ = host.Close() }()
			submission := coreSubmission(t, server.URL, false)
			spec := submission.Job
			if _, _, err := host.Submit(t.Context(), &submission); err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("request did not start before the owner interruption")
			}
			want := domain.ErrorCode("job_execution_timeout")
			if shutdown {
				want = "runner_interrupted"
				if err := host.Close(); err != nil {
					t.Fatal(err)
				}
			}
			job := waitJob(
				t,
				repository,
				spec.JobID,
				domain.RunFailed,
			)
			waitSettled(t, repository)
			child, err := repository.GetRun(t.Context(), spec.Spec.Run.Meta.ID)
			if err != nil || child.Status() != domain.RunFailed {
				t.Fatalf("interrupted child = %s, %v", child.Status(), err)
			}
			if job.Failure == nil || job.Failure.ErrorCode != want {
				t.Fatalf("job interruption = %+v, want %s", job.Failure, want)
			}
			if child.Failure() == nil || child.Failure().ErrorCode != want {
				t.Fatalf("child interruption = %+v, want %s", child.Failure(), want)
			}
		})
	}
}
