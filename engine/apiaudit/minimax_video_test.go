package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type miniMaxResponse struct {
	status int
	body   string
}

type miniMaxSequenceDoer struct {
	responses []miniMaxResponse
	requests  []*http.Request
}

func (doer *miniMaxSequenceDoer) Do(request *http.Request) (*http.Response, error) {
	doer.requests = append(doer.requests, request)
	response := doer.responses[0]
	doer.responses = doer.responses[1:]
	return &http.Response{
		StatusCode: response.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(response.body)),
	}, nil
}

func TestRunMiniMaxVideoCaseCanPreserveExplicitModelBoundaryPayload(t *testing.T) {
	for _, test := range []struct {
		name      string
		body      map[string]any
		modelMode string
		wantModel any
		wantKey   bool
	}{
		{name: "missing model", body: map[string]any{"content": []any{}}, modelMode: "omit", wantKey: false},
		{name: "unknown model", body: map[string]any{"model": "MiniMax-H3-Unknown", "content": []any{}}, modelMode: "body", wantModel: "MiniMax-H3-Unknown", wantKey: true},
		{name: "wrong model type", body: map[string]any{"model": 7, "content": []any{}}, modelMode: "body", wantModel: float64(7), wantKey: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := RunMiniMaxVideoCase(context.Background(), nil, RunConfig{
				BaseURL: "https://api.minimax.cn", DryRun: true,
			}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
				ID: "model-boundary", Name: test.name, Protocol: "minimax-video", Kind: "minimax_video_task_rejected",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: test.body},
				Options: map[string]any{"model_mode": test.modelMode},
			}})

			if len(result.Exchanges) != 1 {
				t.Fatalf("exchanges = %#v", result.Exchanges)
			}
			got, exists := result.Exchanges[0].RequestBody["model"]
			if exists != test.wantKey || got != test.wantModel {
				t.Fatalf("rendered model = %#v, exists=%v; want %#v, exists=%v", got, exists, test.wantModel, test.wantKey)
			}
		})
	}
}

func TestRunMiniMaxVideoCaseRendersPromptLengthBoundary(t *testing.T) {
	result := RunMiniMaxVideoCase(context.Background(), nil, RunConfig{
		BaseURL: "https://api.minimax.cn", DryRun: true,
	}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
		ID: "text-max", Name: "text max", Protocol: "minimax-video", Kind: "minimax_video_task_success",
		Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{
			"content": []any{map[string]any{"type": "text", "text": "placeholder"}},
		}},
		Options: map[string]any{"prompt_length": float64(7000)},
	}})

	content, _ := result.Exchanges[0].RequestBody["content"].([]any)
	item, _ := content[0].(map[string]any)
	prompt, _ := item["text"].(string)
	if len([]rune(prompt)) != 7000 {
		t.Fatalf("rendered prompt length = %d, want 7000", len([]rune(prompt)))
	}
}

func TestRunMiniMaxVideoCaseRequiresDocumentedAuthorizationErrorShape(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus string
	}{
		{
			name: "documented authorization error", status: http.StatusUnauthorized,
			body:       `{"type":"error","error":{"type":"authorized_error","message":"login failed (1004)","http_code":"401"},"request_id":"request-1"}`,
			wantStatus: StatusPass,
		},
		{
			name: "bad request is not authentication rejection", status: http.StatusBadRequest,
			body:       `{"type":"error","error":{"type":"bad_request_error","message":"invalid body (2013)","http_code":"400"},"request_id":"request-2"}`,
			wantStatus: StatusFail,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{{status: test.status, body: test.body}}}
			result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
				BaseURL: "https://api.minimax.cn", APIKey: "configured-key",
			}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
				ID: "auth-boundary", Name: test.name, Protocol: "minimax-video", Kind: "minimax_video_auth_rejected",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{}}},
				Options: map[string]any{"omit_authorization": true},
			}})

			if result.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q; evidence=%s", result.Status, test.wantStatus, result.Evidence)
			}
			if got := doer.requests[0].Header.Get("Authorization"); got != "" {
				t.Fatalf("Authorization = %q, want omitted", got)
			}
		})
	}
}

