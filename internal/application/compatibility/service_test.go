package compatibility_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/application/compatibility"
)

type panicHTTPDoer struct{}

func (panicHTTPDoer) Do(*http.Request) (*http.Response, error) {
	panic("dry-run must not perform an HTTP request")
}

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (do httpDoerFunc) Do(request *http.Request) (*http.Response, error) {
	return do(request)
}

func TestRunDryRunAcceptsHTTPAndEmitsVersionedLifecycle(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 1,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "protocol": "openai-chat",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "definition": {
    "schema_version": 1,
    "type": "openai-chat",
    "type_version": 1,
    "spec": {
      "inputs": {
        "payload": {
          "type": "object",
          "required": true
        }
      },
      "request": {
        "body": {
          "messages": [
            {
              "role": "user",
              "content": "hello"
            }
          ],
          "metadata": {
            "$input": "payload"
          }
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)

	var events []compatibility.Event
	var writtenDir string
	var writtenReport apiaudit.Report
	fixedTime := time.Date(2026, time.August, 30, 8, 9, 10, 0, time.FixedZone("test", 8*60*60))
	service := compatibility.New(compatibility.Dependencies{
		HTTPDoer: panicHTTPDoer{},
		Emit: func(event compatibility.Event) {
			events = append(events, event)
		},
		WriteReport: func(outputDir string, report apiaudit.Report) error {
			writtenDir = outputDir
			writtenReport = report
			return nil
		},
		Now: func() time.Time { return fixedTime },
	})

	outputDir := filepath.Join(t.TempDir(), "report")
	final, err := service.Run(context.Background(), compatibility.RunRequest{
		Inputs:       json.RawMessage(`{"payload": {"api_key": "sk-test-secret", "client_secret": "opaque-client-secret", "nested": {"refresh_token": "opaque-refresh-token"}}}`),
		Suite:        "openai-chat",
		CasesRoot:    casesRoot,
		BaseURL:      "http://gateway.example/v1",
		APIKey:       "sk-test-secret",
		Model:        "test-model",
		DryRun:       true,
		OutputDir:    outputDir,
		PollInterval: time.Second,
		Timeout:      time.Minute,
		Concurrency:  1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	plan, ok := events[0].(compatibility.PlanEvent)
	if !ok {
		t.Fatalf("first event type = %T, want PlanEvent", events[0])
	}
	if plan.SchemaVersion != compatibility.EventSchemaVersion || plan.Type != compatibility.EventTypePlan {
		t.Fatalf("plan metadata = version %d type %q", plan.SchemaVersion, plan.Type)
	}
	if plan.Payload.Total != 1 || len(plan.Payload.Runs) != 1 || plan.Payload.Runs[0].ID != "C001" || plan.Payload.Runs[0].Model != "test-model" {
		t.Fatalf("plan = %#v", plan)
	}

	progress, ok := events[1].(compatibility.ProgressEvent)
	if !ok {
		t.Fatalf("second event type = %T, want ProgressEvent", events[1])
	}
	if progress.SchemaVersion != compatibility.EventSchemaVersion || progress.Type != compatibility.EventTypeProgress {
		t.Fatalf("progress metadata = version %d type %q", progress.SchemaVersion, progress.Type)
	}
	if progress.Payload.Completed != 1 || progress.Payload.Total != 1 || progress.Payload.Result.Status != apiaudit.StatusUnknown {
		t.Fatalf("progress = %#v", progress)
	}
	if got := progress.Payload.Result.Exchanges[0].RequestBody["metadata"].(map[string]any)["api_key"]; got != "[REDACTED]" {
		t.Fatalf("progress api_key = %q, want redacted", got)
	}
	encodedProgress, err := json.Marshal(progress)
	if err != nil {
		t.Fatalf("Marshal(progress): %v", err)
	}
	for _, secret := range []string{"sk-test-secret", "opaque-client-secret", "opaque-refresh-token"} {
		if strings.Contains(string(encodedProgress), secret) {
			t.Fatalf("progress event contains %q: %s", secret, encodedProgress)
		}
	}

	finalEvent, ok := events[2].(compatibility.FinalEvent)
	if !ok {
		t.Fatalf("third event type = %T, want FinalEvent", events[2])
	}
	if finalEvent != final {
		t.Fatalf("emitted final = %#v, returned final = %#v", finalEvent, final)
	}
	if final.SchemaVersion != compatibility.EventSchemaVersion || final.Type != compatibility.EventTypeFinal {
		t.Fatalf("final metadata = version %d type %q", final.SchemaVersion, final.Type)
	}
	if final.Payload.Command != "run" || final.Payload.ReportDir != outputDir {
		t.Fatalf("final = %#v", final)
	}
	if writtenDir != outputDir || len(writtenReport.Results) != 1 {
		t.Fatalf("written report dir = %q, results = %d", writtenDir, len(writtenReport.Results))
	}
	if writtenReport.APIKey != "" {
		t.Fatalf("writer received API key %q", writtenReport.APIKey)
	}
	encodedReport, err := json.Marshal(writtenReport)
	if err != nil {
		t.Fatalf("Marshal(report): %v", err)
	}
	for _, secret := range []string{"sk-test-secret", "opaque-client-secret", "opaque-refresh-token"} {
		if strings.Contains(string(encodedReport), secret) {
			t.Fatalf("writer received %q: %s", secret, encodedReport)
		}
	}

	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	var runID string
	for index, event := range events {
		metadata := event.Metadata()
		if !uuidPattern.MatchString(metadata.EventID) {
			t.Fatalf("event %d event_id = %q", index, metadata.EventID)
		}
		if index == 0 {
			runID = metadata.RunID
		}
		if metadata.RunID != runID || !uuidPattern.MatchString(metadata.RunID) {
			t.Fatalf("event %d run_id = %q, want %q", index, metadata.RunID, runID)
		}
		if metadata.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, metadata.Sequence)
		}
		if !metadata.OccurredAt.Equal(fixedTime.UTC()) || metadata.OccurredAt.Location() != time.UTC {
			t.Fatalf("event %d occurred_at = %s", index, metadata.OccurredAt)
		}
		encoded, marshalErr := json.Marshal(event)
		if marshalErr != nil {
			t.Fatalf("Marshal(event %d): %v", index, marshalErr)
		}
		var envelope map[string]any
		if unmarshalErr := json.Unmarshal(encoded, &envelope); unmarshalErr != nil {
			t.Fatalf("Unmarshal(event %d): %v", index, unmarshalErr)
		}
		if _, ok := envelope["payload"].(map[string]any); !ok {
			t.Fatalf("event %d has no object payload: %s", index, encoded)
		}
	}
}

func TestRunDryRunSupportsWanVideoSuite(t *testing.T) {
	casesRoot := filepath.Clean(filepath.Join("..", "..", "..", "data", "cases"))
	var written apiaudit.Report
	service := compatibility.New(compatibility.Dependencies{
		HTTPDoer: panicHTTPDoer{}, Emit: func(compatibility.Event) {},
		WriteReport: func(_ string, report apiaudit.Report) error { written = report; return nil }, Now: time.Now,
	})
	_, err := service.Run(context.Background(), compatibility.RunRequest{
		Suite: "wan-video", CasesRoot: casesRoot, BaseURL: "https://workspace.example", Model: "wan2.6-t2v", AllCases: true,
		DryRun: true, OutputDir: t.TempDir(), PollInterval: time.Second, Timeout: time.Second, Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(written.Results) != 92 {
		t.Fatalf("results count = %d", len(written.Results))
	}
	for _, result := range written.Results {
		if result.Protocol != "wan-video" || result.Model != "wan2.6-t2v" || result.Exchanges[0].RequestBody["model"] != "wan2.6-t2v" {
			t.Fatalf("bound protocol result = %#v", result)
		}
	}
}

func TestRunDryRunSupportsMiniMaxVideoSuiteAndInjectsH3Model(t *testing.T) {
	casesRoot := t.TempDir()
	writeCase(t, casesRoot, "minimax-video", "H3001", `{
  "schema_version": 1,
  "key": "H3001",
  "name": "H3 minimum duration",
  "dimension": "boundary",
  "protocol": "minimax-video",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "definition": {
    "schema_version": 1,
    "type": "minimax-video",
    "type_version": 1,
    "spec": {
      "inputs": {},
      "request": {
        "body": {
          "content": [
            {
              "type": "text",
              "text": "cat"
            }
          ],
          "resolution": "768P",
          "duration": 4,
          "ratio": "16:9"
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        },
        {
          "id": "terminal",
          "source": "task",
          "pointer": "/status",
          "operator": "equals",
          "value": "succeeded"
        },
        {
          "id": "video",
          "source": "response",
          "pointer": "/task/content/url",
          "operator": "type",
          "value": "string"
        }
      ],
      "workflow": {
        "mode": "wait"
      }
    }
  }
}`)
	var written apiaudit.Report
	service := compatibility.New(compatibility.Dependencies{
		HTTPDoer: panicHTTPDoer{}, Emit: func(compatibility.Event) {},
		WriteReport: func(_ string, report apiaudit.Report) error { written = report; return nil }, Now: time.Now,
	})
	_, err := service.Run(context.Background(), compatibility.RunRequest{
		Suite: "minimax-video", CasesRoot: casesRoot, BaseURL: "https://api.minimax.cn", Model: "MiniMax-H3",
		DryRun: true, OutputDir: t.TempDir(), PollInterval: time.Second, Timeout: time.Second, Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(written.Results) != 1 || written.Results[0].Protocol != "minimax-video" {
		t.Fatalf("results = %#v", written.Results)
	}
	if got := written.Results[0].Exchanges[0].RequestBody["model"]; got != "MiniMax-H3" {
		t.Fatalf("request model = %#v, want MiniMax-H3", got)
	}
}

func TestListLoadsCasesThroughApplicationService(t *testing.T) {
	t.Parallel()

	casesRoot := t.TempDir()
	writeCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 1,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "protocol": "openai-chat",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "definition": {
    "schema_version": 1,
    "type": "openai-chat",
    "type_version": 1,
    "spec": {
      "inputs": {},
      "request": {
        "body": {}
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)

	cases, err := compatibility.New(compatibility.Dependencies{}).List(
		context.Background(),
		compatibility.ListRequest{Suite: "openai-chat", CasesRoot: casesRoot},
	)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(cases) != 1 || cases[0].ID != "C001" {
		t.Fatalf("List() = %#v", cases)
	}
}

func TestRunRejectsCredentialBearingBaseURL(t *testing.T) {
	t.Parallel()

	service := compatibility.New(compatibility.Dependencies{})
	for _, baseURL := range []string{
		"https://user:password@gateway.example",
		"https://gateway.example?api_key=secret",
	} {
		_, err := service.Run(context.Background(), compatibility.RunRequest{
			Suite: "openai-chat", CasesRoot: t.TempDir(), BaseURL: baseURL,
			Model: "test-model", DryRun: true, PollInterval: time.Second,
			Timeout: time.Second, Concurrency: 1,
		})
		if !compatibility.IsConfigError(err) {
			t.Fatalf("Run(%q) error = %v, want ConfigError", baseURL, err)
		}
	}
}

func TestRunPropagatesCallerCancellationToLiveRequest(t *testing.T) {
	casesRoot := t.TempDir()
	writeCase(t, casesRoot, "openai-chat", "C001", `{
  "schema_version": 1,
  "key": "C001",
  "name": "chat sync",
  "dimension": "protocol",
  "protocol": "openai-chat",
  "enabled": true,
  "default": true,
  "severity": "critical",
  "execution_mode": "automatic",
  "definition": {
    "schema_version": 1,
    "type": "openai-chat",
    "type_version": 1,
    "spec": {
      "inputs": {},
      "request": {
        "body": {
          "messages": [
            {
              "role": "user",
              "content": "hello"
            }
          ]
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 200
        }
      ]
    }
  }
}`)

	requestStarted := make(chan struct{})
	doer := httpDoerFunc(func(request *http.Request) (*http.Response, error) {
		close(requestStarted)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	reportWritten := false
	service := compatibility.New(compatibility.Dependencies{
		HTTPDoer: doer,
		WriteReport: func(string, apiaudit.Report) error {
			reportWritten = true
			return nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan error, 1)
	go func() {
		_, err := service.Run(ctx, compatibility.RunRequest{
			Suite:        "openai-chat",
			CasesRoot:    casesRoot,
			BaseURL:      "https://gateway.example",
			APIKey:       "sk-test-secret",
			Model:        "test-model",
			PollInterval: time.Second,
			Timeout:      time.Minute,
			Concurrency:  1,
		})
		result <- err
	}()

	select {
	case <-requestStarted:
		cancel()
	case err := <-result:
		t.Fatalf("Run() returned before starting HTTP request: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Run() did not start HTTP request")
	}

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not stop after caller cancellation")
	}
	if reportWritten {
		t.Fatal("report was written after caller cancellation")
	}
}

func writeCase(t *testing.T, root, suite, id, contents string) {
	t.Helper()
	directory := filepath.Join(root, suite, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", directory, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "case.json"), []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(case.json): %v", err)
	}
}
