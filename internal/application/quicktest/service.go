package quicktest

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/credentials"
	"github.com/894x/llm-studio/internal/domain"
	"github.com/894x/llm-studio/internal/execution/load"
	"github.com/894x/llm-studio/internal/execution/openai"
)

const (
	quickTestChannelID    = "123e4567-e89b-42d3-a456-426614174090"
	quickTestCredentialID = "123e4567-e89b-42d3-a456-426614174091"
)

type Service struct {
	transport         http.RoundTripper
	allowLoopbackHTTP bool
}

func New(dependencies Dependencies) *Service {
	return &Service{
		transport:         dependencies.Transport,
		allowLoopbackHTTP: dependencies.AllowLoopbackHTTPForTesting,
	}
}

func (service *Service) Run(ctx context.Context, command Command) (Result, error) {
	if service == nil {
		return Result{}, ErrServiceUnavailable
	}
	result := Result{SchemaVersion: SchemaVersion, AddressMode: command.AddressMode}
	if ctx == nil {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}

	address, code := normalizeAddress(command.AddressMode, command.URL, service.allowLoopbackHTTP)
	result.BaseURL, result.Endpoint = address.baseURL, address.endpoint
	if code != "" {
		result.ErrorCode = code
		return result, nil
	}
	if strings.TrimSpace(command.APIKey) == "" {
		result.ErrorCode = ErrorCredentialRequired
		return result, nil
	}
	if command.APIKey != strings.TrimSpace(command.APIKey) || strings.TrimSpace(command.ModelID) == "" || command.ModelID != strings.TrimSpace(command.ModelID) {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}
	timeoutMS := command.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = DefaultTimeoutMS
	}
	if timeoutMS < 1 || timeoutMS > MaxTimeoutMS {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}
	prompt := command.Prompt
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultPrompt
	}

	runContext, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	if err := runContext.Err(); err != nil {
		result.ErrorCode = classifyContext(err)
		return result, nil
	}

	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, quickTestCredentialID)
	if err != nil {
		result.ErrorCode = load.ErrorRequestFailed
		return result, nil
	}
	store := credentials.NewMemoryStore()
	secret := []byte(command.APIKey)
	command.APIKey = ""
	if err := store.Set(runContext, storeRef, secret); err != nil {
		clear(secret)
		result.ErrorCode = classifyContext(runContext.Err())
		return result, nil
	}
	clear(secret)
	defer func() { _ = store.Delete(context.Background(), storeRef) }()

	lease, err := store.Get(runContext, storeRef)
	if err != nil {
		result.ErrorCode = classifyContext(runContext.Err())
		return result, nil
	}
	options := make([]openai.Option, 0, 1)
	if service.allowLoopbackHTTP {
		options = append(options, openai.WithLoopbackHTTPForTesting())
	}
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: quickTestChannelID, Revision: 1},
		Name:              "quick-test",
		BaseURL:           address.baseURL,
		Protocol:          domain.ProtocolOpenAIChat,
		UpstreamModelName: command.ModelID,
	}
	client, err := openai.NewClient(channel, lease, service.transport, options...)
	_ = lease.Close()
	if err != nil {
		result.ErrorCode = classifyConstruction(err)
		return result, nil
	}
	defer client.Close()

	body, err := json.Marshal(map[string]any{
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"stream":   false,
	})
	if err != nil {
		result.ErrorCode = load.ErrorRequestFailed
		return result, nil
	}
	executor, err := client.Executor(domain.TestRequest{
		Method:  domain.RequestPOST,
		Path:    "/chat/completions",
		Headers: map[string]string{},
		Body:    body,
	})
	if err != nil {
		result.ErrorCode = ErrorInvalidRequest
		return result, nil
	}

	observation := executor(runContext, load.Request{})
	result.Success = observation.Success
	result.HTTPStatus = observation.HTTPStatus
	result.E2EMS = float64(observation.E2E) / float64(time.Millisecond)
	result.PromptTokens = observation.PromptTokens
	result.CompletionTokens = observation.CompletionTokens
	result.CachedTokens = observation.CachedTokens
	result.ErrorCode = observation.ErrorCode
	if (result.HTTPStatus == http.StatusUnauthorized || result.HTTPStatus == http.StatusForbidden) && result.ErrorCode == load.ErrorHTTP {
		result.ErrorCode = ErrorAuthenticationFailed
	}
	if !result.Success && result.ErrorCode == "" {
		result.ErrorCode = load.ErrorUnclassified
	}
	return result, nil
}

type normalizedAddress struct {
	baseURL  string
	endpoint string
}

func normalizeAddress(mode AddressMode, rawURL string, allowLoopbackHTTP bool) (normalizedAddress, domain.ErrorCode) {
	if rawURL == "" || strings.TrimSpace(rawURL) != rawURL || strings.Contains(rawURL, "\\") {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Opaque != "" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.ForceQuery || parsed.RawPath != "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if !secureEndpoint(parsed, allowLoopbackHTTP) {
		return normalizedAddress{}, ErrorInsecureEndpoint
	}
	trimmed := strings.TrimRight(rawURL, "/")
	var baseURL string
	switch mode {
	case AddressModeBaseURL:
		baseURL = trimmed
	case AddressModeFullURL:
		if !strings.HasSuffix(trimmed, "/chat/completions") {
			return normalizedAddress{}, ErrorInvalidRequest
		}
		baseURL = strings.TrimSuffix(trimmed, "/chat/completions")
	default:
		return normalizedAddress{}, ErrorInvalidRequest
	}
	if baseURL == "" {
		return normalizedAddress{}, ErrorInvalidRequest
	}
	return normalizedAddress{baseURL: baseURL, endpoint: baseURL + "/chat/completions"}, ""
}

func secureEndpoint(parsed *url.URL, allowLoopbackHTTP bool) bool {
	if parsed.Scheme == "https" {
		return true
	}
	if parsed.Scheme != "http" || !allowLoopbackHTTP {
		return false
	}
	hostname := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func classifyContext(err error) domain.ErrorCode {
	if errors.Is(err, context.DeadlineExceeded) {
		return load.ErrorTimeout
	}
	if errors.Is(err, context.Canceled) {
		return load.ErrorCancelled
	}
	return load.ErrorRequestFailed
}

func classifyConstruction(err error) domain.ErrorCode {
	switch {
	case errors.Is(err, openai.ErrInsecureEndpoint):
		return ErrorInsecureEndpoint
	case errors.Is(err, openai.ErrCredentialRequired), errors.Is(err, openai.ErrCredentialUnavailable):
		return ErrorCredentialRequired
	case errors.Is(err, openai.ErrClientClosed):
		return load.ErrorClientClosed
	default:
		return ErrorInvalidRequest
	}
}
