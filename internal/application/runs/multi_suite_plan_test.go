package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/894x/llm-test-studio/internal/testspec"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestMultiSuitePlanRunsInOrderContinuesAfterFailureAndKeepsOwnership(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"inputs":{"prompt":{"type":"string","default":"default"}},"request":{"body":{"messages":[{"role":"user","content":{"$input":"prompt"}}]}},"assertions":[]}`)
	suite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "85000000-0000-4000-8000-000000000001", SchemaVersion: 1, Revision: 1,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		},
		Key: "ordered", Name: "Ordered", Protocol: fixture.model.Protocols[0],
		Cases:  []domain.CaseRef{{CaseID: fixture.testCase.ID}},
		Inputs: []domain.SuiteInput{{Key: "prompt", Label: "Prompt", Input: testspec.Input{Type: "string", Default: json.RawMessage(`"default"`)}, Bindings: []domain.SuiteInputBinding{{CaseID: fixture.testCase.ID, Input: "prompt"}}}},
	}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}}
	fixture.plan.Entries = []domain.PlanEntry{
		{EntryID: "85000000-0000-4000-8000-000000000002", TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Parameters: map[string]json.RawMessage{"prompt": json.RawMessage(`"first"`)}, Load: load, SLA: sla},
		{EntryID: "85000000-0000-4000-8000-000000000003", TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Parameters: map[string]json.RawMessage{"prompt": json.RawMessage(`"second"`)}, Load: load, SLA: sla},
	}
	repository := &multiSuiteRepository{
		fakeRepository: &fakeRepository{fixture: fixture, testCases: map[string]domain.TestCase{fixture.testCase.ID: fixture.testCase}},
		suites:         map[string]domain.Suite{suite.ID: suite},
	}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	executor := &orderedSuiteExecutor{failFirst: errors.New("first suite failed")}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := startFixtureRun(service, context.Background(), fixture); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	waitForStatus(t, repository.fakeRepository, domain.RunFailed)

	executor.mu.Lock()
	requests := append([]runs.ExecutionRequest(nil), executor.requests...)
	prompts := append([]string(nil), executor.prompts...)
	executor.mu.Unlock()
	if len(requests) != 2 || requests[0].Entry.EntryID != fixture.plan.Entries[0].EntryID ||
		requests[1].Entry.EntryID != fixture.plan.Entries[1].EntryID || prompts[0] != "first" || prompts[1] != "second" {
		t.Fatalf("ordered requests = %#v, prompts = %#v", requests, prompts)
	}

	repository.mu.Lock()
	defer repository.mu.Unlock()
	requestOwners := make(map[string]string)
	markerStatuses := make(map[string]domain.EntryExecutionStatus)
	for _, result := range repository.results {
		if result.RequestID != "" {
			if result.CaseID != fixture.testCase.ID || result.EntryID == "" {
				t.Fatalf("request ownership = %#v", result)
			}
			requestOwners[result.EntryID] = result.RequestID
		}
		if result.EntryStatus != "" {
			markerStatuses[result.EntryID] = result.EntryStatus
		}
	}
	if requestOwners[fixture.plan.Entries[0].EntryID] != fixture.plan.Entries[0].EntryID+":request-1" ||
		requestOwners[fixture.plan.Entries[1].EntryID] != fixture.plan.Entries[1].EntryID+":request-1" ||
		markerStatuses[fixture.plan.Entries[0].EntryID] != domain.EntryExecutionFailed ||
		markerStatuses[fixture.plan.Entries[1].EntryID] != domain.EntryExecutionCompleted {
		t.Fatalf("request owners = %#v, markers = %#v", requestOwners, markerStatuses)
	}
}

