package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestPerformanceUsesPinnedSuitePathForTemporaryAndSavedConnections(t *testing.T) {
	for _, mode := range []string{"temporary", "channel", "remembered"} {
		t.Run(mode, func(t *testing.T) {
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
			command := PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, Task: &task, AddressMode: AddressModeBaseURL, URL: server.URL + "/proxy", APIKey: "test-key", ModelID: "model", RequestCount: 1, Concurrency: 1, TimeoutMS: 2000, InputTokens: 2, OutputTokens: 2}
			dependencies := Dependencies{Transport: server.Client().Transport, TaskPath: func(_ context.Context, got TaskReference, model string, _ domain.Protocol) (string, error) {
				if got != task || model != "model" {
					t.Fatal("lost Suite identity or target")
				}
				return "/v1/chat/completions", nil
			}}
			if mode == "channel" {
				command.ChannelID, command.APIKey, command.URL = "123e4567-e89b-42d3-a456-426614174003", "", "https://ignored.example.test"
				dependencies.ChannelConnections = &stubChannelConnectionResolver{connection: ChannelConnection{BaseURL: server.URL + "/proxy", APIKey: []byte("test-key")}}
			}
			if mode == "remembered" {
				command.CredentialRunID, command.APIKey = task.SourceRunID, ""
				dependencies.TaskCredential = func(_ context.Context, id, baseURL string, _ domain.Protocol) (*credentials.Lease, error) {
					if id != task.SourceRunID || baseURL != server.URL+"/proxy" {
						t.Fatal("lost remembered connection identity")
					}
					return credentials.NewTemporaryLease([]byte("test-key"))
				}
			}
			report, err := New(dependencies).RunPerformance(context.Background(), command)
			if err != nil || !report.Success || calls != 1 || report.Endpoint != server.URL+"/proxy/v1/chat/completions" {
				t.Fatalf("Suite performance: calls=%d, code=%s, err=%v", calls, report.ErrorCode, err)
			}
			encoded, _ := json.Marshal(report)
			if strings.Contains(string(encoded), "test-key") || strings.Contains(string(encoded), "credential_run_id") {
				t.Fatal("performance report exposed credential data")
			}
		})
	}
}

func TestPerformanceCompletesPartialAndFullSuiteAddresses(t *testing.T) {
	for _, suffix := range []string{
		"", "/", "/v1", "/v1/", "/v1/chat/co", "/v1/chat/completions", "/v1/chat/completions/",
	} {
		t.Run(suffix, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/proxy/v1/chat/completions" {
					t.Errorf("request path = %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
			}))
			defer server.Close()
			service := New(Dependencies{
				Transport: server.Client().Transport,
				TaskPath: func(context.Context, TaskReference, string, domain.Protocol) (string, error) {
					return "/v1/chat/completions", nil
				},
			})
			report, err := service.RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, Task: &TaskReference{SuiteID: "123e4567-e89b-42d3-a456-426614174001", SuiteRevision: 1},
				AddressMode: AddressModeBaseURL, URL: server.URL + "/proxy" + suffix,
				APIKey: "test-key", ModelID: "model", RequestCount: 1, Concurrency: 1,
				TimeoutMS: 2000, InputTokens: 2, OutputTokens: 2,
			})
			if err != nil || !report.Success || calls != 1 || report.Endpoint != server.URL+"/proxy/v1/chat/completions" {
				t.Fatalf(
					"report = %+v, calls = %d, error = %v",
					report,
					calls,
					err,
				)
			}
		})
	}
}

func TestPerformanceRejectsUnresolvedOrUnsafeSuitePaths(t *testing.T) {
	for _, path := range []string{"", "https://different.test/chat/completions", "//different.test/chat/completions", "/v1/chat/completions?key=secret", "/v1/chat/completions#private", "/tasks"} {
		t.Run(path, func(t *testing.T) {
			report, err := New(Dependencies{TaskPath: func(context.Context, TaskReference, string, domain.Protocol) (string, error) { return path, nil }}).RunPerformance(context.Background(), PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, Task: &TaskReference{}, AddressMode: AddressModeBaseURL, URL: "https://example.test", APIKey: "test-key", ModelID: "model", RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 2, OutputTokens: 2})
			if err != nil || report.ErrorCode != ErrorInvalidRequest || report.Progress.Launched != 0 {
				t.Fatal("invalid Suite path was not rejected before execution")
			}
		})
	}
}

func TestPerformanceRejectsAmbiguousOrUnavailableRememberedKeys(t *testing.T) {
	for _, scenario := range []string{"key", "channel", "missing-task", "full-url", "unavailable", "missing-resolver"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			dependencies := Dependencies{TaskCredential: func(context.Context, string, string, domain.Protocol) (*credentials.Lease, error) {
				calls++
				return nil, credentials.ErrNotFound
			}}
			command := PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, Task: &TaskReference{}, CredentialRunID: "123e4567-e89b-42d3-a456-426614174002", AddressMode: AddressModeBaseURL, URL: "https://example.test"}
			switch scenario {
			case "key":
				command.APIKey = "test-key"
			case "channel":
				command.ChannelID = "123e4567-e89b-42d3-a456-426614174003"
			case "missing-task":
				command.Task = nil
			case "full-url":
				command.AddressMode = AddressModeFullURL
			case "missing-resolver":
				dependencies.TaskCredential = nil
			}
			report, err := New(dependencies).RunPerformance(context.Background(), command)
			if err != nil || report.ErrorCode == "" || report.Progress.Launched != 0 {
				t.Fatal("invalid remembered credential request reached execution")
			}
			if scenario != "unavailable" && calls != 0 {
				t.Fatal("ambiguous request accessed the keyring")
			}
			if scenario == "unavailable" && (calls != 1 || report.ErrorCode != ErrorCredentialRequired) {
				t.Fatal("missing key did not return a recoverable error")
			}
		})
	}
}
