package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type wanResponse struct {
	status int
	body   string
}

type wanSequenceDoer struct {
	responses []wanResponse
	requests  []*http.Request
}

func (doer *wanSequenceDoer) Do(request *http.Request) (*http.Response, error) {
	doer.requests = append(doer.requests, request)
	response := doer.responses[0]
	doer.responses = doer.responses[1:]
	return &http.Response{
		StatusCode: response.status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(response.body)),
	}, nil
}

func TestRunWanVideoCasePollsDashScopeTaskToBusinessSuccess(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{
		{status: http.StatusOK, body: `{"output":{"task_id":"task-123","task_status":"PENDING"},"request_id":"create-1"}`},
		{status: http.StatusOK, body: `{"output":{"task_id":"task-123","task_status":"RUNNING"},"request_id":"poll-1"}`},
		{status: http.StatusOK, body: `{"output":{"task_id":"task-123","task_status":"SUCCEEDED","video_url":"https://result.example/video.mp4"},"usage":{"video_count":1,"duration":2},"request_id":"poll-2"}`},
	}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{
		BaseURL: "https://workspace.example", APIKey: "secret", PollInterval: time.Nanosecond, Timeout: time.Second,
	}, PlannedRun{Model: "wan3.0-video", ResultID: "wan30-smoke", Case: CaseDefinition{
		ID: "wan30-smoke", Name: "smoke", Dimension: "compatibility", Protocol: "wan-video", Kind: "wan_task_success",
		Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Headers: map[string]string{"X-DashScope-Async": "enable"}, Body: map[string]any{"input": map[string]any{"prompt": "cat"}}},
	}})

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want pass; evidence=%s", result.Status, result.Evidence)
	}
	if len(doer.requests) != 3 || doer.requests[1].URL.Path != "/api/v1/tasks/task-123" || doer.requests[2].URL.Path != "/api/v1/tasks/task-123" {
		t.Fatalf("request paths = %#v", []string{doer.requests[0].URL.Path, doer.requests[1].URL.Path, doer.requests[2].URL.Path})
	}
	if doer.requests[0].Header.Get("X-DashScope-Async") != "enable" {
		t.Fatalf("async header = %q", doer.requests[0].Header.Get("X-DashScope-Async"))
	}
	if result.Evidence != "task task-123 succeeded; video_host=result.example" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
	if result.Usage["video_count"] != float64(1) {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestRunWanVideoCasePassesOnlyForStructuredAdmissionRejection(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{{
		status: http.StatusBadRequest,
		body:   `{"code":"InvalidParameter","message":"duration is out of range","request_id":"reject-1"}`,
	}}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{BaseURL: "https://workspace.example"}, PlannedRun{
		Model: "wan2.7-t2v", ResultID: "duration-below-min", Case: CaseDefinition{
			ID: "duration-below-min", Name: "duration below min", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_rejected",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}, "parameters": map[string]any{"duration": 1}}},
		},
	})

	if result.Status != StatusPass || result.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("result = %#v", result)
	}
	if result.Evidence != "request rejected with HTTP 400 and provider code InvalidParameter" {
		t.Fatalf("evidence = %q", result.Evidence)
	}
}

func TestRunWanVideoCasePassesWhenParameterRejectionArrivesAsTerminalFailure(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{
		{status: http.StatusOK, body: `{"output":{"task_id":"task-rejected","task_status":"PENDING"},"request_id":"create-1"}`},
		{status: http.StatusOK, body: `{"output":{"task_id":"task-rejected","task_status":"FAILED","code":"InvalidParameter","message":"media types are mutually exclusive"},"request_id":"poll-1"}`},
	}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{
		BaseURL: "https://workspace.example", PollInterval: time.Nanosecond, Timeout: time.Second,
	}, PlannedRun{Model: "wan3.0-video", Case: CaseDefinition{
		ID: "media-conflict", Name: "media conflict", Dimension: "dependencies", Protocol: "wan-video", Kind: "wan_task_rejected",
		Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}}},
	}})

	if result.Status != StatusPass {
		t.Fatalf("status = %q, want pass; evidence=%s", result.Status, result.Evidence)
	}
	if len(doer.requests) != 2 {
		t.Fatalf("request count = %d, want create plus poll", len(doer.requests))
	}
}

func TestRunWanVideoCaseRequiresConfiguredUsageEvidence(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{
		{status: http.StatusOK, body: `{"output":{"task_id":"task-usage","task_status":"PENDING"}}`},
		{status: http.StatusOK, body: `{"output":{"task_id":"task-usage","task_status":"SUCCEEDED","video_url":"https://result.example/video.mp4"},"usage":{"video_count":1,"duration":2,"SR":720,"ratio":"16:9"}}`},
	}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{
		BaseURL: "https://workspace.example", PollInterval: time.Nanosecond, Timeout: time.Second,
	}, PlannedRun{Model: "wan3.0-video", Case: CaseDefinition{
		ID: "resolution-evidence", Name: "resolution evidence", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_success",
		Options: map[string]any{"expected_usage": map[string]any{"SR": float64(1080)}},
		Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}}},
	}})

	if result.Status != StatusFail || !strings.Contains(result.Evidence, "usage.SR") {
		t.Fatalf("result = %#v, want usage mismatch failure", result)
	}
}

