package apiaudit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocol"
)

type registryDoer func(*http.Request) (*http.Response, error)

func (do registryDoer) Do(request *http.Request) (*http.Response, error) { return do(request) }

func TestRunCaseRejectsInvalidDispatchBeforeHTTP(t *testing.T) {
	for _, input := range []struct{ protocol, caseProtocol, kind string }{
		{"unknown", "unknown", "chat_sync"},
		{"openai-chat", "wan-video", "wan_task_success"},
		{"openai-chat", "openai-chat", "wan_task_success"},
		{"kimi-k3", "kimi-k3", "invented_kind"},
	} {
		t.Run(input.protocol+"/"+input.caseProtocol+"/"+input.kind, func(t *testing.T) {
			doer := registryDoer(func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid dispatch sent an HTTP request")
				return nil, nil
			})
			_, err := RunCase(context.Background(), doer, RunConfig{Suite: input.protocol}, PlannedRun{
				Case: CaseDefinition{Protocol: input.caseProtocol, Kind: input.kind}, Model: "test-model", ResultID: "test-result",
			})
			if err == nil {
				t.Fatal("invalid dispatch was accepted")
			}
		})
	}
}

func TestRunCasePreservesPlannedModelAndResultID(t *testing.T) {
	for _, protocol := range []string{"openai-chat", "kimi-k3"} {
		t.Run(protocol, func(t *testing.T) {
			requests := 0
			doer := registryDoer(func(request *http.Request) (*http.Response, error) {
				requests++
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "planned-model" {
					t.Fatalf("request model = %v", body["model"])
				}
				if request.URL.Path != "/v1/chat/completions" {
					t.Fatalf("path = %s", request.URL.Path)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"id":"chat-1","choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))}, nil
			})
			result, err := RunCase(context.Background(), doer, RunConfig{Suite: protocol, BaseURL: "https://test.example", Model: "wrong-model"}, PlannedRun{
				Case:  CaseDefinition{ID: "case-1", Protocol: protocol, Kind: "chat_sync", Request: RequestDefinition{Method: "POST", Path: "/v1/chat/completions", Body: map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hello"}}}}},
				Model: "planned-model", ResultID: "case-1@planned-model",
			})
			if err != nil {
				t.Fatal(err)
			}
			if requests != 1 || result.Status != StatusPass || result.ID != "case-1@planned-model" || result.Model != "planned-model" {
				t.Fatalf("requests = %d, result = %+v", requests, result)
			}
		})
	}
}

func TestRunCaseDispatchesVideoProtocolsThroughTerminalValidation(t *testing.T) {
	for _, test := range []struct {
		protocol, kind, model, createPath, pollPath, created, completed string
	}{
		{"seedance", "seedance_task", "seedance-model", "/api/v3/contents/generations/tasks", "/api/v3/contents/generations/tasks/task-1", `{"id":"task-1"}`, `{"status":"succeeded","content":{"video_url":"https://result.example/video.mp4"}}`},
		{"wan-video", "wan_task_success", "wan3.0-video", "/api/v1/services/aigc/video-generation/video-synthesis", "/api/v1/tasks/task-1", `{"output":{"task_id":"task-1","task_status":"PENDING"}}`, `{"output":{"task_id":"task-1","task_status":"SUCCEEDED","video_url":"https://result.example/video.mp4"}}`},
		{"minimax-video", "minimax_video_task_success", "MiniMax-H3", "/v2/video_generation", "/v2/query/video_generation/task-1", `{"task_id":"task-1"}`, `{"task":{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"task_type":"generation","modality":"video"}}`},
	} {
		t.Run(test.protocol, func(t *testing.T) {
			requests := 0
			doer := registryDoer(func(request *http.Request) (*http.Response, error) {
				requests++
				response := test.created
				if requests == 1 {
					if request.Method != "POST" || request.URL.Path != test.createPath {
						t.Fatalf("create request = %s %s", request.Method, request.URL.Path)
					}
				} else {
					if requests != 2 || request.Method != "GET" || request.URL.Path != test.pollPath {
						t.Fatalf("poll request %d = %s %s", requests, request.Method, request.URL.Path)
					}
					response = test.completed
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			result, err := RunCase(context.Background(), doer, RunConfig{Suite: test.protocol, BaseURL: "https://test.example", PollInterval: time.Nanosecond, Timeout: time.Second}, PlannedRun{
				Model: test.model, ResultID: "planned-result", Case: CaseDefinition{ID: "case-1", Protocol: test.protocol, Kind: test.kind, Request: RequestDefinition{Method: "POST", Path: test.createPath, Body: map[string]any{"prompt": "test"}}},
			})
			if err != nil || result.Status != StatusPass || requests != 2 || result.ID != "planned-result" || result.Model != test.model {
				t.Fatalf("requests = %d, result = %+v, err = %v", requests, result, err)
			}
		})
	}
}

func TestRegisteredProtocolsHaveMetadataAndExecutableKinds(t *testing.T) {
	// A protocol may support a different CaseType without a legacy driver.
	// Every registered legacy driver must still be discoverable and executable.
	for id, driver := range drivers {
		if _, ok := protocol.Lookup(id); !ok {
			t.Errorf("driver %s has no protocol descriptor", id)
		}
		if driver.run == nil || len(driver.kinds) == 0 {
			t.Errorf("driver %s has no implementation or accepted kinds", id)
		}
	}
}
