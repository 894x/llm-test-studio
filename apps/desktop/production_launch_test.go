package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestProductionLauncherUsesAuthoredQuickTaskAndMinimalPerformance(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		document, _ := io.ReadAll(request.Body)
		defer clear(document)
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(document, &body)
		if body.Stream {
			writer.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n")
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
	root, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir: func() (string, error) { return root, nil }, appVersion: "launch-production-test",
		executablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		credentialStore: credentials.NewMemoryStore(), protocolTransport: provider.Client().Transport,
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	if dependencies.launcher == nil {
		t.Fatal("production job launcher was not wired")
	}
	catalog, err := dependencies.catalog.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	suiteID := ""
	for _, suite := range catalog.Suites {
		if suite.Key == "kimi-k3.official-foundation" {
			suiteID = suite.ID
		}
	}
	if suiteID == "" {
		t.Fatal("authored K3 suite unavailable")
	}
	database := filepath.Join(root, "llm-test-studio", "llm-test-studio.db")
	repository, err := sqlite.OpenRepository(t.Context(), database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for _, kind := range []jobs.LaunchKind{jobs.LaunchQuickTask, jobs.LaunchPerformance} {
		t.Run(string(kind), func(t *testing.T) {
			command := jobs.LaunchCommand{Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: kind}
			if kind == jobs.LaunchQuickTask {
				command.QuickTask = &runs.QuickTaskCommand{SuiteID: suiteID, Model: "kimi-k3",
					BaseURL: provider.URL, APIKey: "launch-test-only-secret", CaseConcurrency: 1,
					RequestTimeoutMS: 3000, Inputs: map[string]json.RawMessage{}}
			} else {
				command.Performance = &quicktest.PerformanceCommand{
					Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
					URL: provider.URL, ModelID: "kimi-k3", APIKey: "launch-test-only-secret",
					RequestCount: 3, Concurrency: 1, TimeoutMS: 3000, InputTokens: 32, OutputTokens: 128,
				}
			}
			before := requests.Load()
			accepted, err := dependencies.launcher.Launch(t.Context(), command)
			if err != nil || accepted.State != jobs.SubmissionAccepted {
				t.Fatalf("production launch: %v", err)
			}
			document, err := json.Marshal(accepted)
			if err != nil || bytes.Contains(document, []byte("launch-test-only-secret")) {
				t.Fatal("temporary preparation secret entered the submission outbox")
			}
			if kind == jobs.LaunchQuickTask {
				count := 0
				for _, entry := range accepted.Prepared.Spec.Spec.Run.Snapshot.Entries {
					count += len(entry.CaseDefinitions)
				}
				if count != 84 {
					t.Fatal("launcher omitted authored K3 cases")
				}
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				pending, err := repository.RecoveryJobs(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("production launch did not finish settlement")
				}
				time.Sleep(10 * time.Millisecond)
			}
			job, err := repository.GetJob(t.Context(), accepted.Prepared.Spec.JobID)
			if err != nil || job.Receipt.Status != domain.RunCompleted {
				t.Fatalf("production launch did not complete: %v", err)
			}
			if kind == jobs.LaunchPerformance {
				result, err := repository.ReadPerformanceResult(t.Context(), accepted.Prepared.Spec.JobID)
				if err != nil || result.Result.ObservationState != jobcontract.PerformanceAvailable || requests.Load()-before != 3 {
					t.Fatal("minimum production performance did not preserve three measured requests")
				}
			} else {
				reports, err := repository.ListReportsForRuns(t.Context(), accepted.Receipt.RunIDs)
				if err != nil || len(reports) != 1 || requests.Load() <= before {
					t.Fatal("authored production quick task did not produce its original report")
				}
			}
		})
	}
}
