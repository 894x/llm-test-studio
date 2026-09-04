package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/execution/load"
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
		time.Sleep(2 * time.Millisecond)
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

func TestRunPerformanceUsesTheTestedConnectionAndReturnsABoundedReport(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer performance-secret" {
			t.Errorf("authorization header was not populated")
		}
		var body struct {
			Model     string `json:"model"`
			Stream    bool   `json:"stream"`
			MaxTokens uint32 `json:"max_tokens"`
			Messages  []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "performance-model" || !body.Stream || body.MaxTokens != 32 || len(body.Messages) != 1 {
			t.Errorf("request body = %#v", body)
		}
		if words := len(strings.Fields(body.Messages[0].Content)); words != 20 {
			t.Errorf("generated prompt words = %d, want 20", words)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":32,\"prompt_tokens_details\":{\"cached_tokens\":5}}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode:  AddressModeBaseURL,
		URL:          server.URL + "/v1",
		APIKey:       "performance-secret",
		ModelID:      "performance-model",
		RequestCount: 4,
		Concurrency:  2,
		TimeoutMS:    2_000,
		InputTokens:  20,
		OutputTokens: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 4 || !report.Success || report.SchemaVersion != PerformanceSchemaVersion {
		t.Fatalf("report = %#v, calls = %d", report, calls.Load())
	}
	if report.BaseURL != server.URL+"/v1" || report.Endpoint != server.URL+"/v1/chat/completions" {
		t.Fatalf("report address = %#v", report)
	}
	if report.Profile.RequestCount != 4 || report.Profile.Concurrency != 2 || report.Profile.InputTokens != 20 || report.Profile.OutputTokens != 32 {
		t.Fatalf("profile = %#v", report.Profile)
	}
	if report.Progress.Completed != 4 || report.Progress.Succeeded != 4 || report.Progress.Failed != 0 || report.Progress.Phase != load.PhaseCompleted {
		t.Fatalf("progress = %#v", report.Progress)
	}
	if report.Metrics.Completed != 4 || report.Metrics.PromptTokens != 80 || report.Metrics.CompletionTokens != 128 || report.Metrics.CachedTokens != 20 {
		t.Fatalf("metrics = %#v", report.Metrics)
	}
	if report.Metrics.RequestQPS <= 0 || report.Metrics.E2EP95 <= 0 || len(report.Failures) != 0 || report.ErrorCode != "" {
		t.Fatalf("report measurements = %#v", report)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"performance-secret", "messages", "choices", "request body"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("performance report leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunPerformanceReturnsRedactedFailureResponseEvidence(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.Header().Set("X-Request-Id", "req-quick-test-123")
		writer.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(writer, `{"error":{"code":"quota_exceeded","message":"quota exhausted for sk-sensitive","api_key":"sk-sensitive"}}`)
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "sk-sensitive", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Samples) != 1 || report.Samples[0].ResponseEvidence == nil {
		t.Fatalf("samples = %#v", report.Samples)
	}
	evidence := report.Samples[0].ResponseEvidence
	if evidence.CaptureStatus != PerformanceEvidenceCaptured || evidence.ContentType != "application/json" || evidence.RequestID != "req-quick-test-123" ||
		!evidence.Redacted || evidence.Truncated || evidence.BodyBytes == 0 {
		t.Fatalf("evidence = %#v", evidence)
	}
	if !strings.Contains(evidence.Body, "quota exhausted") || !strings.Contains(evidence.Body, "[REDACTED]") || strings.Contains(evidence.Body, "sk-sensitive") {
		t.Fatalf("unsafe or incomplete evidence body = %q", evidence.Body)
	}
}

func TestRunPerformanceBoundsOversizedFailureResponseEvidence(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		writer.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(writer, strings.Repeat("上游暂不可用 ", MaxPerformanceEvidenceBodyBytes))
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "sk-safe", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := report.Samples[0].ResponseEvidence
	if evidence == nil || evidence.CaptureStatus != PerformanceEvidenceCaptured || !evidence.Truncated ||
		len(evidence.Body) > MaxPerformanceEvidenceBodyBytes || evidence.BodyBytes <= uint64(len(evidence.Body)) || !utf8.ValidString(evidence.Body) {
		t.Fatalf("evidence = %#v", evidence)
	}
}

func TestRunPerformanceDoesNotExposeUndecodableTruncatedJSON(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(writer, `{"error":{"session_token":"opaque-sensitive","message":"%s`, strings.Repeat("x", MaxPerformanceEvidenceBodyBytes))
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "sk-safe", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := report.Samples[0].ResponseEvidence
	if evidence == nil || !evidence.Truncated || strings.Contains(evidence.Body, "opaque-sensitive") {
		t.Fatalf("unsafe truncated JSON evidence = %#v", evidence)
	}
}

func TestRunPerformanceBoundsJSONExpandedDuringRedaction(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(writer, `{"message":"%s"}`, strings.Repeat("<", 4_000))
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "sk-safe", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	evidence := report.Samples[0].ResponseEvidence
	if evidence == nil || !evidence.Truncated || len(evidence.Body) > MaxPerformanceEvidenceBodyBytes || !utf8.ValidString(evidence.Body) {
		t.Fatalf("expanded JSON evidence = %#v", evidence)
	}
}

func TestRunPerformanceWithProgressPublishesAuthoritativeLifecycleSnapshots(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	progress := make([]PerformanceProgress, 0)
	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformanceWithProgress(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RequestCount: 3, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 20, OutputTokens: 3,
	}, func(next PerformanceProgress) {
		progress = append(progress, next)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || len(progress) < 3 {
		t.Fatalf("report = %#v, progress = %#v", report, progress)
	}
	first, last := progress[0], progress[len(progress)-1]
	if first.Phase != load.PhaseSending || first.Planned != 3 || first.Completed != 0 {
		t.Fatalf("first progress = %#v", first)
	}
	if last.Phase != load.PhaseCompleted || last.Completed != 3 || last.Succeeded != 3 || last.Failed != 0 || last.PeakInFlight == 0 || last.TotalDurationMS <= 0 {
		t.Fatalf("last progress = %#v", last)
	}
	foundPartial := false
	foundInFlight := false
	for _, snapshot := range progress {
		if snapshot.Completed > 0 && snapshot.Completed < snapshot.Planned {
			foundPartial = true
		}
		if snapshot.InFlight > 0 {
			foundInFlight = true
		}
	}
	if !foundPartial {
		t.Fatalf("progress never exposed an intermediate completion: %#v", progress)
	}
	if !foundInFlight {
		t.Fatalf("progress never exposed in-flight work: %#v", progress)
	}
}

func TestRunPerformanceArchivesLaunchedSamplesWithStableIdentity(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3,\"prompt_tokens_details\":{\"cached_tokens\":2}}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	archive := &capturingPerformanceArchive{}
	generatedAt := time.Date(2026, time.August, 31, 15, 30, 0, 123, time.UTC)
	service := New(Dependencies{
		Transport: server.Client().Transport,
		Archive:   archive,
		Clock:     fixedPerformanceClock{now: generatedAt},
		IDFactory: func(time.Time) (string, error) { return "77777777-7777-4777-8777-777777777777", nil },
	})
	report, err := service.RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "archive-secret", ModelID: "archive-model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Archived || report.ArchiveStatus != PerformanceArchiveArchived || report.ReportID != "77777777-7777-4777-8777-777777777777" {
		t.Fatalf("archive state = %#v", report)
	}
	if report.GeneratedAt != "2026-08-31T15:30:00.000000123Z" || report.ModelID != "archive-model" {
		t.Fatalf("report identity = %#v", report)
	}
	if len(report.Samples) != 2 || report.Samples[0].RequestIndex != 0 || report.Samples[1].RequestIndex != 1 {
		t.Fatalf("samples = %#v", report.Samples)
	}
	for _, sample := range report.Samples {
		if !sample.Success || sample.E2EMS <= 0 || sample.TTFTMS <= 0 || sample.TPOTMS < 0 || sample.HTTPStatus != http.StatusOK || sample.PromptTokens != 10 || sample.CompletionTokens != 3 || sample.CachedTokens != 2 {
			t.Fatalf("sample = %#v", sample)
		}
	}
	if len(archive.saved) != 1 || archive.saved[0].ReportID != report.ReportID || !archive.saved[0].Archived {
		t.Fatalf("saved reports = %#v", archive.saved)
	}
	encoded, _ := json.Marshal(archive.saved[0])
	for _, forbidden := range []string{"archive-secret", "messages", "choices", "raw response"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("archived report leaked %q: %s", forbidden, encoded)
		}
	}
}

func TestRunPerformanceKeepsImmediateReportWhenArchiveFails(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	archive := &capturingPerformanceArchive{err: fmt.Errorf("sqlite path contains sk-do-not-leak")}
	report, err := New(Dependencies{Transport: server.Client().Transport, Archive: archive}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Archived || report.ArchiveStatus != PerformanceArchiveFailed || report.ReportID == "" || report.GeneratedAt == "" || len(report.Samples) != 1 {
		t.Fatalf("report = %#v", report)
	}
	encoded, _ := json.Marshal(report)
	if strings.Contains(string(encoded), "sk-do-not-leak") {
		t.Fatalf("archive error leaked: %s", encoded)
	}
}

func TestRunPerformanceDoesNotArchiveValidationFailure(t *testing.T) {
	archive := &capturingPerformanceArchive{}
	report, err := New(Dependencies{Archive: archive}).RunPerformance(context.Background(), PerformanceCommand{})
	if err != nil {
		t.Fatal(err)
	}
	if report.ArchiveStatus != PerformanceArchiveNotAttempted || report.ReportID != "" || report.GeneratedAt != "" || len(archive.saved) != 0 {
		t.Fatalf("report = %#v, archive = %#v", report, archive.saved)
	}
}

func TestRunPerformanceAcceptsOneMillionInputTokens(t *testing.T) {
	var calls atomic.Int64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1000000,\"completion_tokens\":1}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})
	report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 30_000, InputTokens: 1_000_000, OutputTokens: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.ErrorCode != "" || report.Profile.InputTokens != 1_000_000 || calls.Load() != 1 {
		t.Fatalf("report = %#v, transport calls = %d", report, calls.Load())
	}
}

func TestRunPerformanceRejectsUnsafeOrUnboundedProfilesBeforeTransport(t *testing.T) {
	var calls atomic.Int64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("transport must not be called")
	})
	valid := PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 10, Concurrency: 2, TimeoutMS: 30_000, InputTokens: 100, OutputTokens: 100,
	}
	tests := []struct {
		name   string
		change func(*PerformanceCommand)
	}{
		{name: "no request or duration target", change: func(command *PerformanceCommand) { command.RequestCount = 0 }},
		{name: "too many requests", change: func(command *PerformanceCommand) { command.RequestCount = MaxPerformanceRequests + 1 }},
		{name: "zero concurrency", change: func(command *PerformanceCommand) { command.Concurrency = 0 }},
		{name: "too much concurrency", change: func(command *PerformanceCommand) { command.Concurrency = MaxPerformanceConcurrency + 1 }},
		{name: "duration too long", change: func(command *PerformanceCommand) { command.DurationMS = MaxPerformanceDurationMS + 1 }},
		{name: "zero timeout", change: func(command *PerformanceCommand) { command.TimeoutMS = 0 }},
		{name: "timeout too long", change: func(command *PerformanceCommand) { command.TimeoutMS = MaxPerformanceTimeoutMS + 1 }},
		{name: "zero input tokens", change: func(command *PerformanceCommand) { command.InputTokens = 0 }},
		{name: "too many input tokens", change: func(command *PerformanceCommand) { command.InputTokens = MaxPerformanceInputTokens + 1 }},
		{name: "zero output tokens", change: func(command *PerformanceCommand) { command.OutputTokens = 0 }},
		{name: "too many output tokens", change: func(command *PerformanceCommand) { command.OutputTokens = MaxPerformanceOutputTokens + 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := valid
			test.change(&command)
			report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if report.Success || report.ErrorCode != ErrorInvalidRequest {
				t.Fatalf("report = %#v", report)
			}
			if report.Progress.Phase != PerformancePhaseNotStarted {
				t.Fatalf("progress phase = %q", report.Progress.Phase)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("transport calls = %d", calls.Load())
	}
}

func TestRunPerformanceDurationOnlyPreservesRequestedProfileAndCapsLaunches(t *testing.T) {
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})
	report, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 0, DurationMS: 1_000, Concurrency: MaxPerformanceConcurrency,
		TimeoutMS: 2_000, InputTokens: 1, OutputTokens: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Profile.RequestCount != 0 {
		t.Fatalf("reported request count = %d, want user value 0", report.Profile.RequestCount)
	}
	if report.Progress.Planned != MaxPerformanceRequests || report.Progress.Launched != MaxPerformanceRequests ||
		report.Progress.Completed != MaxPerformanceRequests || uint64(len(report.Samples)) != MaxPerformanceRequests || calls.Load() != MaxPerformanceRequests {
		t.Fatalf("bounded report progress = %#v, samples=%d calls=%d", report.Progress, len(report.Samples), calls.Load())
	}
}

