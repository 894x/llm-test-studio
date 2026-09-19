package compatibility_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/application/compatibility"
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
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Header:     make(http.Header),
	}, nil
}

func TestRunMiniMaxVideoPollsH3TaskToVerifiedBusinessSuccess(t *testing.T) {
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
	doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{
		{status: http.StatusOK, body: `{"task_id":"task-123"}`},
		{status: http.StatusOK, body: `{"task":{"id":"task-123","model":"MiniMax-H3","status":"running","resolution":"768P","duration":4,"ratio":"16:9","task_type":"generation","modality":"video"}}`},
		{status: http.StatusOK, body: `{"task":{"id":"task-123","model":"MiniMax-H3","status":"succeeded","content":{"url":"https://result.example/h3.mp4"},"resolution":"768P","duration":4,"usage":{"total_seconds":4,"input_seconds":0,"output_seconds":4,"input_image_count":0},"ratio":"16:9","task_type":"generation","modality":"video"}}`},
	}}
	var written apiaudit.Report
	service := compatibility.New(compatibility.Dependencies{
		HTTPDoer: doer, Emit: func(compatibility.Event) {},
		WriteReport: func(_ string, report apiaudit.Report) error { written = report; return nil }, Now: time.Now,
	})
	_, err := service.Run(context.Background(), compatibility.RunRequest{
		Suite: "minimax-video", CasesRoot: casesRoot, BaseURL: "https://api.minimax.cn", APIKey: "secret", Model: "MiniMax-H3",
		OutputDir: t.TempDir(), PollInterval: time.Nanosecond, Timeout: time.Second, Concurrency: 1,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(written.Results) != 1 || written.Results[0].Status != apiaudit.StatusPass {
		t.Fatalf("results = %#v", written.Results)
	}
	if len(doer.requests) != 3 || doer.requests[1].Method != http.MethodGet || doer.requests[1].URL.Path != "/v2/query/video_generation/task-123" {
		t.Fatalf("requests = %#v", doer.requests)
	}
}

func TestRunMiniMaxVideoPassesOnlyForStructuredBadRequestRejection(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantStatus string
	}{
		{
			name: "documented bad request", status: http.StatusBadRequest,
			body:       `{"type":"error","error":{"type":"bad_request_error","message":"invalid duration (2013)","http_code":"400"},"request_id":"request-1"}`,
			wantStatus: apiaudit.StatusPass,
		},
		{
			name: "authentication failure", status: http.StatusUnauthorized,
			body:       `{"type":"error","error":{"type":"authorized_error","message":"login fail (1004)","http_code":"401"},"request_id":"request-2"}`,
			wantStatus: apiaudit.StatusFail,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			casesRoot := t.TempDir()
			writeCase(t, casesRoot, "minimax-video", "H3002", `{
  "schema_version": 1,
  "key": "H3002",
  "name": "H3 duration below minimum",
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
          "duration": 3,
          "ratio": "16:9"
        }
      },
      "assertions": [
        {
          "id": "http",
          "source": "http.status",
          "operator": "equals",
          "value": 400
        },
        {
          "id": "error",
          "source": "response",
          "pointer": "/error/type",
          "operator": "equals",
          "value": "bad_request_error"
        }
      ]
    }
  }
}`)
			doer := &miniMaxSequenceDoer{responses: []miniMaxResponse{{status: test.status, body: test.body}}}
			var written apiaudit.Report
			service := compatibility.New(compatibility.Dependencies{
				HTTPDoer: doer, Emit: func(compatibility.Event) {},
				WriteReport: func(_ string, report apiaudit.Report) error { written = report; return nil }, Now: time.Now,
			})
			_, err := service.Run(context.Background(), compatibility.RunRequest{
				Suite: "minimax-video", CasesRoot: casesRoot, BaseURL: "https://api.minimax.cn", APIKey: "secret", Model: "MiniMax-H3",
				OutputDir: t.TempDir(), PollInterval: time.Second, Timeout: time.Second, Concurrency: 1,
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(written.Results) != 1 || written.Results[0].Status != test.wantStatus {
				t.Fatalf("results = %#v", written.Results)
			}
		})
	}
}
