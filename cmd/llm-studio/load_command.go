package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
	"github.com/894x/llm-studio/internal/execution/openai"
)

const loadUsage = `Usage: llm-studio load run [options]

Options:
  --url URL                    Full OpenAI-compatible endpoint (or LOADTEST_URL)
  --model NAME                Upstream model name (or LOADTEST_MODEL)
  --api-key-env NAME          Environment variable containing the API key
  --requests N                Number of requests (default 1)
  --concurrency N             Maximum fixed concurrency (default 1)
  --rate N                    Open-loop requests per second
  --duration DURATION         Sending window, such as 60s
  --timeout DURATION          Per-request timeout (default 60s)
  --request-file PATH         JSON body or case.json to execute
  --stream                    Request an SSE response
  --input-tokens N            Approximate generated prompt tokens
  --max-tokens N              Maximum completion tokens
  --output PATH               Optional JSON result path
  --format json|human         Standard output format
  --allow-insecure-loopback   Permit HTTP only for explicit localhost testing
`

type loadRunResponse struct {
	SchemaVersion int            `json:"schema_version"`
	Type          string         `json:"type"`
	Payload       loadRunPayload `json:"payload"`
}

type loadRunPayload struct {
	URL      string             `json:"url"`
	Model    string             `json:"model"`
	Profile  domain.LoadProfile `json:"profile"`
	Progress load.Progress      `json:"progress"`
	Metrics  load.Metrics       `json:"metrics"`
	Results  []load.Observation `json:"results"`
}

func runLoad(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	if len(args) == 0 {
		return diagnosticExit(stderr, "json", "usage_error", "a load command is required", 2)
	}
	switch args[0] {
	case "help", "-h", "--help":
		return writeUsage(stdout, stderr, loadUsage)
	case "run":
		return runLoadRun(nonNilContext(ctx), args[1:], stdout, stderr, dependencies)
	default:
		return diagnosticExit(stderr, "json", "usage_error", "unknown load command", 2)
	}
}

