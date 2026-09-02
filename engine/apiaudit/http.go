package apiaudit

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/894x/llm-test-studio/engine/common"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

func performRequest(ctx context.Context, doer HTTPDoer, config RunConfig, definition RequestDefinition, body map[string]any) (HTTPExchange, []byte, error) {
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
			return HTTPExchange{}, nil, fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	method := strings.ToUpper(strings.TrimSpace(definition.Method))
	if method == "" {
		method = http.MethodPost
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return HTTPExchange{}, nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("User-Agent", "new-api-audit/1.0")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := doer.Do(request)
	if err != nil {
		return HTTPExchange{Method: method, URL: requestURL, RequestBody: body}, nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return HTTPExchange{Method: method, URL: requestURL, RequestBody: body, StatusCode: response.StatusCode}, nil, fmt.Errorf("read response: %w", err)
	}
	exchange := HTTPExchange{
		Method:       method,
		URL:          requestURL,
		RequestBody:  body,
		StatusCode:   response.StatusCode,
		ResponseBody: string(responseBody),
	}
	return exchange, responseBody, nil
}

func cloneBody(body map[string]any) (map[string]any, error) {
	if body == nil {
		return nil, nil
	}
	encoded, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	var cloned map[string]any
	if err := common.Unmarshal(encoded, &cloned); err != nil {
		return nil, err
	}
	return cloned, nil
}

func redactURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.RawQuery == "" {
		return rawURL
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	return parsed.String()
}
