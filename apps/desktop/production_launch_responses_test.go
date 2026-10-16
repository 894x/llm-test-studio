package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
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

func TestProductionLauncherSettlesAuthoredResponsesAndThenPerformance(t *testing.T) {
	var requests atomic.Int64
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		document, _ := io.ReadAll(request.Body)
		defer clear(document)
		var body struct {
			Stream bool `json:"stream"`
		}
		_ = json.Unmarshal(document, &body)
		if strings.Contains(request.URL.Path, "/responses") {
			response := `{"id":"resp_mock","object":"response","created_at":1,"status":"completed","model":"kimi-k3","output":[{"id":"msg_mock","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"ok","annotations":[]}]}],"usage":{"input_tokens":8,"output_tokens":2,"total_tokens":10}}`
			if body.Stream {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(writer, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"ok\"}\n\n")
				_, _ = io.WriteString(writer, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":"+response+"}\n\n")
			} else {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(writer, response)
			}
			return
		}
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
		if suite.Key == "openai-responses.kimi-k3.official-foundation" {
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
			startedAt := time.Now()
			terminalAt := time.Time{}
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
				if count != 230 {
					t.Fatal("launcher omitted authored K3 cases")
				}
			}
			deadline := time.Now().Add(90 * time.Second)
			for {
				pending, err := repository.RecoveryJobs(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				var current *jobcontract.Snapshot
				for index := range pending {
					if pending[index].Receipt.JobID == accepted.Prepared.Spec.JobID {
						current = &pending[index]
						break
					}
				}
				if current == nil {
					break
				}
				if jobcontract.Terminal(current.Receipt.Status) && terminalAt.IsZero() {
					terminalAt = time.Now()
					t.Logf("terminal_elapsed=%s status=%s", time.Since(startedAt), current.Receipt.Status)
				}
				if time.Now().After(deadline) {
					state, _ := json.Marshal(current)
					t.Fatalf("production launch did not finish settlement: current=%s requests=%d", state, requests.Load()-before)
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !terminalAt.IsZero() {
				t.Logf("settled_elapsed=%s terminal_to_settled=%s requests=%d", time.Since(startedAt), time.Since(terminalAt), requests.Load()-before)
			}
			job, err := repository.GetJob(t.Context(), accepted.Prepared.Spec.JobID)
			if err != nil || job.Receipt.Status != domain.RunCompleted {
				t.Fatalf("production launch did not complete: %v, failure=%+v", err, job.Failure)
			}
			runner := accepted.Prepared.Runner
			expectedOrigin := domain.ExecutionOrigin{Kind: "local", NodeID: runner.NodeID,
				InstallationID: runner.NodeInstallationID, EngineBuildID: runner.EngineBuildID,
				RunnerVersion: "launch-production-test", OS: runtime.GOOS, Arch: runtime.GOARCH}
			if job.Receipt.Origin != expectedOrigin {
				t.Fatal("settled receipt does not identify the current Core executor")
			}
			if kind == jobs.LaunchPerformance {
				result, err := repository.ReadPerformanceResult(t.Context(), accepted.Prepared.Spec.JobID)
				if err != nil || result.Result.ObservationState != jobcontract.PerformanceAvailable || requests.Load()-before != 3 {
					t.Fatal("minimum production performance did not preserve three measured requests")
				}
				if result.Result.Terminal.Receipt.Origin != expectedOrigin {
					t.Fatal("performance terminal source changed")
				}
				archive, err := repository.GetQuickPerformanceReport(t.Context(), accepted.Prepared.Spec.Spec.Performance.ReportID)
				if err != nil || archive.ExecutionOrigin == nil || *archive.ExecutionOrigin != expectedOrigin {
					t.Fatal("performance archive lost its actual executor source")
				}
			} else {
				reports, err := repository.ListReportsForRuns(t.Context(), accepted.Receipt.RunIDs)
				if err != nil || len(reports) != 1 || requests.Load()-before < 230 {
					t.Fatal("authored production quick task did not produce its original report")
				}
				if len(reports[0].CaseResults) != 230 || reports[0].ExecutionOrigin == nil || *reports[0].ExecutionOrigin != expectedOrigin {
					t.Fatal("Responses report lost authored cases or actual executor source")
				}
				actualOrigin, err := repository.RunExecutionOrigin(t.Context(), accepted.Receipt.RunIDs[0])
				if err != nil || actualOrigin == nil || *actualOrigin != expectedOrigin {
					t.Fatal("persisted run source changed during settlement")
				}
			}
		})
	}
	next, _ := productionLocalPerformance(t, dependencies, provider.URL)
	if !next.Prepared.Runner.Ready || !next.Prepared.Runner.Healthy {
		t.Fatal("successful Responses and performance settlement isolated the application owner")
	}
}
