package runs_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/application/runs"
	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
)

func TestStartRunPinsPlanExecutesAndPersistsResults(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	reporter := &recordingReporter{}
	service, err := runs.New(runs.Dependencies{
		Repository:  repository,
		Credentials: store,
		Executor:    executor,
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		Reporter:    reporter,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })

	if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	request := <-executor.entered
	if request.Run.Snapshot().Model.ID != fixture.model.ID || request.Run.Snapshot().Channel.ID != fixture.channel.ID {
		t.Fatalf("execution snapshot target = %#v", request.Run.Snapshot())
	}
	if len(request.Cases) != 1 || request.Cases[0].Revision != fixture.testCase.Revision {
		t.Fatalf("execution cases = %#v", request.Cases)
	}
	secret, err := request.Credential.Bytes()
	if err != nil || string(secret) != "test-secret" {
		t.Fatalf("execution credential unavailable: %v", err)
	}
	clear(secret)
	close(executor.release)

	waitForStatus(t, repository, domain.RunCompleted)
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if len(repository.results) != 2 || repository.results[0].RequestID == "" || repository.results[0].CaseID != "" ||
		repository.results[1].CaseID != fixture.testCase.ID || repository.results[1].RequestID != "" || !repository.results[1].Success.Overall() {
		t.Fatalf("persisted results = %#v", repository.results)
	}
	if _, err := request.Credential.Bytes(); err == nil {
		t.Fatal("credential lease remains readable after execution")
	}
	var reportedRunID string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		reporter.mu.Lock()
		reportedRunID = reporter.runID
		reporter.mu.Unlock()
		if reportedRunID != "" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if reportedRunID != request.Run.Meta().ID {
		t.Fatalf("reported run id = %q", reportedRunID)
	}
}

type recordingReporter struct {
	mu    sync.Mutex
	runID string
}

func (reporter *recordingReporter) Generate(_ context.Context, runID string) error {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()
	reporter.runID = runID
	return nil
}

func TestStartRunRejectsManualCasesBeforeCreatingDurableState(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.testCase.ExecutionMode = domain.CaseExecutionManual
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: &recordingExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.StartRun(context.Background(), fixture.plan.ID); err != runs.ErrNotRunnable {
		t.Fatalf("StartRun(manual case) error = %v, want ErrNotRunnable", err)
	}
	if repository.run.Meta().ID != "" {
		t.Fatal("manual case created a durable run")
	}
}

func TestStartRunRejectsInsecureEndpointBeforeCredentialLease(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.channel.BaseURL = "http://api.example.test/v1"
	repository := &fakeRepository{fixture: fixture}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: panicCredentialStore{}, Executor: &recordingExecutor{},
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.StartRun(context.Background(), fixture.plan.ID); !errors.Is(err, runs.ErrNotRunnable) {
		t.Fatalf("StartRun(http endpoint) error = %v, want ErrNotRunnable", err)
	}
}

type panicCredentialStore struct{}

func (panicCredentialStore) Get(context.Context, credentials.StoreRef) (*credentials.Lease, error) {
	panic("credential lease must not be requested for an insecure endpoint")
}

func TestStartTargetUsesAnExplicitModelAndChannelForComparisonRuns(t *testing.T) {
	fixture := newRunFixture(t)
	secondModelID := "30000000-0000-4000-8000-000000000010"
	secondChannelID := "30000000-0000-4000-8000-000000000011"
	fixture.plan.ModelIDs = append(fixture.plan.ModelIDs, secondModelID)
	fixture.plan.ChannelIDs = append(fixture.plan.ChannelIDs, secondChannelID)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	runID, err := service.StartTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID,
	})
	if err != nil || !domain.IsUUID(runID) {
		t.Fatalf("StartTarget() = %q, %v", runID, err)
	}
	request := <-executor.entered
	close(executor.release)
	if request.Run.Snapshot().Model.ID != fixture.model.ID || request.Run.Snapshot().Channel.ID != fixture.channel.ID {
		t.Fatalf("comparison target snapshot = %#v", request.Run.Snapshot())
	}
	if repository.selectedModelID != fixture.model.ID || repository.selectedChannelID != fixture.channel.ID {
		t.Fatalf("repository selection = %s/%s", repository.selectedModelID, repository.selectedChannelID)
	}
}

