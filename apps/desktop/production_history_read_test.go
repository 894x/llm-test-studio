package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestProductionHistoryRefreshDoesNotInterruptLocalCasePersistence(t *testing.T) {
	verifyProductionHistoryReadDuringExecution(t)
}

func verifyProductionHistoryReadDuringExecution(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	notifyEntered := sync.OnceFunc(func() { close(entered) })
	releaseProvider := sync.OnceFunc(func() { close(release) })
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		notifyEntered()
		select {
		case <-release:
		case <-request.Context().Done():
			return
		}
		body := struct {
			Stream bool `json:"stream"`
		}{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body.Stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
			_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],"+
				"\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":2,\"total_tokens\":10}}\n\n")
			_, _ = io.WriteString(writer, "data: [DONE]\n\n")
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"id":"mock","object":"chat.completion","model":"kimi-k3",`+
			`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],`+
			`"usage":{"prompt_tokens":8,"completion_tokens":2,"total_tokens":10}}`)
	}))
	defer provider.Close()
	defer releaseProvider()
	root, executableRoot := t.TempDir(), t.TempDir()
	database := filepath.Join(root, "llm-test-studio", "llm-test-studio.db")
	diagnostics := []runs.Diagnostic{}
	var diagnosticMu sync.Mutex
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return root, nil }, appVersion: "history-read-test",
		executablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		credentialStore: credentials.NewMemoryStore(), protocolTransport: provider.Client().Transport,
		reportRunDiagnostic: func(diagnostic runs.Diagnostic) {
			diagnosticMu.Lock()
			defer diagnosticMu.Unlock()
			diagnostics = append(diagnostics, diagnostic)
		},
	})(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	catalog, err := dependencies.catalog.Snapshot(ctx, catalog.ScopeDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	var suiteID string
	for _, suite := range catalog.Suites {
		if suite.Key == "kimi-k3.official-foundation" {
			suiteID = suite.ID
		}
	}
	if suiteID == "" {
		t.Fatal("authored K3 suite unavailable")
	}
	accepted, err := dependencies.launcher.Launch(ctx, jobs.LaunchCommand{
		Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchQuickTask,
		QuickTask: &runs.QuickTaskCommand{SuiteID: suiteID, Model: "kimi-k3", BaseURL: provider.URL,
			APIKey: "history-read-fixture-secret", CaseConcurrency: 4, RequestTimeoutSeconds: 60,
			Inputs: map[string]json.RawMessage{}},
	})
	if err != nil || accepted.State != jobs.SubmissionAccepted {
		t.Fatalf("launch authored K3 suite: state=%s error=%v", accepted.State, err)
	}
	caseCount := 0
	for _, entry := range accepted.Prepared.Spec.Spec.Run.Snapshot.Entries {
		caseCount += len(entry.CaseDefinitions)
	}
	if caseCount != 84 || len(accepted.Receipt.RunIDs) != 1 {
		t.Fatalf("prepared case count=%d run count=%d", caseCount, len(accepted.Receipt.RunIDs))
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("local provider request did not start:", ctx.Err())
	}
	queryContext, stopQueries := context.WithCancel(ctx)
	queries := []func(context.Context) error{
		func(ctx context.Context) error { _, err := dependencies.query.Snapshot(ctx); return err },
		func(ctx context.Context) error { _, err := dependencies.reports.Snapshot(ctx); return err },
		func(ctx context.Context) error { _, err := dependencies.jobQuery.Jobs(ctx, "", 25); return err },
	}
	started, done := make(chan error, len(queries)), make(chan error, len(queries))
	for _, query := range queries {
		go func() {
			err := query(queryContext)
			started <- err
			if err != nil {
				done <- err
				return
			}
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-queryContext.Done():
					done <- nil
					return
				case <-ticker.C:
					if err := query(queryContext); err != nil {
						if queryContext.Err() != nil {
							err = nil
						}
						done <- err
						return
					}
				}
			}
		}()
	}
	joinQueries := sync.OnceFunc(func() {
		stopQueries()
		for range queries {
			if err := <-done; err != nil {
				t.Error("history refresh failed:", err)
			}
		}
	})
	defer joinQueries()
	for range queries {
		if err := <-started; err != nil {
			t.Fatal("initial concurrent history refresh failed:", err)
		}
	}
	// All real history snapshots have run while provider requests are held.
	// Their refresh loops remain active as results and terminal facts are written.
	releaseProvider()
	repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	jobID, runID := accepted.Prepared.Spec.JobID, accepted.Receipt.RunIDs[0]
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, err := repository.GetJob(ctx, jobID)
		if err != nil {
			t.Fatal(err)
		}
		if jobcontract.Terminal(job.Receipt.Status) {
			if job.Receipt.Status != domain.RunCompleted {
				t.Fatalf("Job status=%s failure=%+v", job.Receipt.Status, job.Failure)
			}
			pending, err := repository.RecoveryJobs(ctx)
			if err != nil {
				t.Fatal(err)
			}
			settled := true
			for _, candidate := range pending {
				if candidate.Receipt.JobID == jobID {
					settled = false
				}
			}
			if settled {
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("local execution did not settle: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	joinQueries()
	run, err := repository.GetRun(ctx, runID)
	if err != nil || run.Status() != domain.RunCompleted {
		t.Fatalf("Run status=%s error=%v", run.Status(), err)
	}
	results, err := repository.ListResults(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	summaries := 0
	for _, result := range results {
		if result.CaseID != "" && result.RequestID == "" {
			summaries++
		}
	}
	if summaries != 84 {
		t.Fatalf("persisted case summaries=%d, want 84", summaries)
	}
	reports, err := repository.ListReportsForRuns(ctx, []string{runID})
	if err != nil || len(reports) != 1 {
		t.Fatalf("persisted reports=%d error=%v", len(reports), err)
	}
	if err := dependencies.close(); err != nil {
		t.Fatal(err)
	}
	diagnosticMu.Lock()
	defer diagnosticMu.Unlock()
	for _, diagnostic := range diagnostics {
		persistenceFailure := diagnostic.ErrorCode == "result_persistence_failed" ||
			diagnostic.ErrorCode == "run_state_transition_failed"
		if diagnostic.RunID == runID && persistenceFailure {
			t.Errorf("persistence diagnostic operation=%s error=%v", diagnostic.Operation, diagnostic.Err)
		}
	}
	t.Logf("completed and settled: requests=%d case_summaries=%d reports=%d", requests.Load(), summaries, len(reports))
}
