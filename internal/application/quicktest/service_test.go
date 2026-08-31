package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/execution/load"
)

func TestRunBaseURLUsesEphemeralCredentialAndReturnsSafeMeasurement(t *testing.T) {
	var received atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("authorization header was not populated")
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body.Model != "model-a" || len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content != "say ok" {
			t.Errorf("request body = %#v", body)
		}
		received.Store(true)
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":7,"completion_tokens":2,"prompt_tokens_details":{"cached_tokens":3}}}`)
	}))
	defer server.Close()

	service := New(Dependencies{Transport: server.Client().Transport})
	result, err := service.Run(context.Background(), Command{
		AddressMode: AddressModeBaseURL,
		URL:         server.URL + "/v1/",
		APIKey:      "test-secret",
		ModelID:     "model-a",
		Prompt:      "say ok",
		TimeoutMS:   2_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !received.Load() {
		t.Fatal("server did not receive request")
	}
	if !result.Success || result.SchemaVersion != 1 || result.AddressMode != AddressModeBaseURL {
		t.Fatalf("result = %#v", result)
	}
	if result.BaseURL != server.URL+"/v1" || result.Endpoint != server.URL+"/v1/chat/completions" {
		t.Fatalf("normalized addresses = %#v", result)
	}
	if result.HTTPStatus != http.StatusOK || result.E2EMS <= 0 {
		t.Fatalf("measurement = %#v", result)
	}
	if result.PromptTokens != 7 || result.CompletionTokens != 2 || result.CachedTokens != 3 || result.ErrorCode != "" {
		t.Fatalf("usage = %#v", result)
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "test-secret") || strings.Contains(string(encoded), "say ok") || strings.Contains(string(encoded), "choices") {
		t.Fatalf("result leaked request, response, or credential: %s", encoded)
	}
	var safe map[string]any
	if err := json.Unmarshal(encoded, &safe); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"schema_version": true, "success": true, "address_mode": true,
		"base_url": true, "endpoint": true, "http_status": true, "e2e_ms": true,
		"prompt_tokens": true, "completion_tokens": true, "cached_tokens": true,
	}
	for key := range safe {
		if !allowed[key] {
			t.Fatalf("unsafe result field %q in %s", key, encoded)
		}
	}
}

func TestRunFullURLSplitsKnownEndpointAndUsesDefaultPrompt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		messages := body["messages"].([]any)
		content := messages[0].(map[string]any)["content"]
		if content != DefaultPrompt {
			t.Errorf("default prompt = %#v", content)
		}
		fmt.Fprint(writer, `{"choices":[{"message":{"content":"OK"}}]}`)
	}))
	defer server.Close()

	result, err := New(Dependencies{Transport: server.Client().Transport}).Run(context.Background(), Command{
		AddressMode: AddressModeFullURL,
		URL:         server.URL + "/chat/completions",
		APIKey:      "secret",
		ModelID:     "model-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Success || result.BaseURL != server.URL || result.Endpoint != server.URL+"/chat/completions" {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunRejectsInvalidOrUnsafeCommandsBeforeTransport(t *testing.T) {
	var calls atomic.Int64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("transport must not be called")
	})
	tests := []struct {
		name    string
		command Command
		code    string
	}{
		{name: "unknown mode", command: validCommand(), code: string(ErrorInvalidRequest)},
		{name: "remote http", command: withCommand(validCommand(), func(command *Command) { command.URL = "http://example.com/v1" }), code: string(ErrorInsecureEndpoint)},
		{name: "query", command: withCommand(validCommand(), func(command *Command) { command.URL += "?key=secret" }), code: string(ErrorInvalidRequest)},
		{name: "fragment", command: withCommand(validCommand(), func(command *Command) { command.URL += "#fragment" }), code: string(ErrorInvalidRequest)},
		{name: "userinfo", command: withCommand(validCommand(), func(command *Command) { command.URL = "https://user:pass@example.com/v1" }), code: string(ErrorInvalidRequest)},
		{name: "wrong full endpoint", command: withCommand(validCommand(), func(command *Command) {
			command.AddressMode = AddressModeFullURL
			command.URL = "https://example.com/v1/responses"
		}), code: string(ErrorInvalidRequest)},
		{name: "missing key", command: withCommand(validCommand(), func(command *Command) { command.APIKey = "" }), code: string(ErrorCredentialRequired)},
		{name: "missing model", command: withCommand(validCommand(), func(command *Command) { command.ModelID = "" }), code: string(ErrorInvalidRequest)},
		{name: "negative timeout", command: withCommand(validCommand(), func(command *Command) { command.TimeoutMS = -1 }), code: string(ErrorInvalidRequest)},
		{name: "excessive timeout", command: withCommand(validCommand(), func(command *Command) { command.TimeoutMS = MaxTimeoutMS + 1 }), code: string(ErrorInvalidRequest)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "unknown mode" {
				test.command.AddressMode = "unknown"
			}
			result, err := New(Dependencies{Transport: transport}).Run(context.Background(), test.command)
			if err != nil {
				t.Fatal(err)
			}
			if result.Success || string(result.ErrorCode) != test.code {
				t.Fatalf("result = %#v, want error_code %q", result, test.code)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("transport calls = %d", calls.Load())
	}
}

func TestRunMapsSemanticAndTimeoutFailuresWithoutProviderDetails(t *testing.T) {
	t.Run("authentication", func(t *testing.T) {
		for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
			t.Run(http.StatusText(status), func(t *testing.T) {
				server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.WriteHeader(status)
					fmt.Fprint(writer, `{"error":{"message":"secret provider detail"}}`)
				}))
				defer server.Close()
				command := validCommand()
				command.URL = server.URL
				result, err := New(Dependencies{Transport: server.Client().Transport}).Run(context.Background(), command)
				if err != nil {
					t.Fatal(err)
				}
				if result.Success || result.ErrorCode != ErrorAuthenticationFailed || result.HTTPStatus != status {
					t.Fatalf("result = %#v", result)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "secret provider detail") {
					t.Fatalf("provider detail leaked: %s", encoded)
				}
			})
		}
	})

	t.Run("semantic empty", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(writer, `{"choices":[],"provider_error":"sensitive detail"}`)
		}))
		defer server.Close()
		command := validCommand()
		command.URL = server.URL
		result, err := New(Dependencies{Transport: server.Client().Transport}).Run(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success || result.ErrorCode != load.ErrorSemanticEmpty || result.HTTPStatus != http.StatusOK {
			t.Fatalf("result = %#v", result)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "sensitive detail") {
			t.Fatalf("provider detail leaked: %s", encoded)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond)
			fmt.Fprint(writer, `{"choices":[{"message":{"content":"late"}}]}`)
		}))
		defer server.Close()
		command := validCommand()
		command.URL = server.URL
		command.TimeoutMS = 20
		result, err := New(Dependencies{Transport: server.Client().Transport}).Run(context.Background(), command)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success || result.ErrorCode != load.ErrorTimeout || result.E2EMS <= 0 {
			t.Fatalf("result = %#v", result)
		}
	})
}

func validCommand() Command {
	return Command{
		AddressMode: AddressModeBaseURL,
		URL:         "https://example.com/v1",
		APIKey:      "secret",
		ModelID:     "model",
		Prompt:      "ok",
		TimeoutMS:   1_000,
	}
}

func withCommand(command Command, change func(*Command)) Command {
	change(&command)
	return command
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