func TestPrepareTargetStaysQueuedUntilExplicitActivation(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	executor := &controlledExecutor{entered: make(chan runs.ExecutionRequest, 1), release: make(chan struct{})}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	runID, err := service.PrepareTarget(context.Background(), runs.StartCommand{
		PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	status := repository.run.Status()
	repository.mu.Unlock()
	if status != domain.RunQueued {
		t.Fatalf("prepared run status = %q", status)
	}
	select {
	case <-executor.entered:
		t.Fatal("prepared run executed before activation")
	default:
	}
	if err := service.ActivateRun(context.Background(), runID); err != nil {
		t.Fatal(err)
	}
	<-executor.entered
	close(executor.release)
	waitForStatus(t, repository, domain.RunCompleted)
}

type controlledExecutor struct {
	entered chan runs.ExecutionRequest
	release chan struct{}
}

func TestStopSendingDrainsAndCancelPersistsDistinctTerminalStates(t *testing.T) {
	for _, test := range []struct {
		name       string
		command    func(*runs.Service, string) error
		wantStatus domain.RunStatus
	}{
		{name: "stop", command: func(service *runs.Service, id string) error { return service.StopSending(context.Background(), id) }, wantStatus: domain.RunFailed},
		{name: "cancel", command: func(service *runs.Service, id string) error { return service.CancelRun(context.Background(), id) }, wantStatus: domain.RunCancelled},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRunFixture(t)
			repository := &fakeRepository{fixture: fixture}
			store := credentials.NewMemoryStore()
			storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
			_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
			executor := &signalExecutor{entered: make(chan runs.ExecutionRequest, 1)}
			service, err := runs.New(runs.Dependencies{
				Repository: repository, Credentials: store, Executor: executor,
				Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
			})
			if err != nil {
				t.Fatal(err)
			}
			defer service.Close()
			if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
				t.Fatal(err)
			}
			<-executor.entered
			repository.mu.Lock()
			runID := repository.run.Meta().ID
			repository.mu.Unlock()
			if err := test.command(service, runID); err != nil {
				t.Fatalf("command error = %v", err)
			}
			waitForStatus(t, repository, test.wantStatus)
			if test.name == "stop" {
				repository.mu.Lock()
				statuses := append([]domain.RunStatus(nil), repository.statuses...)
				repository.mu.Unlock()
				if !containsRunStatus(statuses, domain.RunDraining) {
					t.Fatalf("stop transitions = %v, missing draining", statuses)
				}
			}
		})
	}
}

func TestCloseCancelsAndPersistsActiveRun(t *testing.T) {
	fixture := newRunFixture(t)
	repository := &fakeRepository{fixture: fixture}
	store := credentials.NewMemoryStore()
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	executor := &signalExecutor{entered: make(chan runs.ExecutionRequest, 1)}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.StartRun(context.Background(), fixture.plan.ID); err != nil {
		t.Fatal(err)
	}
	<-executor.entered
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	waitForStatus(t, repository, domain.RunCancelled)
	if err := service.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

type signalExecutor struct{ entered chan runs.ExecutionRequest }

func (executor *signalExecutor) Execute(ctx context.Context, request runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
	executor.entered <- request
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-request.StopSending:
		return nil
	}
}

func (executor *controlledExecutor) Execute(ctx context.Context, request runs.ExecutionRequest, emit func(runs.ResultDraft) error) error {
	executor.entered <- request
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-executor.release:
	}
	return emit(runs.ResultDraft{
		CaseID: request.Cases[0].ID, RequestID: "request-1",
		Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics: map[string]float64{"e2e_ms": 12},
	})
}

type fakeRepository struct {
	mu                sync.Mutex
	fixture           runFixture
	run               domain.Run
	results           []domain.Result
	statuses          []domain.RunStatus
	selectedModelID   string
	selectedChannelID string
}

func (repository *fakeRepository) GetPlan(context.Context, string) (domain.Plan, error) {
	return repository.fixture.plan, nil
}

func (repository *fakeRepository) ResolvePlanTargetSelection(_ context.Context, _ domain.Plan, modelID, channelID string) (domain.Model, domain.Channel, domain.ChannelModel, error) {
	repository.selectedModelID = modelID
	repository.selectedChannelID = channelID
	return repository.fixture.model, repository.fixture.channel, repository.fixture.mapping, nil
}

