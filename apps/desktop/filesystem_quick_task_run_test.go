package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/plancatalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/application/workspace"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestQuickSuiteExecutesMixedCasesOnceAndPersistsWithoutAuthoredTargets(t *testing.T) {
	for _, failRequests := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed_requests=%t", failRequests), func(t *testing.T) {
			testQuickSuitePersistence(t, failRequests)
		})
	}
}

func testQuickSuitePersistence(t *testing.T, failRequests bool) {
	t.Helper()
	ctx := context.Background()
	catalog, suite, first, casePath := filesystemQuickTaskFixture(t)
	plans, err := plancatalog.New(filepath.Join(filepath.Dir(catalog.lockPath), "plans"))
	if err != nil {
		t.Fatal(err)
	}
	catalog.plans = plans
	caseTypes := casetypes.MustBuiltinRegistry()
	probe := first.TestCase
	probe.ID, probe.Key = "63000000-0000-4000-8000-000000000031", "probe"
	for _, descriptor := range caseTypes.Descriptors() {
		if descriptor.Type == casetypes.TypeResponseProbe {
			probe.Definition.Type = descriptor.Type
			probe.Definition.Spec = descriptor.DefaultSpec
		}
	}
	third := first.TestCase
	third.ID, third.Key = "63000000-0000-4000-8000-000000000032", "third"
	ladder := first.TestCase
	ladder.ID, ladder.Key = "63000000-0000-4000-8000-000000000033", "ladder"
	ladder.Definition.Type, ladder.Definition.TypeVersion = casetypes.TypeInputLatencyLadder, 2
	ladder.Definition.Spec, _ = json.Marshal(casetypes.InputLatencyLadderSpec{
		Request: domain.TestRequest{Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"user","content":"hello"}]}`)},
		Stages:  []casetypes.InputLatencyStage{{InputTokens: 8}}, SamplesPerStep: 3, OutputTokens: 4, TimeoutMS: 1000, CacheMode: casetypes.CacheModeCold,
	})
	for _, testCase := range []domain.TestCase{probe, third, ladder} {
		if err := catalog.cases.SaveCase(ctx, string(testCase.Protocol), testCase.Key, testCase); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := catalog.cases.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := make(map[string]domain.TestCase)
	for _, entry := range entries {
		byKey[entry.TestCase.Key] = entry.TestCase
	}
	for _, key := range []string{probe.Key, third.Key, ladder.Key} {
		testCase := byKey[key]
		suite.Cases = append(suite.Cases, domain.CaseRevisionRef{CaseID: testCase.ID, Revision: testCase.Revision})
	}
	if err := catalog.suites.SaveSuite(ctx, string(suite.Protocol), suite.Key, suite); err != nil {
		t.Fatal(err)
	}
	suites, err := catalog.suites.Entries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	suite = suites[0].Suite
	originalCase, err := os.ReadFile(casePath)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var messages []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-temporary-key" {
			t.Error("missing temporary credential")
		}
		var body struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "arbitrary-model" || len(body.Messages) == 0 {
			t.Error("wrong upstream target or missing message")
		}
		mu.Lock()
		if len(body.Messages) > 0 {
			messages = append(messages, body.Messages[0].Content)
		}
		mu.Unlock()
		time.Sleep(15 * time.Millisecond)
		if failRequests {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"error":{"message":"private-provider-error","type":"authentication_error"}}`))
			return
		}
		if body.Stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"arbitrary-model","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()
	database := filepath.Join(t.TempDir(), "runs.db")
	if err := sqlite.Migrate(ctx, database, sqlite.MigrateOptions{AppVersion: "test"}); err != nil {
		t.Fatal(err)
	}
	operational, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer operational.Close()
	repository := filesystemRuntimeRepository{Repository: operational, catalog: catalog}
	reporter, err := reporting.NewGenerator(reporting.GeneratorDependencies{Repository: operational, Clock: productionClock{}})
	if err != nil {
		t.Fatal(err)
	}
	rememberedStore := credentials.NewMemoryStore()
	service, err := runs.New(runs.Dependencies{QuickTaskCredentials: rememberedStore, Repository: repository, QuickTasks: catalog, CaseTypes: caseTypes, Credentials: credentials.NewMemoryStore(),
		Reporter: reporter,
		Executor: runs.MustExecutorRouter(caseTypes, map[domain.CaseType]runs.Executor{casetypes.TypeRequestSingle: runs.NewLoadExecutor(server.Client().Transport), casetypes.TypeResponseProbe: runs.NewResponseProbeExecutor(server.Client().Transport), casetypes.TypeInputLatencyLadder: runs.NewInputLatencyLadderExecutor(server.Client().Transport)}),
		Clock:    productionClock{}, Environment: func() domain.EnvironmentSnapshot {
			return domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "test"}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	app := NewDesktopApp(nil, service)
	app.onStartup(ctx)
	defer app.shutdown()
	id, err := app.StartQuickTask(runs.QuickTaskCommand{SuiteID: suite.ID, SuiteRevision: suite.Revision, Model: "arbitrary-model", BaseURL: server.URL, APIKey: "test-temporary-key", Inputs: map[string]json.RawMessage{"prompt": json.RawMessage(`"edited"`)}})
	if err != nil {
		t.Fatal(err)
	}
	var stored domain.Run
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stored, err = operational.GetRun(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status() == domain.RunCompleted || stored.Status() == domain.RunFailed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if stored.Status() != domain.RunCompleted {
		t.Fatalf("task status=%s failure=%+v", stored.Status(), stored.Failure())
	}
	if err := app.RememberQuickTaskCredential(runs.RememberQuickTaskCredentialCommand{RunID: id, BaseURL: server.URL, Protocol: suite.Protocol, APIKey: "test-temporary-key"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(messages) != 6 || messages[0] != "edited" || messages[2] != "hi" {
		t.Errorf("Suite order or request count changed: %v", messages)
	}
	mu.Unlock()
	projections, err := operational.ListRunProjections(ctx)
	if err != nil || len(projections) != 1 {
		t.Fatalf("durable task projection count=%d error=%v", len(projections), err)
	}
	if projections[0].Completed != 6 || (projections[0].Failed > 0) != failRequests {
		t.Fatalf("durable task counts completed=%d failed=%d", projections[0].Completed, projections[0].Failed)
	}
	reports, err := operational.ListReportsForRuns(ctx, []string{id})
	if err != nil || len(reports) != 1 {
		t.Fatalf("quick report was not sealed: %v", err)
	}
	report := reports[0]
	if report.Conclusion.Passed == failRequests || report.PlanSnapshot.QuickTask == nil || len(report.CaseResults) != 4 {
		t.Fatalf("quick report was not sealed correctly: %v", err)
	}
	if elapsed := report.Metrics["elapsed_seconds"].Value; elapsed < 0.08 {
		t.Errorf("sequential task timeline reset between Cases: elapsed=%fs", elapsed)
	}
	var previousFinish float64
	for _, raw := range report.Timeline {
		var item struct {
			Started  float64 `json:"started_offset_ms"`
			Finished float64 `json:"finished_offset_ms"`
		}
		if err := json.Unmarshal(raw, &item); err != nil || item.Started < previousFinish || item.Finished <= item.Started {
			t.Fatalf("sequential request timeline overlaps: %s, %v", raw, err)
		}
		previousFinish = item.Finished
	}
	view, err := workspace.New(repository).Snapshot(ctx)
	if err != nil || len(view.Runs) != 1 || view.Runs[0].Completed != 6 || view.Runs[0].Planned != 0 || view.Runs[0].Source != "quick_task" {
		t.Fatalf("variable task history cannot be displayed: %+v, %v", view, err)
	}
	if err := operational.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if restored, err := reopened.GetReport(ctx, report.ID); err != nil || restored.PlanSnapshot.QuickTask == nil || restored.Conclusion.Passed == failRequests {
		t.Fatalf("task history did not survive reopening SQLite: %v", err)
	}
	replayService, err := runs.New(runs.Dependencies{QuickTaskCredentials: rememberedStore, Repository: filesystemRuntimeRepository{Repository: reopened, catalog: catalog}, QuickTasks: catalog, CaseTypes: caseTypes, Credentials: credentials.NewMemoryStore(), Executor: runs.NewLoadExecutor(server.Client().Transport), Clock: productionClock{}, Environment: func() domain.EnvironmentSnapshot { return stored.Snapshot().Environment }})
	if err != nil {
		t.Fatal(err)
	}
	defer replayService.Close()
	replayApp := NewDesktopApp(nil, replayService)
	replayApp.onStartup(ctx)
	defer replayApp.shutdown()
	history, err := replayApp.GetQuickTask(id)
	if err != nil || history.RunID != id || history.CredentialRunID != id || history.Model != "arbitrary-model" || string(history.Inputs["prompt"]) != `"edited"` {
		t.Fatalf("native history after reopening: %+v, %v", history, err)
	}
	historyJSON, _ := json.Marshal(history)
	if strings.Contains(string(historyJSON), "test-temporary-key") || strings.Contains(string(historyJSON), "case_definitions") {
		t.Fatal("native history leaked private data")
	}
	replayID, err := replayService.PrepareQuickTask(ctx, runs.QuickTaskCommand{SuiteID: history.Suite.ID, SuiteRevision: history.Suite.Revision, SourceRunID: id, Model: history.Model, BaseURL: history.BaseURL, CredentialRunID: history.CredentialRunID, Inputs: history.Inputs})
	if err != nil || replayID == id {
		t.Fatalf("replay after restart: %s, %v", replayID, err)
	}
	if err := replayService.CancelRun(ctx, replayID); err != nil {
		t.Fatal(err)
	}
	replayed, err := replayApp.GetQuickTask(replayID)
	if err != nil || replayed.CredentialRunID != id {
		t.Fatalf("replayed history lost remembered key reference: %+v, %v", replayed, err)
	}
	for range 2 {
		if err := replayApp.ForgetQuickTaskCredential(id); err != nil {
			t.Fatal(err)
		}
	}
	forgotten, err := replayApp.GetQuickTask(replayID)
	if err != nil || forgotten.CredentialRunID != "" || forgotten.Model != replayed.Model {
		t.Fatalf("forget did not preserve history without key: %+v, %v", forgotten, err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(encoded), "test-temporary-key") || stored.Snapshot().QuickTask == nil {
		t.Fatalf("invalid history snapshot: %v", err)
	}
	currentCase, err := os.ReadFile(casePath)
	if err != nil || string(currentCase) != string(originalCase) {
		t.Fatal("running changed authored Case")
	}
	for _, name := range []string{"models.json", "channels.json", "plans"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(catalog.lockPath), name)); !os.IsNotExist(err) {
			t.Fatalf("quick run created authored %s", name)
		}
	}
}