func TestRunWanVideoCaseExpandsPromptLengthOption(t *testing.T) {
	result := RunWanVideoCase(context.Background(), nil, RunConfig{BaseURL: "https://workspace.example", DryRun: true}, PlannedRun{
		Model: "wan3.0-video", Case: CaseDefinition{
			ID: "prompt-length", Name: "prompt length", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_success",
			Options: map[string]any{"prompt_length": float64(5)},
			Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "placeholder"}}},
		},
	})

	input, _ := result.Exchanges[0].RequestBody["input"].(map[string]any)
	prompt, _ := input["prompt"].(string)
	if len([]rune(prompt)) != 5 {
		t.Fatalf("prompt length = %d, want 5; prompt=%q", len([]rune(prompt)), prompt)
	}
}

func TestRunWanVideoCaseHonorsModelMode(t *testing.T) {
	for _, item := range []struct {
		name      string
		mode      string
		bodyModel any
		wantModel any
		wantKey   bool
	}{
		{name: "configured target", mode: "target", bodyModel: "ignored", wantModel: "wan3.0-video", wantKey: true},
		{name: "preserve body", mode: "body", bodyModel: "wan3.0-unknown", wantModel: "wan3.0-unknown", wantKey: true},
		{name: "omit", mode: "omit", bodyModel: "ignored", wantKey: false},
	} {
		t.Run(item.name, func(t *testing.T) {
			result := RunWanVideoCase(context.Background(), nil, RunConfig{BaseURL: "https://workspace.example", DryRun: true}, PlannedRun{
				Model: "wan3.0-video", Case: CaseDefinition{
					ID: "model-mode", Name: "model mode", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_rejected",
					Options: map[string]any{"model_mode": item.mode},
					Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"model": item.bodyModel, "input": map[string]any{"prompt": "cat"}}},
				},
			})
			got, exists := result.Exchanges[0].RequestBody["model"]
			if exists != item.wantKey || (item.wantKey && got != item.wantModel) {
				t.Fatalf("model = %#v, exists=%t; want %#v, exists=%t", got, exists, item.wantModel, item.wantKey)
			}
		})
	}
}

func TestRunWanVideoCaseDoesNotMistakeAuthenticationFailureForBoundaryRejection(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{{
		status: http.StatusUnauthorized,
		body:   `{"code":"InvalidApiKey","message":"invalid API key","request_id":"auth-1"}`,
	}}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{BaseURL: "https://workspace.example"}, PlannedRun{
		Model: "wan2.7-t2v", ResultID: "duration-below-min", Case: CaseDefinition{
			ID: "duration-below-min", Name: "duration below min", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_rejected",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}, "parameters": map[string]any{"duration": 1}}},
		},
	})

	if result.Status != StatusFail {
		t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
	}
}

func TestRunWanVideoCaseDoesNotMistakeNonParameterBadRequestForBoundaryRejection(t *testing.T) {
	doer := &wanSequenceDoer{responses: []wanResponse{{
		status: http.StatusBadRequest,
		body:   `{"code":"Arrearage","message":"account is in arrears","request_id":"billing-1"}`,
	}}}
	result := RunWanVideoCase(context.Background(), doer, RunConfig{BaseURL: "https://workspace.example"}, PlannedRun{
		Model: "wan2.7-t2v", ResultID: "duration-below-min", Case: CaseDefinition{
			ID: "duration-below-min", Name: "duration below min", Dimension: "parameters", Protocol: "wan-video", Kind: "wan_task_rejected",
			Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}, "parameters": map[string]any{"duration": 1}}},
		},
	})

	if result.Status != StatusFail {
		t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
	}
}

func TestRunWanVideoCaseRejectsUnsafeOrNonHTTPVideoURL(t *testing.T) {
	for _, videoURL := range []string{
		"ftp://result.example/video.mp4",
		"//result.example/video.mp4",
		"https://user:secret@result.example/video.mp4",
		"/video.mp4",
	} {
		t.Run(videoURL, func(t *testing.T) {
			doer := &wanSequenceDoer{responses: []wanResponse{
				{status: http.StatusOK, body: `{"output":{"task_id":"task-unsafe","task_status":"PENDING"}}`},
				{status: http.StatusOK, body: `{"output":{"task_id":"task-unsafe","task_status":"SUCCEEDED","video_url":"` + videoURL + `"}}`},
			}}
			result := RunWanVideoCase(context.Background(), doer, RunConfig{
				BaseURL: "https://workspace.example", PollInterval: time.Nanosecond, Timeout: time.Second,
			}, PlannedRun{Model: "wan3.0-video", Case: CaseDefinition{
				ID: "unsafe-url", Name: "unsafe URL", Protocol: "wan-video", Kind: "wan_task_success",
				Request: RequestDefinition{Method: http.MethodPost, Path: "/api/v1/services/aigc/video-generation/video-synthesis", Body: map[string]any{"input": map[string]any{"prompt": "cat"}}},
			}})

			if result.Status != StatusFail {
				t.Fatalf("status = %q, want fail; evidence=%s", result.Status, result.Evidence)
			}
		})
	}
}

func TestExpandRunsPreservesSelectedVideoTasksInLiveAndDryRunModes(t *testing.T) {
	for _, protocol := range []string{"seedance", "wan-video", "minimax-video"} {
		for _, dryRun := range []bool{false, true} {
			runs, err := ExpandRuns(RunConfig{Suite: protocol, Model: "explicit-model", DryRun: dryRun}, []CaseDefinition{{ID: "first"}, {ID: "second"}})
			if err != nil || len(runs) != 2 || runs[0].Case.ID != "first" || runs[1].Case.ID != "second" {
				t.Fatalf("%s dry=%t plan=%#v error=%v", protocol, dryRun, runs, err)
			}
		}
	}
}
