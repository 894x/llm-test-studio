package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// Exercises the production composition, authored catalogs, scheduler, HTTPS
// protocol client, operational database and desktop report/export bindings.
func TestProtocolDesktopEndToEnd(t *testing.T) {
	testProtocolDesktopEndToEnd(t, false)
}

func TestProtocolDesktopTransportFailureEndToEnd(t *testing.T) {
	testProtocolDesktopEndToEnd(t, true)
}

type failedProtocolTransport struct{}

func (failedProtocolTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("fixture transport unavailable")
}

func testProtocolDesktopEndToEnd(t *testing.T, transportFailure bool) {
	var requests atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer desktop-e2e-secret" {
			t.Error("missing credential binding")
		}
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if r.Method == http.MethodPost {
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["model"] != "e2e-upstream" {
				t.Error("missing upstream model")
			}
		}
		if body["temperature"] == "invalid" {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"desktop-e2e-secret","type":"invalid_parameter"}}`)
			return
		}
		if r.URL.Path == "/v1/chat/completions" {
			if body["stream"] == true {
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"id\":\"e2e\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}]}\n\n")
				w.(http.Flusher).Flush()
				time.Sleep(5 * time.Millisecond)
				io.WriteString(w, "data: {\"id\":\"e2e\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14,\"prompt_tokens_details\":{\"cached_tokens\":3}}}\n\ndata: [DONE]\n\n")
				return
			}
			io.WriteString(w, `{"id":"e2e","choices":[{"message":{"content":"Hello world"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`)
			return
		}
		if r.Method == http.MethodPost {
			io.WriteString(w, `{"id":"e2e-task","task_id":"e2e-task","output":{"task_id":"e2e-task"}}`)
			return
		}
		io.WriteString(w, `{"id":"e2e-task","status":"succeeded","content":{"video_url":"https://example.test/video?signature=private-video-token"},"task":{"id":"e2e-task","status":"succeeded","content":{"url":"https://example.test/video?signature=private-video-token"}},"output":{"task_id":"e2e-task","task_status":"SUCCEEDED","video_url":"https://example.test/video?signature=private-video-token"}}`)
	}))
	defer server.Close()
	configurationRoot, executableRoot := t.TempDir(), t.TempDir()
	transport := server.Client().Transport
	if transportFailure {
		transport = failedProtocolTransport{}
	}
	options := productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(executableRoot, "app.exe"), nil },
		appVersion:     "protocol-e2e", caseBundle: fstest.MapFS{}, suiteBundle: fstest.MapFS{},
		credentialStore: credentials.NewMemoryStore(), protocolTransport: transport,
	}
	app := newDesktopApp(newProductionInitializer(options))
	app.onStartup(context.Background())
	defer app.shutdown()
	for _, protocol := range []domain.Protocol{domain.ProtocolOpenAIChat, domain.ProtocolSeedance, domain.ProtocolWanVideo, domain.ProtocolMiniMaxVideo} {
		t.Run(string(protocol), func(t *testing.T) {
			before := requests.Load()
			modelSnapshot, err := app.CreateModel(catalog.CreateModelCommand{Name: "E2E " + string(protocol), Protocols: []domain.Protocol{protocol}, Capabilities: []string{}})
			if err != nil {
				t.Fatal(err)
			}
			var modelID string
			for _, item := range modelSnapshot.Models {
				if item.Protocols[0] == protocol {
					modelID = item.ID
				}
			}
			channels, err := app.CreateChannel(catalog.CreateChannelCommand{Name: "Local HTTPS " + string(protocol), BaseURL: server.URL, APIKey: "desktop-e2e-secret", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			var channelID string
			for _, item := range channels.Channels {
				if item.Name == "Local HTTPS "+string(protocol) {
					channelID = item.ID
				}
			}
			if _, err = app.CreateChannelModel(catalog.CreateChannelModelCommand{Protocols: []domain.Protocol{protocol}, ChannelID: channelID, ModelID: modelID, UpstreamModelName: "e2e-upstream"}); err != nil {
				t.Fatal(err)
			}
			specs := []string{`{"inputs":{},"request":{"body":{}},"workflow":{"mode":"wait"},"assertions":[{"id":"terminal","source":"task","pointer":"/terminal","operator":"equals","value":true},{"id":"video","source":"response","pointer":"/content/video_url","operator":"http_url"}]}`}
			if protocol == domain.ProtocolWanVideo {
				specs[0] = strings.ReplaceAll(specs[0], "/content/video_url", "/output/video_url")
			}
			if protocol == domain.ProtocolMiniMaxVideo {
				specs[0] = strings.ReplaceAll(specs[0], "/content/video_url", "/task/content/url")
			}
			if protocol == domain.ProtocolOpenAIChat {
				specs = []string{
					`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"Hello"}],"stream":true}},"assertions":[{"id":"done","source":"stream.completed","operator":"equals","value":true}]}`,
					`{"inputs":{},"request":{"body":{"temperature":"invalid"}},"assertions":[{"id":"negative","source":"http.status","operator":"equals","value":400}]}`,
					`{"inputs":{},"request":{"body":{}},"assertions":[]}`,
					`{"inputs":{},"request":{"body":{}},"assertions":[{"id":"deliberate_failure","source":"http.status","operator":"equals","value":418}]}`,
				}
			}
			caseIDs := []string{}
			for index, spec := range specs {
				key := fmt.Sprintf("e2e.%s.%d", protocol, index)
				snapshot, err := app.CreateTestCase(catalog.CreateTestCaseCommand{Key: key, Name: key, Dimension: "e2e", Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic, Definitions: domain.ProtocolDefinitions{protocol: json.RawMessage(spec)}})
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range snapshot.TestCases {
					if item.Key == key {
						caseIDs = append(caseIDs, item.ID)
					}
				}
			}
			members := []catalog.CaseInput{}
			for _, id := range caseIDs {
				members = append(members, catalog.CaseInput{CaseID: id})
			}
			suites, err := app.CreateSuite(catalog.CreateSuiteCommand{Key: "e2e." + string(protocol), Name: "E2E suite " + string(protocol), Protocol: protocol, Cases: members})
			if err != nil {
				t.Fatal(err)
			}
			var suiteID string
			for _, item := range suites.Suites {
				if item.Protocol == protocol {
					suiteID = item.ID
				}
			}
			entry := catalog.PlanEntryInput{TargetKind: domain.PlanTargetSuite, TargetID: suiteID, LoadMode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 3000, WarmupCount: 2, Settings: testspec.RunSettings{PollIntervalMS: 1, TaskTimeoutMS: 1000}, Parameters: map[string]json.RawMessage{}, SLAThresholds: map[string]float64{}}
			entries := []catalog.PlanEntryInput{entry}
			if protocol == domain.ProtocolOpenAIChat {
				next := entry
				next.TargetKind = domain.PlanTargetCase
				next.TargetID = caseIDs[1]
				next.WarmupCount = 0
				entries = append(entries, next)
			}
			plans, err := app.CreatePlan(catalog.CreatePlanCommand{Name: "E2E " + string(protocol), Protocol: protocol, Seed: 42, Entries: entries})
			if err != nil {
				t.Fatal(err)
			}
			var planID string
			for _, item := range plans.Plans {
				if item.Protocol == protocol {
					planID = item.ID
				}
			}
			if _, err = app.StartRunTarget(runs.StartCommand{PlanID: planID, ModelID: modelID, ChannelID: channelID}); err != nil {
				t.Fatal(err)
			}
			var summary reporting.Summary
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				list, err := app.GetReports()
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range list.Reports {
					if item.PlanName == "E2E "+string(protocol) {
						summary = item
					}
				}
				if summary.ID != "" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if summary.ID == "" {
				t.Fatal("run did not persist its report")
			}
			detail, err := app.GetReportDetail(summary.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Entries) != len(entries) || detail.Entries[0].WarmupCount != 2 {
				t.Fatal("entry settings lost")
			}
			wantRequests := 3
			if protocol == domain.ProtocolOpenAIChat {
				wantRequests = 7
			}
			if len(detail.RequestResults) != wantRequests {
				t.Fatalf("request results = %d, want %d", len(detail.RequestResults), wantRequests)
			}
			warmup := 0
			for _, result := range detail.RequestResults {
				if result.Dimensions["phase"] == "warmup" {
					warmup++
				}
			}
			if warmup != 2 {
				t.Fatalf("warmups = %d", warmup)
			}
			if transportFailure {
				if requests.Load() != 0 || summary.Passed || summary.PassedCaseCount != 0 || summary.FailedCaseCount != 0 {
					t.Fatalf("transport failure fabricated verification: %+v", summary)
				}
				wantIndeterminate, wantObserved := uint64(1), uint64(0)
				if protocol == domain.ProtocolOpenAIChat {
					wantIndeterminate, wantObserved = 4, 1
				}
				if summary.IndeterminateCaseCount != wantIndeterminate || summary.ObservedCaseCount != wantObserved {
					t.Fatalf("transport failure verification counts: %+v", summary)
				}
				for _, result := range detail.RequestResults {
					if result.ExecutionStatus != domain.ExecutionFailed {
						t.Fatalf("transport failure execution = %s", result.ExecutionStatus)
					}
				}
			} else if protocol == domain.ProtocolOpenAIChat {
				if summary.PassedCaseCount != 3 || summary.FailedCaseCount != 1 || summary.ObservedCaseCount != 1 {
					t.Fatalf("summary = %+v", summary)
				}
				if detail.Entries[1].Verification.Passed != 1 {
					t.Fatal("failed suite prevented next entry")
				}
				if requests.Load()-before != 7 {
					t.Fatalf("outbound requests = %d", requests.Load()-before)
				}
			} else if requests.Load()-before != 6 || summary.PassedCaseCount != 1 {
				t.Fatalf("task execution count/summary: %d %+v", requests.Load()-before, summary)
			}
			for _, format := range []string{"json", "html", "png", "pdf"} {
				document, err := app.ExportReport(summary.ID, format, "E2E", "zh-CN")
				if err != nil {
					t.Fatal(err)
				}
				raw, err := base64.StdEncoding.DecodeString(document.DataBase64)
				if err != nil || len(raw) == 0 {
					t.Fatalf("empty %s export", format)
				}
				if bytes.Contains(raw, []byte("desktop-e2e-secret")) || bytes.Contains(raw, []byte("private-video-token")) {
					t.Fatalf("credential in %s export", format)
				}
			}
			if directory := os.Getenv("LLM_TEST_E2E_ARTIFACTS"); directory != "" && !transportFailure {
				if !filepath.IsAbs(directory) {
					t.Fatal("artifact directory must be absolute")
				}
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.MarshalIndent(detail, "", "  ")
				if strings.Contains(string(raw), "desktop-e2e-secret") {
					t.Fatal("secret in report")
				}
				if err := os.WriteFile(filepath.Join(directory, string(protocol)+".json"), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	app.shutdown()
	reloaded := newDesktopApp(newProductionInitializer(options))
	reloaded.onStartup(context.Background())
	if _, err := reloaded.GetWorkspace(); err != nil {
		t.Fatalf("reopened workspace: %v", err)
	}
	if _, err := reloaded.GetCatalog(); err != nil {
		t.Fatalf("reopened catalog: %v", err)
	}
	if _, err := reloaded.GetReports(); err != nil {
		t.Fatalf("reopened reports: %v", err)
	}
	reloaded.shutdown()
	if directory := os.Getenv("LLM_TEST_E2E_ARTIFACTS"); directory != "" && !transportFailure {
		output, err := os.MkdirTemp(directory, "workspace-")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(filepath.Join(output, "data"), os.DirFS(filepath.Join(executableRoot, "data"))); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(output, "llm-test-studio.db"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("Browser acceptance workspace: %s", output)
	}

}