func TestMultiSuitePlanCancellationStopsBeforeTheNextSuite(t *testing.T) {
	fixture := newRunFixture(t)
	suite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "85000000-0000-4000-8000-000000000011", SchemaVersion: 1, Revision: 1,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		},
		Key: "cancel-order", Name: "Cancel order", Protocol: fixture.model.Protocols[0], Inputs: []domain.SuiteInput{},
		Cases: []domain.CaseRef{{CaseID: fixture.testCase.ID}},
	}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}}
	fixture.plan.Entries = []domain.PlanEntry{
		{EntryID: "85000000-0000-4000-8000-000000000012", TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
		{EntryID: "85000000-0000-4000-8000-000000000013", TargetKind: domain.PlanTargetSuite, TargetID: suite.ID, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla},
	}
	repository := &multiSuiteRepository{
		fakeRepository: &fakeRepository{fixture: fixture, testCases: map[string]domain.TestCase{fixture.testCase.ID: fixture.testCase}},
		suites:         map[string]domain.Suite{suite.ID: suite},
	}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	executor := &cancelBlockingSuiteExecutor{entered: make(chan string, 1)}
	reporter := &recordingReporter{generated: make(chan string, 2)}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		Reporter: reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	if err := startFixtureRun(service, context.Background(), fixture); err != nil {
		t.Fatal(err)
	}
	if entryID := <-executor.entered; entryID != fixture.plan.Entries[0].EntryID {
		t.Fatalf("first executed suite = %q", entryID)
	}
	repository.mu.Lock()
	runID := repository.run.Meta().ID
	repository.mu.Unlock()
	if err := service.CancelRun(context.Background(), runID); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	waitForStatus(t, repository.fakeRepository, domain.RunCancelled)
	select {
	case reportedRunID := <-reporter.generated:
		if reportedRunID != runID {
			t.Fatalf("reported run id = %q, want %q", reportedRunID, runID)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled multi-suite run did not generate a report")
	}
	time.Sleep(10 * time.Millisecond)
	reporter.mu.Lock()
	reportCalls := reporter.calls
	reporter.mu.Unlock()
	if reportCalls != 1 {
		t.Fatalf("report generation calls = %d, want 1", reportCalls)
	}

	executor.mu.Lock()
	calls := append([]string(nil), executor.entries...)
	executor.mu.Unlock()
	if len(calls) != 1 || calls[0] != fixture.plan.Entries[0].EntryID {
		t.Fatalf("executed suites after cancellation = %v", calls)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	for _, result := range repository.results {
		if result.EntryID == fixture.plan.Entries[1].EntryID {
			t.Fatalf("second suite persisted a result after cancellation: %#v", result)
		}
	}
}

func TestQueuedMultiSuitePlanCancellationGeneratesOneReportWithoutExecution(t *testing.T) {
	fixture := newRunFixture(t)
	suite := domain.Suite{
		EntityMeta: domain.EntityMeta{
			ID: "85000000-0000-4000-8000-000000000021", SchemaVersion: 1, Revision: 1,
			CreatedAt: fixture.now, UpdatedAt: fixture.now,
		},
		Key: "queued-cancel", Name: "Queued cancel", Protocol: fixture.model.Protocols[0], Inputs: []domain.SuiteInput{},
		Cases: []domain.CaseRef{{CaseID: fixture.testCase.ID}},
	}
	fixture.plan.Entries = []domain.PlanEntry{{
		EntryID: "85000000-0000-4000-8000-000000000022", TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
		Parameters: map[string]json.RawMessage{},
		Load:       domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1_000},
		SLA:        domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1_000}},
	}}
	repository := &multiSuiteRepository{
		fakeRepository: &fakeRepository{fixture: fixture, testCases: map[string]domain.TestCase{fixture.testCase.ID: fixture.testCase}},
		suites:         map[string]domain.Suite{suite.ID: suite},
	}
	store := credentials.NewMemoryStore()
	storeRef, err := credentials.StoreRefFromCredential(fixture.credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Set(context.Background(), storeRef, []byte("test-secret")); err != nil {
		t.Fatal(err)
	}
	executor := &orderedSuiteExecutor{}
	reporter := &recordingReporter{generated: make(chan string, 2)}
	service, err := runs.New(runs.Dependencies{
		Repository: repository, Credentials: store, Executor: executor,
		Clock: &stepClock{next: fixture.now}, Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
		Reporter: reporter,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	runID, err := service.PrepareTarget(context.Background(), runs.StartCommand{PlanID: fixture.plan.ID, ModelID: fixture.model.ID, ChannelID: fixture.channel.ID})
	if err != nil {
		t.Fatalf("PrepareTarget() error = %v", err)
	}
	if err := service.CancelRun(context.Background(), runID); err != nil {
		t.Fatalf("CancelRun() error = %v", err)
	}
	waitForStatus(t, repository.fakeRepository, domain.RunCancelled)
	select {
	case reportedRunID := <-reporter.generated:
		if reportedRunID != runID {
			t.Fatalf("reported run id = %q, want %q", reportedRunID, runID)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation did not generate a report")
	}
	reporter.mu.Lock()
	reportCalls := reporter.calls
	reporter.mu.Unlock()
	executor.mu.Lock()
	executionCalls := len(executor.requests)
	executor.mu.Unlock()
	if reportCalls != 1 || executionCalls != 0 {
		t.Fatalf("report calls/execution calls = %d/%d, want 1/0", reportCalls, executionCalls)
	}
}

type multiSuiteRepository struct {
	*fakeRepository
	suites map[string]domain.Suite
}

func (repository *multiSuiteRepository) ListSuites(context.Context) ([]domain.Suite, error) {
	suites := make([]domain.Suite, 0, len(repository.suites))
	for _, suite := range repository.suites {
		suites = append(suites, suite)
	}
	return suites, nil
}

type orderedSuiteExecutor struct {
	mu        sync.Mutex
	requests  []runs.ExecutionRequest
	prompts   []string
	failFirst error
}

type cancelBlockingSuiteExecutor struct {
	mu      sync.Mutex
	entries []string
	entered chan string
}

func (executor *cancelBlockingSuiteExecutor) Execute(ctx context.Context, request runs.ExecutionRequest, _ func(runs.ResultDraft) error) error {
	executor.mu.Lock()
	executor.entries = append(executor.entries, request.Entry.EntryID)
	first := len(executor.entries) == 1
	executor.mu.Unlock()
	if first {
		executor.entered <- request.Entry.EntryID
	}
	<-ctx.Done()
	return ctx.Err()
}

func (executor *orderedSuiteExecutor) Execute(_ context.Context, request runs.ExecutionRequest, emit func(runs.ResultDraft) error) error {
	var prompt string
	if err := json.Unmarshal(request.Entry.CaseInputs[request.Cases[0].ID]["prompt"], &prompt); err != nil {
		return err
	}
	executor.mu.Lock()
	index := len(executor.requests)
	executor.requests = append(executor.requests, request)
	executor.prompts = append(executor.prompts, prompt)
	executor.mu.Unlock()
	if err := emit(runs.ResultDraft{
		CaseID: request.Cases[0].ID, RequestID: "request-1",
		ExecutionStatus: domain.ExecutionCompleted, Verification: passedVerification(),
		Metrics: map[string]float64{"e2e_ms": 10},
	}); err != nil {
		return err
	}
	if index == 0 {
		return executor.failFirst
	}
	return nil
}
