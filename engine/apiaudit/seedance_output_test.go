package apiaudit

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSeedanceSucceededRequiresUsableVideoOutput(t *testing.T) {
	for _, test := range []struct {
		name, output string
		wantPass     bool
	}{
		{"missing content", `{"status":"succeeded"}`, false},
		{"missing URL", `{"status":"succeeded","content":{}}`, false},
		{"empty URL", `{"status":"succeeded","content":{"video_url":""}}`, false},
		{"relative URL", `{"status":"succeeded","content":{"video_url":"/video.mp4"}}`, false},
		{"non HTTP URL", `{"status":"succeeded","content":{"video_url":"javascript:alert(1)"}}`, false},
		{"embedded credential", `{"status":"succeeded","content":{"video_url":"https://user:secret@result.example/video.mp4"}}`, false},
		{"signed HTTPS output", `{"status":"succeeded","content":{"video_url":"https://result.example/video.mp4?signature=output-secret"}}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			doer := registryDoer(func(request *http.Request) (*http.Response, error) {
				requests++
				body := `{"id":"task-1"}`
				if requests == 2 {
					if request.Method != "GET" || request.URL.Path != "/tasks/task-1" {
						t.Fatalf("poll request = %s %s", request.Method, request.URL.Path)
					}
					body = test.output
				} else if requests != 1 {
					t.Fatalf("unexpected request %d", requests)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			result := RunSeedanceCase(context.Background(), doer, RunConfig{BaseURL: "https://test.example", PollInterval: time.Nanosecond, Timeout: time.Second}, PlannedRun{
				Model: "test-model", Case: CaseDefinition{ID: "output", Protocol: "seedance", Kind: "seedance_task", Request: RequestDefinition{Method: "POST", Path: "/tasks", Body: map[string]any{"prompt": "test"}}},
			})
			if (result.Status == StatusPass) != test.wantPass {
				t.Fatalf("status = %s, evidence = %s, want pass = %v", result.Status, result.Evidence, test.wantPass)
			}
			if !test.wantPass && result.Status != StatusFail {
				t.Fatalf("invalid output status = %s", result.Status)
			}
			if strings.Contains(result.Evidence, "secret") {
				t.Fatalf("evidence exposes URL credentials: %s", result.Evidence)
			}
		})
	}
}