func TestRunMiniMaxVideoCaseValidatesRequestedOutputContract(t *testing.T) {
	for _, test := range []struct {
		name string
		task string
	}{
		{name: "resolution mismatch", task: `{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"resolution":"768P","duration":15,"ratio":"9:16","usage":{"total_seconds":15,"input_seconds":0,"output_seconds":15},"task_type":"generation","modality":"video"}`},
		{name: "duration mismatch", task: `{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"resolution":"2K","duration":14,"ratio":"9:16","usage":{"total_seconds":14,"input_seconds":0,"output_seconds":14},"task_type":"generation","modality":"video"}`},
		{name: "ratio mismatch", task: `{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"resolution":"2K","duration":15,"ratio":"16:9","usage":{"total_seconds":15,"input_seconds":0,"output_seconds":15},"task_type":"generation","modality":"video"}`},
		{name: "usage arithmetic mismatch", task: `{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"resolution":"2K","duration":15,"ratio":"9:16","usage":{"total_seconds":14,"input_seconds":0,"output_seconds":15},"task_type":"generation","modality":"video"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
				{status: http.StatusOK, body: `{"task_id":"task-1"}`},
				{status: http.StatusOK, body: `{"task":` + test.task + `}`},
			}}
			result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
				BaseURL: "https://api.minimax.cn", PollInterval: time.Nanosecond, Timeout: time.Second,
			}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
				ID: "output-contract", Name: test.name, Protocol: "minimax-video", Kind: "minimax_video_task_success",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{}}},
				Options: map[string]any{
					"expected_resolution": "2K", "expected_duration": float64(15), "expected_ratio": "9:16", "require_video_usage": true,
				},
			}})

			if result.Status != StatusFail {
				t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
			}
		})
	}
}

func TestRunMiniMaxVideoCasePassesMatchingOutputAndUsageContract(t *testing.T) {
	doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
		{status: http.StatusOK, body: `{"task_id":"task-1"}`},
		{status: http.StatusOK, body: `{"task":{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"resolution":"2K","duration":15,"ratio":"9:16","usage":{"total_seconds":15,"input_seconds":0,"output_seconds":15,"input_image_count":0},"task_type":"generation","modality":"video"}}`},
	}}
	result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
		BaseURL: "https://api.minimax.cn", PollInterval: time.Nanosecond, Timeout: time.Second,
	}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
		ID: "output-contract", Name: "matching output", Protocol: "minimax-video", Kind: "minimax_video_task_success",
		Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{}}},
		Options: map[string]any{
			"expected_resolution": "2K", "expected_duration": float64(15), "expected_ratio": "9:16", "require_video_usage": true,
			"expected_usage": map[string]any{"total_seconds": float64(15), "input_seconds": float64(0), "output_seconds": float64(15), "input_image_count": float64(0)},
		},
	}})

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want pass; evidence=%s", result.Status, result.Evidence)
	}
}

func TestRunMiniMaxVideoCaseRejectsExpectedUsageMismatch(t *testing.T) {
	doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
		{status: http.StatusOK, body: `{"task_id":"task-1"}`},
		{status: http.StatusOK, body: `{"task":{"id":"task-1","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/video.mp4"},"usage":{"output_seconds":14},"task_type":"generation","modality":"video"}}`},
	}}
	result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
		BaseURL: "https://api.minimax.cn", PollInterval: time.Nanosecond, Timeout: time.Second,
	}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
		ID: "usage-contract", Name: "usage mismatch", Protocol: "minimax-video", Kind: "minimax_video_task_success",
		Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{}}},
		Options: map[string]any{"expected_usage": map[string]any{"output_seconds": float64(15)}},
	}})

	if result.Status != StatusFail || result.Evidence != "usage.output_seconds = 14; expected 15" {
		t.Fatalf("result = %#v, want expected-usage failure", result)
	}
}

func TestRunMiniMaxVideoCaseRejectsFailedAndCancelledTerminalStates(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
				{status: http.StatusOK, body: `{"task_id":"task-1"}`},
				{status: http.StatusOK, body: `{"task":{"id":"task-1","model":"MiniMax-H3","status":"` + status + `","error":{"code":"1026","message":"rejected"},"task_type":"generation","modality":"video"}}`},
			}}
			result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
				BaseURL: "https://api.minimax.cn", PollInterval: time.Nanosecond, Timeout: time.Second,
			}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
				ID: "terminal-state", Name: status, Protocol: "minimax-video", Kind: "minimax_video_task_success",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{}}},
			}})
			if result.Status != StatusFail {
				t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
			}
		})
	}
}

func TestRunMiniMaxVideoCaseRejectsUnsafeOrNonHTTPVideoURL(t *testing.T) {
	for _, videoURL := range []string{
		"ftp://result.example/video.mp4",
		"//result.example/video.mp4",
		"https://user:secret@result.example/video.mp4",
		"/video.mp4",
	} {
		t.Run(videoURL, func(t *testing.T) {
			doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
				{status: http.StatusOK, body: `{"task_id":"task-unsafe"}`},
				{status: http.StatusOK, body: `{"task":{"id":"task-unsafe","model":"MiniMax-H3","status":"succeeded","content":{"url":"` + videoURL + `"},"task_type":"generation","modality":"video"}}`},
			}}
			result := RunMiniMaxVideoCase(context.Background(), doer, RunConfig{
				BaseURL: "https://api.minimax.cn", PollInterval: time.Nanosecond, Timeout: time.Second,
			}, PlannedRun{Model: "MiniMax-H3", Case: CaseDefinition{
				ID: "unsafe-url", Name: "unsafe URL", Protocol: "minimax-video", Kind: "minimax_video_task_success",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/v2/video_generation", Body: map[string]any{"content": []any{map[string]any{"type": "text", "text": "cat"}}}},
			}})

			if result.Status != StatusFail {
				t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
			}
		})
	}
}

func TestExpandRunsRequiresExplicitPaidConfirmationForMiniMaxVideo(t *testing.T) {
	_, err := ExpandRuns(RunConfig{Suite: "minimax-video", Model: "MiniMax-H3"}, []CaseDefinition{{ID: "h3-smoke"}})
	if err == nil {
		t.Fatal("live MiniMax video plan did not require paid confirmation")
	}

	runs, err := ExpandRuns(RunConfig{Suite: "minimax-video", Model: "MiniMax-H3", DryRun: true}, []CaseDefinition{{ID: "h3-smoke"}})
	if err != nil || len(runs) != 1 {
		t.Fatalf("dry-run plan = %#v, error = %v", runs, err)
	}
}
