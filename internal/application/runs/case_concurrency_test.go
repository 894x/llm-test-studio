package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func concurrentSuiteFixture(t *testing.T, count int) (runFixture, map[string]domain.TestCase) {
	t.Helper()
	fixture := newRunFixture(t)
	fixture.suite.Cases = []domain.CaseRef{}
	cases := make(map[string]domain.TestCase, count)
	for index := range count {
		testCase := fixture.testCase
		testCase.ID = fmt.Sprintf("31000000-0000-4000-8000-%012d", index+1)
		testCase.Key = fmt.Sprintf("case-%d", index+1)
		testCase.Definition.Spec = json.RawMessage(fmt.Sprintf(
			`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"case-%d"}]}},
			"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`,
			index+1,
		))
		cases[testCase.ID] = testCase
		fixture.suite.Cases = append(fixture.suite.Cases, domain.CaseRef{CaseID: testCase.ID})
	}
	return fixture, cases
}

func TestCaseConcurrencyPreparationFreezesLimitsWithoutChangingCatalog(t *testing.T) {
	for _, requested := range []uint32{0, 1, 2, 4, 8, 9} {
		for _, quick := range []bool{false, true} {
			t.Run(fmt.Sprintf("requested=%d/quick=%t", requested, quick), func(t *testing.T) {
				fixture, cases := concurrentSuiteFixture(t, 6)
				// The second entry's load policy must retain its own concurrency.
				loaded := fixture.plan.Entries[0]
				loaded.EntryID = "31000000-0000-4000-8000-000000000021"
				loaded.Load.Mode = domain.LoadFixedConcurrency
				loaded.Load.Concurrency, loaded.Load.RequestCount = 3, 12
				fixture.plan.Entries = append(fixture.plan.Entries, loaded)
				repository := &fakeRepository{fixture: fixture, testCases: cases}
				store := credentials.NewMemoryStore()
				ref, err := credentials.StoreRefFromCredential(fixture.credential)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Set(context.Background(), ref, []byte("test-secret")); err != nil {
					t.Fatal(err)
				}
				service, err := runs.New(runs.Dependencies{
					Repository: repository, QuickTasks: quickTaskCatalog{suite: fixture.suite},
					Credentials: store, Executor: &recordingExecutor{},
					Clock:       &stepClock{next: fixture.now},
					Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
				})
				if err != nil {
					t.Fatal(err)
				}
				defer service.Close()
				var id string
				if quick {
					id, err = service.PrepareQuickTask(context.Background(), runs.QuickTaskCommand{
						SuiteID: fixture.suite.ID, CaseConcurrency: requested,
						Model: "model", BaseURL: "https://example.test", APIKey: "test-secret",
					})
				} else {
					id, err = service.PrepareTarget(context.Background(), runs.StartCommand{
						PlanID: fixture.plan.ID, ModelID: fixture.model.ID,
						ChannelID: fixture.channel.ID, CaseConcurrency: requested,
					})
				}
				if requested == 9 {
					if !errors.Is(err, runs.ErrInvalid) || repository.run.Meta().ID != "" {
						t.Fatalf("invalid concurrency persisted a run: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				expected := requested
				if expected == 0 {
					expected = 4
				}
				expected = min(expected, 6)
				snapshot := repository.run.Snapshot()
				entryLimit := snapshot.Entries[0].Load.Concurrency
				planLimit := snapshot.PlanDocument.Entries[0].Load.Concurrency
				if entryLimit != expected || planLimit != expected {
					t.Fatalf("effective concurrency not frozen: %+v", snapshot.Entries[0].Load)
				}
				if repository.fixture.plan.Entries[0].Load.Concurrency != 1 {
					t.Fatal("starting a run mutated its catalog Plan")
				}
				request := runs.ExecutionRequest{
					Entry: snapshot.Entries[0], Cases: snapshot.Entries[0].CaseDefinitions,
				}
				profile := request.LoadProfile()
				correctCount := profile.RequestCount == 6
				correctLimit := profile.Concurrency == expected
				correctMode := profile.Mode == domain.LoadFixedConcurrency
				if !correctCount || !correctLimit || !correctMode {
					t.Fatalf("single-pass schedule = %+v", profile)
				}
				if quick {
					history, err := service.QuickTask(context.Background(), id)
					if err != nil || history.CaseConcurrency != expected {
						t.Fatalf("history lost concurrency: %+v, %v", history, err)
					}
				} else if snapshot.Entries[1].Load != loaded.Load {
					t.Fatal("case concurrency changed a load-test entry")
				}
				raw, err := json.Marshal(repository.run)
				if err != nil {
					t.Fatal(err)
				}
				var restored domain.Run
				if err := json.Unmarshal(raw, &restored); err != nil {
					t.Fatalf("snapshot round trip lost concurrency: %v", err)
				}
				if restored.Snapshot().Entries[0].Load.Concurrency != expected {
					t.Fatal("snapshot round trip lost concurrency")
				}
			})
		}
	}
}

func TestProtocolCaseConcurrencyExecutesEachWorkflowOnceAndKeepsStepsOrdered(t *testing.T) {
	for _, limit := range []uint32{1, 2, 4, 8} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			fixture, cases := concurrentSuiteFixture(t, 9)
			for id, testCase := range cases {
				var spec map[string]any
				if err := json.Unmarshal(testCase.Definition.Spec, &spec); err != nil {
					t.Fatal(err)
				}
				spec["workflow"] = map[string]any{
					"mode": "sequence",
					"steps": []any{map[string]any{
						"id": "followup", "request": map[string]any{"body": map[string]any{
							"messages": []any{
								map[string]any{"$response": "initial", "pointer": "/choices/0/message"},
								map[string]any{"role": "user", "content": "Next"},
							},
						}},
					}},
				}
				raw, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				testCase.Definition.Spec = raw
				cases[id] = testCase
			}
			request := concurrentExecutionRequest(t, fixture, cases, limit)
			entered := make(chan struct{}, 32)
			release := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var mu sync.Mutex
			stages := map[string]int{}
			var inFlight, peak int
			executor := runs.NewProtocolExecutor(protocolRoundTrip(func(r *http.Request) (*http.Response, error) {
				var body struct {
					Messages []struct{ Content string } `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return nil, err
				}
				key := body.Messages[0].Content
				mu.Lock()
				inFlight++
				peak = max(peak, inFlight)
				stage := stages[key]
				stages[key]++
				mu.Unlock()
				defer func() { mu.Lock(); inFlight--; mu.Unlock() }()
				if stage == 0 {
					if len(body.Messages) != 1 {
						t.Error("followup executed before initial response")
					}
					entered <- struct{}{}
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
				} else if stage != 1 || len(body.Messages) != 2 || body.Messages[1].Content != "Next" {
					t.Error("workflow repeated or lost response dependency")
				}
				payload := fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%q}}]}`, key)
				return &http.Response{
					StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(payload)),
				}, nil
			}))
			drafts := []runs.ResultDraft{}
			done := make(chan error, 1)
			go func() {
				done <- executor.Execute(ctx, request, func(draft runs.ResultDraft) error {
					drafts = append(drafts, draft)
					return nil
				})
			}()
			waitForExecutions(
				t,
				ctx,
				entered,
				int(limit),
			)
			close(release)
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("executor did not drain")
			}
			allCasesExecuted := len(drafts) == 9
			limitReached := peak == int(limit)
			uniqueCasesExecuted := len(stages) == 9
			if !allCasesExecuted || !limitReached || !uniqueCasesExecuted {
				t.Fatalf(
					"workflows=%d peak=%d cases=%d",
					len(drafts),
					peak,
					len(stages),
				)
			}
			seen := map[string]bool{}
			for index, draft := range drafts {
				if seen[draft.CaseID] || len(draft.Observation.Exchanges) != 2 {
					t.Fatal("Case executed more than once or lost workflow exchanges")
				}
				seen[draft.CaseID] = true
				if limit == 1 && draft.CaseID != request.Cases[index].ID {
					t.Fatal("sequential policy changed Suite order")
				}
			}
		})
	}
}