func runLoadRun(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	flags := flag.NewFlagSet("load run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	defaultURL, defaultModel := "", ""
	if dependencies.getenv != nil {
		defaultURL, defaultModel = dependencies.getenv("LOADTEST_URL"), dependencies.getenv("LOADTEST_MODEL")
	}
	endpoint := flags.String("url", defaultURL, "full OpenAI-compatible endpoint")
	model := flags.String("model", defaultModel, "upstream model")
	keyEnv := flags.String("api-key-env", "LOADTEST_API_KEY", "environment variable containing the API key")
	requests := flags.Uint64("requests", 1, "number of requests")
	concurrency := flags.Uint("concurrency", 1, "maximum fixed concurrency")
	rate := flags.Float64("rate", 0, "open-loop requests per second")
	duration := flags.Duration("duration", 0, "sending window")
	timeout := flags.Duration("timeout", 60*time.Second, "per-request timeout")
	requestFile := flags.String("request-file", "", "JSON request body or case.json")
	stream := flags.Bool("stream", false, "request an SSE response")
	inputTokens := flags.Uint("input-tokens", 100, "approximate prompt tokens")
	maxTokens := flags.Uint("max-tokens", 100, "maximum completion tokens")
	output := flags.String("output", "", "optional JSON result path")
	format := flags.String("format", "json", "output format: json or human")
	allowLoopback := flags.Bool("allow-insecure-loopback", false, "permit HTTP only for localhost testing")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeUsage(stdout, stderr, loadUsage)
		}
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid load run options", 2)
	}
	if flags.NArg() != 0 || (*format != "json" && *format != "human") {
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid load run options", 2)
	}
	requestsExplicit := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "requests" {
			requestsExplicit = true
		}
	})
	if !validEnvironmentName(*keyEnv) {
		return diagnosticExit(stderr, *format, "config_error", "API key environment variable name is invalid", 2)
	}
	secret := ""
	if dependencies.getenv != nil {
		secret = strings.TrimSpace(dependencies.getenv(*keyEnv))
		if secret == "" && *keyEnv == "LOADTEST_API_KEY" {
			secret = strings.TrimSpace(dependencies.getenv("KIMI_K3_API_KEY"))
		}
	}
	if secret == "" {
		return diagnosticExit(stderr, *format, "missing_credential", "configured API key is required for a live run", 2)
	}
	baseURL, requestPath, err := splitLoadEndpoint(*endpoint)
	if err != nil || strings.TrimSpace(*model) == "" || *concurrency == 0 || *concurrency > load.MaxConcurrency || *duration < 0 || *timeout <= 0 {
		return diagnosticExit(stderr, *format, "config_error", "load configuration is invalid", 2)
	}
	body, requestPath, err := loadRequestBody(*requestFile, requestPath, *model, *stream, *inputTokens, *maxTokens)
	if err != nil {
		return diagnosticExit(stderr, *format, "config_error", "load request definition is invalid", 2)
	}
	requestPath = relativeLoadRequestPath(baseURL, requestPath)
	effectiveRequests := effectiveLoadRequestCount(*requests, requestsExplicit, *rate, *duration)
	profile, err := loadProfile(effectiveRequests, uint32(*concurrency), *rate, *duration, *timeout)
	if err != nil {
		return diagnosticExit(stderr, *format, "config_error", "load configuration is invalid", 2)
	}
	outcome, err := executeCLILoad(ctx, baseURL, requestPath, strings.TrimSpace(*model), secret, body, profile, *allowLoopback)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return diagnosticExit(stderr, *format, "run_error", "load run failed", 1)
	}
	response := loadRunResponse{SchemaVersion: 1, Type: "load_run", Payload: loadRunPayload{
		URL: *endpoint, Model: strings.TrimSpace(*model), Profile: profile,
		Progress: outcome.Progress, Metrics: outcome.Metrics, Results: outcome.Results,
	}}
	encoded, marshalErr := json.MarshalIndent(response, "", "  ")
	if marshalErr != nil {
		return diagnosticExit(stderr, *format, "output_error", "load result could not be encoded", 1)
	}
	if *output != "" {
		if err := writeLoadOutput(*output, append(encoded, '\n')); err != nil {
			return diagnosticExit(stderr, *format, "output_error", "load result could not be written", 1)
		}
	}
	if *format == "human" {
		text := fmt.Sprintf("LOAD %d/%d passed, %.2f req/s, E2E P95 %.2f ms\n", outcome.Metrics.Succeeded, outcome.Metrics.Completed, outcome.Metrics.RequestQPS, outcome.Metrics.E2EP95)
		if *output != "" {
			text += "REPORT " + filepath.Clean(*output) + "\n"
		}
		if err := writeAll(stdout, []byte(text)); err != nil {
			return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
		}
	} else if err := writeAll(stdout, append(encoded, '\n')); err != nil {
		return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 1
	}
	if outcome.Metrics.Failed > 0 {
		return 1
	}
	return 0
}

func splitLoadEndpoint(value string) (string, string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return "", "", errors.New("invalid endpoint")
	}
	path := strings.TrimRight(parsed.EscapedPath(), "/")
	const suffix = "/chat/completions"
	if strings.HasSuffix(path, suffix) {
		basePath := strings.TrimSuffix(path, suffix)
		parsed.Path, parsed.RawPath = basePath, ""
		return strings.TrimRight(parsed.String(), "/"), suffix, nil
	}
	return "", "", errors.New("unsupported endpoint path")
}

func effectiveLoadRequestCount(requests uint64, requestsExplicit bool, rate float64, duration time.Duration) uint64 {
	if rate > 0 && duration > 0 && !requestsExplicit {
		return 0
	}
	return requests
}

func relativeLoadRequestPath(baseURL, requestPath string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return requestPath
	}
	basePath := strings.TrimRight(parsed.EscapedPath(), "/")
	if basePath != "" && (requestPath == basePath || strings.HasPrefix(requestPath, basePath+"/")) {
		trimmed := strings.TrimPrefix(requestPath, basePath)
		if trimmed == "" {
			return "/"
		}
		return trimmed
	}
	return requestPath
}