func (repository *fakeRepository) GetTestCaseRevision(context.Context, string, uint64) (domain.TestCase, error) {
	return repository.fixture.testCase, nil
}

func (repository *fakeRepository) GetCredentialRef(context.Context, string) (domain.CredentialRef, error) {
	return repository.fixture.credential, nil
}

func (repository *fakeRepository) CreateRun(_ context.Context, run domain.Run) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.run = run
	repository.statuses = append(repository.statuses, run.Status())
	return nil
}

func (repository *fakeRepository) GetRun(context.Context, string) (domain.Run, error) {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	return repository.run, nil
}

func (repository *fakeRepository) UpdateRun(_ context.Context, expected uint64, run domain.Run) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.run.Meta().Revision != expected {
		return context.Canceled
	}
	repository.run = run
	repository.statuses = append(repository.statuses, run.Status())
	return nil
}

func (repository *fakeRepository) AppendResult(_ context.Context, result domain.Result) error {
	repository.mu.Lock()
	defer repository.mu.Unlock()
	repository.results = append(repository.results, result)
	return nil
}

type stepClock struct {
	mu   sync.Mutex
	next time.Time
}

func (clock *stepClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	result := clock.next
	clock.next = clock.next.Add(time.Millisecond)
	return result
}

type runFixture struct {
	now         time.Time
	model       domain.Model
	credential  domain.CredentialRef
	channel     domain.Channel
	mapping     domain.ChannelModel
	testCase    domain.TestCase
	plan        domain.Plan
	environment domain.EnvironmentSnapshot
}

func newRunFixture(t *testing.T) runFixture {
	t.Helper()
	now := time.Date(2026, 8, 31, 1, 2, 3, 0, time.UTC)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	modelID := "30000000-0000-4000-8000-000000000001"
	credentialID := "30000000-0000-4000-8000-000000000002"
	channelID := "30000000-0000-4000-8000-000000000003"
	mappingID := "30000000-0000-4000-8000-000000000004"
	caseID := "30000000-0000-4000-8000-000000000005"
	planID := "30000000-0000-4000-8000-000000000006"
	model := domain.Model{EntityMeta: meta(modelID), Name: "test-model", Protocol: domain.ProtocolOpenAIChat}
	digest := sha256.Sum256([]byte("test fingerprint"))
	credential := domain.CredentialRef{EntityMeta: meta(credentialID), StoreRef: "llm-studio/v1/channel_api_key/" + credentialID, Purpose: domain.CredentialChannelAPIKey, MaskedSuffix: "key1", Fingerprint: "sha256:" + hex.EncodeToString(digest[:])}
	channel := domain.Channel{EntityMeta: meta(channelID), Name: "test-channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true, CredentialID: credentialID}
	mapping := domain.ChannelModel{EntityMeta: meta(mappingID), ChannelID: channelID, ModelID: modelID, UpstreamModelName: "upstream-model"}
	testCase := domain.TestCase{
		EntityMeta: meta(caseID), Key: "T001", Name: "basic", Dimension: "compatibility", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: 1,
			Request:       domain.TestRequest{Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`)},
			Expected:      domain.TestExpected{AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable},
			Assertions:    []domain.TestAssertion{{Kind: domain.AssertionText, Config: json.RawMessage(`{"non_empty":true}`)}},
		},
	}
	plan := domain.Plan{
		EntityMeta: meta(planID), Name: "single target", ModelIDs: []string{modelID}, ChannelIDs: []string{channelID}, Cases: []domain.CaseRevisionRef{{CaseID: caseID, Revision: 1}},
		Load: domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000},
		SLA:  domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1000}},
	}
	environment := domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "test"}
	return runFixture{now: now, model: model, credential: credential, channel: channel, mapping: mapping, testCase: testCase, plan: plan, environment: environment}
}

func waitForStatus(t *testing.T, repository *fakeRepository, status domain.RunStatus) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repository.mu.Lock()
		current := repository.run.Status()
		repository.mu.Unlock()
		if current == status {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run did not reach %q", status)
}

func containsRunStatus(statuses []domain.RunStatus, target domain.RunStatus) bool {
	for _, status := range statuses {
		if status == target {
			return true
		}
	}
	return false
}