func TestProtocolCaseConcurrencyStopDrainsAndCancelAbortsActiveCases(t *testing.T) {
	for _, abort := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", abort), func(t *testing.T) {
			fixture, cases := concurrentSuiteFixture(t, 9)
			request := concurrentExecutionRequest(t, fixture, cases, 4)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stop, release := make(chan struct{}), make(chan struct{})
			request.StopSending = stop
			entered := make(chan struct{}, 9)
			executor := runs.NewProtocolExecutor(protocolRoundTrip(func(r *http.Request) (*http.Response, error) {
				entered <- struct{}{}
				select {
				case <-release:
					return &http.Response{
						StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
						Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`)),
					}, nil
				case <-r.Context().Done():
					return nil, r.Context().Err()
				}
			}))
			drafts := []runs.ResultDraft{}
			done := make(chan error, 1)
			go func() {
				done <- executor.Execute(ctx, request, func(draft runs.ResultDraft) error {
					drafts = append(drafts, draft)
					return nil
				})
			}()
			waitForExecutions(
				t,
				ctx,
				entered,
				4,
			)
			if abort {
				cancel()
			} else {
				close(stop)
				close(release)
			}
			select {
			case err := <-done:
				var expectedError error
				if abort {
					expectedError = context.Canceled
				}
				if !errors.Is(err, expectedError) {
					t.Fatalf("stop result = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("active cases did not terminate")
			}
			if len(drafts) != 4 || len(entered) != 0 {
				t.Fatalf("launched additional cases after stop: drafts=%d queued=%d", len(drafts), len(entered))
			}
		})
	}
}

func concurrentExecutionRequest(
	t *testing.T,
	fixture runFixture,
	cases map[string]domain.TestCase,
	limit uint32,
) runs.ExecutionRequest {
	t.Helper()
	repository := &fakeRepository{fixture: fixture, testCases: cases}
	service, err := runs.New(runs.Dependencies{
		Repository:  repository,
		QuickTasks:  quickTaskCatalog{suite: fixture.suite},
		Credentials: credentials.NewMemoryStore(), Executor: &recordingExecutor{},
		Clock:       &stepClock{next: fixture.now},
		Environment: func() domain.EnvironmentSnapshot { return fixture.environment },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	id, err := service.PrepareQuickTask(context.Background(), runs.QuickTaskCommand{
		SuiteID: fixture.suite.ID, CaseConcurrency: limit,
		Model: "model", BaseURL: "https://example.test", APIKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	history, err := service.QuickTask(context.Background(), id)
	if err != nil || history.CaseConcurrency != limit {
		t.Fatalf("invalid prepared request: %v", err)
	}
	// A separate lease keeps this direct executor test independent of activation.
	lease, err := credentials.NewTemporaryLease([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	return runs.ExecutionRequest{
		Run: repository.run, Entry: repository.run.Snapshot().Entries[0],
		Cases: repository.run.Snapshot().Entries[0].CaseDefinitions, Credential: lease,
	}
}

func waitForExecutions(t *testing.T, ctx context.Context, entered <-chan struct{}, count int) {
	t.Helper()
	for range count {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("configured concurrency was not reached")
		}
	}
}
