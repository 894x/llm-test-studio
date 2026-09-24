package quicktest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func TestPerformanceRejectsInvalidOrUnsafeConnectionsBeforeTransport(t *testing.T) {
	var calls atomic.Int64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(2 * time.Millisecond)
		return nil, fmt.Errorf("transport must not be called")
	})
	tests := []struct {
		name    string
		command PerformanceCommand
		code    string
	}{
		{name: "unknown mode", command: validConnectionCommand(), code: string(ErrorInvalidRequest)},
		{name: "query", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.URL += "?key=secret" }), code: string(ErrorInvalidRequest)},
		{name: "fragment", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.URL += "#fragment" }), code: string(ErrorInvalidRequest)},
		{name: "userinfo", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.URL = "https://user:pass@example.com/v1" }), code: string(ErrorInvalidRequest)},
		{name: "wrong full endpoint", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) {
			command.AddressMode = AddressModeFullURL
			command.URL = "https://example.com/v1/responses"
		}), code: string(ErrorInvalidRequest)},
		{name: "missing key", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.APIKey = "" }), code: string(ErrorCredentialRequired)},
		{name: "missing model", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.ModelID = "" }), code: string(ErrorInvalidRequest)},
		{name: "zero timeout", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.TimeoutMS = 0 }), code: string(ErrorInvalidRequest)},
		{name: "excessive timeout", command: withConnectionCommand(validConnectionCommand(), func(command *PerformanceCommand) { command.TimeoutMS = MaxPerformanceTimeoutMS + 1 }), code: string(ErrorInvalidRequest)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "unknown mode" {
				test.command.AddressMode = "unknown"
			}
			result, err := New(Dependencies{Transport: transport}).RunPerformance(context.Background(), test.command)
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

func TestRunPerformanceUsesTheTestedConnectionAndReturnsABoundedReport(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		if body.Messages[0].Content != strings.TrimSpace(strings.Repeat("test ", 20)) {
			t.Errorf("fixed prompt changed with random input disabled: %q", body.Messages[0].Content)
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

func TestRunPerformanceSupportsRateControlledOpenLoop(t *testing.T) {
	var mu sync.Mutex
	startedAt := make([]time.Time, 0, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		startedAt = append(startedAt, time.Now())
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		LoadMode: domain.LoadOpenLoop, RatePerSecond: 50, RequestCount: 3, MaxInFlight: 3,
		TimeoutMS: 2_000, InputTokens: 20, OutputTokens: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Profile.LoadMode != domain.LoadOpenLoop || report.Profile.RatePerSecond != 50 || report.Profile.MaxInFlight != 3 {
		t.Fatalf("report = %#v", report)
	}
	if report.Metrics.OfferedQPS <= 0 || report.Metrics.LaunchedQPS <= 0 || report.Metrics.SuccessfulRequestQPS <= 0 || report.Metrics.CompletedQPS <= 0 {
		t.Fatalf("throughput metrics = %#v", report.Metrics)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(startedAt) != 3 {
		t.Fatalf("request starts = %d, want 3", len(startedAt))
	}
	if gap := startedAt[1].Sub(startedAt[0]); gap < 8*time.Millisecond {
		t.Fatalf("open-loop request gap = %v, want scheduled pacing", gap)
	}
}

func TestRunPerformanceNormalWorkloadRecordsTargetsWithoutPromptLeakage(t *testing.T) {
	type receivedRequest struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		MaxTokens uint32 `json:"max_tokens"`
	}
	var mu sync.Mutex
	received := make([]receivedRequest, 0, 3)
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body receivedRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		received = append(received, body)
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	report, err := New(Dependencies{Transport: server.Client().Transport}).RunPerformance(context.Background(), PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: server.URL, APIKey: "secret", ModelID: "model",
		RequestCount: 3, Concurrency: 1, TimeoutMS: 2_000,
		WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 91,
		InputTokens: 20, InputTokensStdDev: 4, SharedPrefixTokens: 5,
		OutputTokens: 8, OutputTokensStdDev: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Profile.WorkloadMode != PerformanceWorkloadNormal || report.Profile.ArrivalPattern != load.ArrivalConstant {
		t.Fatalf("report profile = %#v", report.Profile)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != len(report.Samples) || len(received) != 3 {
		t.Fatalf("requests/samples = %d/%d", len(received), len(report.Samples))
	}
	for index, sample := range report.Samples {
		if sample.TargetInputTokens == 0 || sample.TargetOutputTokens == 0 || len(received[index].Messages) != 1 {
			t.Fatalf("sample/request %d = %#v / %#v", index, sample, received[index])
		}
		if got := len(strings.Fields(received[index].Messages[0].Content)); got != int(sample.TargetInputTokens) {
			t.Fatalf("request %d words = %d, target = %d", index, got, sample.TargetInputTokens)
		}
		if received[index].MaxTokens != sample.TargetOutputTokens {
			t.Fatalf("request %d max_tokens = %d, target = %d", index, received[index].MaxTokens, sample.TargetOutputTokens)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range received {
		if strings.Contains(string(encoded), request.Messages[0].Content) {
			t.Fatalf("report leaked generated prompt: %s", encoded)
		}
	}
}

func TestPerformanceInputBudgetGatesDynamicRunsWithoutTouchingFixedFastPath(t *testing.T) {
	releaseBudget, err := sharedPerformanceInputTokenBudget.acquire(context.Background(), uint32(maxPerformanceInputTokensInFlight))
	if err != nil {
		t.Fatal(err)
	}
	defer releaseBudget()
	var calls atomic.Uint64
	transport := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n" +
					"data: [DONE]\n\n",
			)),
		}, nil
	})
	service := New(Dependencies{Transport: transport})
	command := PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "secret", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 500, InputTokens: 10, OutputTokens: 2,
	}
	fixed, err := service.RunPerformance(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !fixed.Success || calls.Load() != 1 {
		t.Fatalf("fixed report = %#v, transport calls = %d", fixed, calls.Load())
	}

	command.WorkloadMode = PerformanceWorkloadNormal
	command.RandomSeed = 1
	command.TimeoutMS = 20
	blocked, err := service.RunPerformance(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Success || len(blocked.Samples) != 1 || !blocked.Samples[0].TimedOut || calls.Load() != 1 {
		t.Fatalf("blocked dynamic report = %#v, transport calls = %d", blocked, calls.Load())
	}

	releaseBudget()
	command.TimeoutMS = 2_000
	dynamic, err := service.RunPerformance(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if !dynamic.Success || calls.Load() != 2 {
		t.Fatalf("released dynamic report = %#v, transport calls = %d", dynamic, calls.Load())
	}
}

func TestRunPerformanceRejectsInvalidLoadModeCombinations(t *testing.T) {
	base := PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com", APIKey: "secret", ModelID: "model",
		RequestCount: 3, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 20, OutputTokens: 3,
	}
	for name, mutate := range map[string]func(*PerformanceCommand){
		"open loop without rate": func(command *PerformanceCommand) {
			command.LoadMode = domain.LoadOpenLoop
			command.Concurrency = 0
			command.MaxInFlight = 2
		},
		"fixed concurrency with rate": func(command *PerformanceCommand) { command.RatePerSecond = 10 },
		"duration schedule exceeds sample cap": func(command *PerformanceCommand) {
			command.LoadMode = domain.LoadOpenLoop
			command.RequestCount = 0
			command.DurationMS = MaxPerformanceDurationMS
			command.Concurrency = 0
			command.RatePerSecond = 100
			command.MaxInFlight = 2
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := base
			mutate(&command)
			report, err := New(Dependencies{}).RunPerformance(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if report.ErrorCode != ErrorInvalidRequest {
				t.Fatalf("error code = %q, want %q", report.ErrorCode, ErrorInvalidRequest)
			}
		})
	}
}

func TestRunPerformanceRejectsInvalidArrivalAndWorkloadCombinations(t *testing.T) {
	base := PerformanceCommand{
		AddressMode: AddressModeBaseURL, URL: "https://example.com", APIKey: "secret", ModelID: "model",
		RequestCount: 3, Concurrency: 2, TimeoutMS: 2_000, InputTokens: 20, OutputTokens: 3,
	}
	for name, mutate := range map[string]func(*PerformanceCommand){
		"poisson requires open loop": func(command *PerformanceCommand) {
			command.ArrivalPattern = load.ArrivalPoisson
			command.RandomSeed = 1
		},
		"unknown arrival":                      func(command *PerformanceCommand) { command.ArrivalPattern = "bursty" },
		"fixed workload rejects stddev":        func(command *PerformanceCommand) { command.InputTokensStdDev = 1 },
		"fixed workload rejects shared prefix": func(command *PerformanceCommand) { command.SharedPrefixTokens = 1 },
		"unused seed":                          func(command *PerformanceCommand) { command.RandomSeed = 1 },
		"unknown workload":                     func(command *PerformanceCommand) { command.WorkloadMode = "trace" },
		"normal workload requires seed": func(command *PerformanceCommand) {
			command.WorkloadMode = PerformanceWorkloadNormal
		},
		"poisson arrival requires seed": func(command *PerformanceCommand) {
			command.LoadMode = domain.LoadOpenLoop
			command.ArrivalPattern = load.ArrivalPoisson
			command.Concurrency = 0
			command.RatePerSecond = 10
			command.MaxInFlight = 2
		},
		"input stddev exceeds mean": func(command *PerformanceCommand) {
			command.WorkloadMode = PerformanceWorkloadNormal
			command.RandomSeed = 1
			command.InputTokensStdDev = command.InputTokens + 1
		},
		"output stddev exceeds mean": func(command *PerformanceCommand) {
			command.WorkloadMode = PerformanceWorkloadNormal
			command.RandomSeed = 1
			command.OutputTokensStdDev = command.OutputTokens + 1
		},
		"shared prefix consumes input": func(command *PerformanceCommand) {
			command.WorkloadMode = PerformanceWorkloadNormal
			command.RandomSeed = 1
			command.SharedPrefixTokens = command.InputTokens
		},
		"poisson duration lacks headroom": func(command *PerformanceCommand) {
			command.LoadMode = domain.LoadOpenLoop
			command.ArrivalPattern = load.ArrivalPoisson
			command.RequestCount = 0
			command.DurationMS = 1_000
			command.Concurrency = 0
			command.RatePerSecond = 6_000
			command.MaxInFlight = 2
		},
	} {
		t.Run(name, func(t *testing.T) {
			command := base
			mutate(&command)
			report, err := New(Dependencies{}).RunPerformance(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if report.ErrorCode != ErrorInvalidRequest {
				t.Fatalf("error code = %q, want %q; profile = %#v", report.ErrorCode, ErrorInvalidRequest, report.Profile)
			}
		})
	}
}

func TestValidPerformanceProfileAcceptsNormalStddevAtMeanBoundary(t *testing.T) {
	command := PerformanceCommand{
		LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: load.ArrivalConstant,
		WorkloadMode: PerformanceWorkloadNormal, RandomSeed: 1,
		RequestCount: 1, Concurrency: 1, TimeoutMS: 1_000,
		InputTokens: 10, InputTokensStdDev: 10,
		OutputTokens: 4, OutputTokensStdDev: 4,
	}
	if !validPerformanceProfile(command) {
		t.Fatalf("boundary profile rejected: %#v", command)
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

func validConnectionCommand() PerformanceCommand {
	return PerformanceCommand{AddressMode: AddressModeBaseURL, URL: "https://example.com/v1", APIKey: "sk-private", ModelID: "test-model", RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 2, OutputTokens: 2}
}

func withConnectionCommand(command PerformanceCommand, change func(*PerformanceCommand)) PerformanceCommand {
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