func loadRequestBody(filename, defaultPath, model string, stream bool, inputTokens, maxTokens uint) (json.RawMessage, string, error) {
	if filename != "" {
		contents, err := os.ReadFile(filepath.Clean(filename))
		if err != nil {
			return nil, "", err
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(contents, &document); err != nil {
			return nil, "", err
		}
		if requestRaw, ok := document["request"]; ok {
			var request struct {
				Path string          `json:"path"`
				Body json.RawMessage `json:"body"`
			}
			if err := json.Unmarshal(requestRaw, &request); err != nil || len(request.Body) == 0 {
				return nil, "", errors.New("invalid case request")
			}
			if request.Path != "" {
				defaultPath = request.Path
			}
			patched, err := patchLoadBody(request.Body, model, stream, maxTokens)
			return patched, defaultPath, err
		}
		patched, err := patchLoadBody(contents, model, stream, maxTokens)
		return patched, defaultPath, err
	}
	prompt := strings.TrimSpace(strings.Repeat("test ", int(inputTokens)))
	body, err := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": maxTokens, "stream": stream,
	})
	return body, defaultPath, err
}

func patchLoadBody(body json.RawMessage, model string, stream bool, maxTokens uint) (json.RawMessage, error) {
	var object map[string]any
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	object["model"], object["stream"] = model, stream
	if maxTokens > 0 {
		object["max_tokens"] = maxTokens
	}
	encoded, err := json.Marshal(object)
	return encoded, err
}

func loadProfile(requests uint64, concurrency uint32, rate float64, duration, timeout time.Duration) (domain.LoadProfile, error) {
	mode := domain.LoadFixedConcurrency
	if rate > 0 {
		mode = domain.LoadOpenLoop
	} else if requests == 1 && concurrency == 1 && duration == 0 {
		mode = domain.LoadSingle
	}
	profile := domain.LoadProfile{Mode: mode, Concurrency: concurrency, RequestCount: requests, RatePerSecond: rate, DurationMS: uint64(duration / time.Millisecond), RequestTimeoutMS: uint64(timeout / time.Millisecond)}
	return profile, profile.Validate()
}

func executeCLILoad(ctx context.Context, baseURL, requestPath, model, secret string, body json.RawMessage, profile domain.LoadProfile, allowLoopback bool) (load.Outcome, error) {
	ref, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, "10000000-0000-4000-8000-000000000001")
	if err != nil {
		return load.Outcome{}, err
	}
	store := credentials.NewMemoryStore()
	secretBytes := []byte(secret)
	if err := store.Set(ctx, ref, secretBytes); err != nil {
		clear(secretBytes)
		return load.Outcome{}, err
	}
	clear(secretBytes)
	lease, err := store.Get(ctx, ref)
	if err != nil {
		return load.Outcome{}, err
	}
	defer lease.Close()
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: "10000000-0000-4000-8000-000000000002", Revision: 1},
		Name:              "CLI", BaseURL: baseURL, Protocol: domain.ProtocolOpenAIChat, UpstreamModelName: model,
	}
	options := []openai.Option{}
	if allowLoopback {
		options = append(options, openai.WithLoopbackHTTPForTesting())
	}
	client, err := openai.NewClient(channel, lease, nil, options...)
	if err != nil {
		return load.Outcome{}, err
	}
	defer client.Close()
	executor, err := client.Executor(domain.TestRequest{Method: domain.RequestPOST, Path: requestPath, Headers: map[string]string{}, Body: body})
	if err != nil {
		return load.Outcome{}, err
	}
	return load.Run(ctx, profile, executor, load.Options{})
}

func writeLoadOutput(filename string, contents []byte) error {
	cleaned := filepath.Clean(filename)
	directory := filepath.Dir(cleaned)
	if directory != "." {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return err
		}
	}
	temporary, err := os.CreateTemp(directory, ".llm-studio-report-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceOutputFile(temporaryName, cleaned); err != nil {
		return err
	}
	committed = true
	return nil
}