func TestRunPerformanceArchivesAfterCancellationWithIndependentShortDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int64
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			cancel()
		}
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	archive := &contextCheckingPerformanceArchive{}
	report, err := New(Dependencies{Transport: transport, Archive: archive}).RunPerformance(ctx, PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 1, OutputTokens: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Archived || report.ArchiveStatus != PerformanceArchiveArchived || archive.contextErr != nil || !archive.hasDeadline {
		t.Fatalf("report = %#v, archive context err=%v deadline=%v", report, archive.contextErr, archive.hasDeadline)
	}
}

func TestRunPerformanceAggregatesStableFailureCodesWithRedactedEvidence(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(writer, `{"error":{"message":"provider mentioned sk-sensitive"}}`)
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "sk-sensitive", ModelID: "model",
		RequestCount: 2, Concurrency: 1, TimeoutMS: 2_000, InputTokens: 10, OutputTokens: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Success || report.ErrorCode != "" || report.Metrics.Failed != 2 {
		t.Fatalf("report = %#v", report)
	}
	if len(report.Failures) != 1 || report.Failures[0].ErrorCode != ErrorAuthenticationFailed || report.Failures[0].Count != 2 {
		t.Fatalf("failures = %#v", report.Failures)
	}
	encoded, _ := json.Marshal(report)
	if !strings.Contains(string(encoded), "provider mentioned") || strings.Contains(string(encoded), "sk-sensitive") {
		t.Fatalf("report did not retain safe detail or leaked a credential: %s", encoded)
	}
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

type capturingPerformanceArchive struct {
	saved []PerformanceReport
	err   error
}

func (archive *capturingPerformanceArchive) SaveQuickPerformanceReport(_ context.Context, report PerformanceReport) error {
	archive.saved = append(archive.saved, report)
	return archive.err
}

type fixedPerformanceClock struct{ now time.Time }

func (clock fixedPerformanceClock) Now() time.Time { return clock.now }

type contextCheckingPerformanceArchive struct {
	contextErr  error
	hasDeadline bool
}

func (archive *contextCheckingPerformanceArchive) SaveQuickPerformanceReport(ctx context.Context, _ PerformanceReport) error {
	archive.contextErr = ctx.Err()
	_, archive.hasDeadline = ctx.Deadline()
	return archive.contextErr
}
