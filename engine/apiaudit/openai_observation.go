package apiaudit

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/engine/common"
)

type syncObservation struct {
	Exchange     HTTPExchange
	StatusCode   int
	Response     map[string]any
	ID           string
	Content      string
	Reasoning    string
	FinishReason string
	Usage        map[string]any
	Message      map[string]any
	ElapsedMS    int64
}

type streamObservation struct {
	Exchange     HTTPExchange
	StatusCode   int
	Content      string
	Reasoning    string
	FinishReason string
	FinishCount  int
	Usage        map[string]any
	Frames       int
	Done         bool
	IDs          []string
	FirstFrameMS int64
	ElapsedMS    int64
}

func observeSync(ctx context.Context, doer HTTPDoer, config RunConfig, definition RequestDefinition, body map[string]any) (syncObservation, error) {
	started := time.Now()
	exchange, responseBody, err := performRequest(ctx, doer, config, definition, body)
	observation := syncObservation{Exchange: exchange, StatusCode: exchange.StatusCode, ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		return observation, err
	}
	if len(responseBody) == 0 {
		return observation, fmt.Errorf("decode response: empty response body")
	}
	if err := common.Unmarshal(responseBody, &observation.Response); err != nil {
		return observation, fmt.Errorf("decode response: %w", err)
	}
	observation.ID, _ = observation.Response["id"].(string)
	observation.Usage, _ = observation.Response["usage"].(map[string]any)
	choices, _ := observation.Response["choices"].([]any)
	if len(choices) > 0 {
		choice, _ := choices[0].(map[string]any)
		observation.FinishReason, _ = choice["finish_reason"].(string)
		observation.Message, _ = choice["message"].(map[string]any)
		observation.Content, _ = observation.Message["content"].(string)
		observation.Reasoning, _ = observation.Message["reasoning_content"].(string)
	}
	return observation, nil
}

func observeStream(ctx context.Context, doer HTTPDoer, config RunConfig, definition RequestDefinition, body map[string]any) (streamObservation, error) {
	started := time.Now()
	baseURL := strings.TrimRight(config.BaseURL, "/")
	path := definition.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	requestURL := baseURL + path
	var reader io.Reader
	if body != nil {
		encoded, err := common.Marshal(body)
		if err != nil {
			return streamObservation{}, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	method := strings.ToUpper(strings.TrimSpace(definition.Method))
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return streamObservation{}, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("User-Agent", auditUserAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range definition.Headers {
		request.Header.Set(name, value)
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := doer.Do(request)
	if err != nil {
		return streamObservation{Exchange: HTTPExchange{Method: method, URL: requestURL, RequestBody: body}, ElapsedMS: time.Since(started).Milliseconds()}, err
	}
	defer response.Body.Close()

	observation := streamObservation{StatusCode: response.StatusCode}
	responseText := strings.Builder{}
	content := strings.Builder{}
	reasoning := strings.Builder{}
	ids := make(map[string]bool)
	limited := &io.LimitedReader{R: response.Body, N: maxAuditResponseBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	for scanner.Scan() {
		if limited.N == 0 {
			break
		}
		line := scanner.Text()
		responseText.WriteString(line)
		responseText.WriteByte('\n')
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "data:") {
			continue
		}
		if observation.Frames == 0 {
			observation.FirstFrameMS = time.Since(started).Milliseconds()
		}
		observation.Frames++
		data := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
		if data == "[DONE]" {
			observation.Done = true
			continue
		}
		var chunk map[string]any
		if err := common.Unmarshal([]byte(data), &chunk); err != nil {
			observation.Exchange = HTTPExchange{Method: method, URL: requestURL, RequestBody: body, StatusCode: response.StatusCode, ResponseBody: responseText.String()}
			observation.ElapsedMS = time.Since(started).Milliseconds()
			return observation, fmt.Errorf("decode SSE frame: %w", err)
		}
		if id, _ := chunk["id"].(string); id != "" {
			ids[id] = true
		}
		if usage, ok := chunk["usage"].(map[string]any); ok {
			observation.Usage = usage
		}
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		if finishReason, ok := choice["finish_reason"].(string); ok && finishReason != "" {
			observation.FinishReason = finishReason
			observation.FinishCount++
		}
		delta, _ := choice["delta"].(map[string]any)
		if value, ok := delta["content"].(string); ok {
			content.WriteString(value)
		}
		if value, ok := delta["reasoning_content"].(string); ok {
			reasoning.WriteString(value)
		}
	}
	observation.ElapsedMS = time.Since(started).Milliseconds()
	observation.Content = content.String()
	observation.Reasoning = reasoning.String()
	for id := range ids {
		observation.IDs = append(observation.IDs, id)
	}
	sort.Strings(observation.IDs)
	observation.Exchange = HTTPExchange{Method: method, URL: requestURL, RequestBody: body, StatusCode: response.StatusCode, ResponseBody: responseText.String()}
	if limited.N == 0 {
		return observation, fmt.Errorf("response exceeds %d-byte limit", maxAuditResponseBytes)
	}
	if err := scanner.Err(); err != nil {
		return observation, fmt.Errorf("read SSE: %w", err)
	}
	return observation, nil
}
