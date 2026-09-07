package quicktest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPerformanceUsesPinnedSuitePathForTemporaryAndSavedConnections(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprint(saved), func(t *testing.T) {
			calls := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/proxy/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
					t.Errorf("wrong Suite endpoint or credential: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			task := TaskReference{SuiteID: "123e4567-e89b-42d3-a456-426614174001", SuiteRevision: 2, SourceRunID: "123e4567-e89b-42d3-a456-426614174002"}
			command := PerformanceCommand{Task: &task, AddressMode: AddressModeBaseURL, URL: server.URL + "/proxy", APIKey: "test-key", ModelID: "model", RequestCount: 1, Concurrency: 1, TimeoutMS: 2000, InputTokens: 2, OutputTokens: 2}
			dependencies := Dependencies{Transport: server.Client().Transport, TaskPath: func(_ context.Context, got TaskReference, model string) (string, error) {
				if got != task || model != "model" {
					t.Fatal("lost Suite identity or target")
				}
				return "/v1/chat/completions", nil
			}}
			if saved {
				command.ChannelID, command.APIKey, command.URL = "123e4567-e89b-42d3-a456-426614174003", "", "https://ignored.example.test"
				dependencies.ChannelConnections = &stubChannelConnectionResolver{connection: ChannelConnection{BaseURL: server.URL + "/proxy", APIKey: []byte("test-key")}}
			}
			report, err := New(dependencies).RunPerformance(context.Background(), command)
			if err != nil || !report.Success || calls != 1 || report.Endpoint != server.URL+"/proxy/v1/chat/completions" {
				t.Fatalf("Suite performance: calls=%d, code=%s, err=%v", calls, report.ErrorCode, err)
			}
		})
	}
}

func TestPerformanceRejectsUnresolvedOrUnsafeSuitePaths(t *testing.T) {
	for _, path := range []string{"", "https://different.test/chat/completions", "//different.test/chat/completions", "/v1/chat/completions?key=secret", "/v1/chat/completions#private", "/tasks"} {
		t.Run(path, func(t *testing.T) {
			report, err := New(Dependencies{TaskPath: func(context.Context, TaskReference, string) (string, error) { return path, nil }}).RunPerformance(context.Background(), PerformanceCommand{Task: &TaskReference{}, AddressMode: AddressModeBaseURL, URL: "https://example.test", APIKey: "test-key", ModelID: "model", RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 2, OutputTokens: 2})
			if err != nil || report.ErrorCode != ErrorInvalidRequest || report.Progress.Launched != 0 {
				t.Fatal("invalid Suite path was not rejected before execution")
			}
		})
	}
}
