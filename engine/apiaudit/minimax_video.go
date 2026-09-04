package apiaudit

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/engine/common"
)

const miniMaxVideoPollPath = "/v2/query/video_generation/"

func RunMiniMaxVideoCase(ctx context.Context, doer HTTPDoer, config RunConfig, run PlannedRun) (result CaseResult) {
	started := time.Now()
	result = CaseResult{
		ID: run.ResultID, Name: run.Case.Name, Dimension: run.Case.Dimension,
		Protocol: run.Case.Protocol, Model: run.Model, Status: StatusUnknown,
		Severity: run.Case.Severity,
	}
	if result.ID == "" {
		result.ID = run.Case.ID
	}
	if result.Severity == "" {
		result.Severity = "normal"
	}
	defer func() {
		result.ElapsedMS = time.Since(started).Milliseconds()
		if result.ElapsedMS == 0 {
			result.ElapsedMS = 1
		}
	}()

	body, err := cloneBody(run.Case.Request.Body)
	if err != nil || body == nil {
		result.Status, result.Evidence = StatusFail, "invalid request body"
		return result
	}
	if promptLength, ok := run.Case.Options["prompt_length"].(float64); ok && promptLength > 0 {
		if content, contentOK := body["content"].([]any); contentOK {
			for _, rawItem := range content {
				item, itemOK := rawItem.(map[string]any)
				if itemOK && item["type"] == "text" {
					item["text"] = strings.Repeat("测", int(promptLength))
					break
				}
			}
		}
	}
	switch modelMode, _ := run.Case.Options["model_mode"].(string); modelMode {
	case "body":
		// Preserve the case-defined value so enum and type boundaries can be exercised.
	case "omit":
		delete(body, "model")
	default:
		body["model"] = run.Model
	}
	requestConfig := config
	if omitAuthorization, _ := run.Case.Options["omit_authorization"].(bool); omitAuthorization {
		requestConfig.APIKey = ""
	}
	if invalidAuthorization, _ := run.Case.Options["invalid_authorization"].(bool); invalidAuthorization {
		requestConfig.APIKey = "invalid-api-key"
	}
	baseURL := strings.TrimRight(config.BaseURL, "/")
	path := run.Case.Request.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if config.DryRun {
		result.Evidence = "dry-run: task was not submitted"
		result.Exchanges = []HTTPExchange{{Method: http.MethodPost, URL: baseURL + path, RequestBody: body}}
		return result
	}
	if doer == nil {
		result.Status, result.Evidence = StatusFail, "HTTP client is required"
		return result
	}

	exchange, responseBody, requestErr := performRequest(ctx, doer, requestConfig, run.Case.Request, body)
	result.Exchanges = append(result.Exchanges, exchange)
	if requestErr != nil {
		result.Status, result.Evidence = StatusFail, "create request failed: "+requestErr.Error()
		return result
	}
	result.HTTPStatus = exchange.StatusCode
	if run.Case.Kind == "minimax_video_task_rejected" {
		return evaluateMiniMaxVideoRejection(result, responseBody)
	}
	if run.Case.Kind == "minimax_video_auth_rejected" {
		return evaluateMiniMaxVideoAuthorizationRejection(result, responseBody)
	}
	if exchange.StatusCode != http.StatusOK {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("task creation returned HTTP %d", exchange.StatusCode)
		return result
	}

	createResponse, parseErr := decodeMiniMaxVideoResponse(responseBody)
	if parseErr != nil {
		result.Status, result.Evidence = StatusFail, "invalid create response: "+parseErr.Error()
		return result
	}
	taskID, _ := createResponse["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		result.Status, result.Evidence = StatusFail, "create response has no task id"
		return result
	}
	if config.NoWait {
		result.Status, result.Evidence = StatusWarning, "task "+taskID+" was accepted but terminal status was not checked"
		return result
	}

	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 15 * time.Second
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	pollDefinition := RequestDefinition{Method: http.MethodGet, Path: miniMaxVideoPollPath + url.PathEscape(taskID)}
	for time.Now().Before(deadline) {
		exchange, responseBody, requestErr = performRequest(ctx, doer, config, pollDefinition, nil)
		result.Exchanges = append(result.Exchanges, exchange)
		if requestErr != nil {
			result.Status, result.Evidence = StatusFail, "poll request failed: "+requestErr.Error()
			return result
		}
		result.HTTPStatus = exchange.StatusCode
		if exchange.StatusCode != http.StatusOK {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task poll returned HTTP %d", exchange.StatusCode)
			return result
		}
		pollResponse, pollErr := decodeMiniMaxVideoResponse(responseBody)
		if pollErr != nil {
			result.Status, result.Evidence = StatusFail, "invalid poll response: "+pollErr.Error()
			return result
		}
		task, _ := pollResponse["task"].(map[string]any)
		status, _ := task["status"].(string)
		status = strings.ToLower(strings.TrimSpace(status))
		if usage, ok := task["usage"].(map[string]any); ok {
			result.Usage = usage
		}
		switch status {
		case "succeeded":
			returnedID, _ := task["id"].(string)
			returnedModel, _ := task["model"].(string)
			taskType, _ := task["task_type"].(string)
			modality, _ := task["modality"].(string)
			content, _ := task["content"].(map[string]any)
			videoURL, _ := content["url"].(string)
			parsed, validURL := validMiniMaxVideoURL(videoURL)
			if returnedID != taskID || returnedModel != run.Model || taskType != "generation" || modality != "video" || !validURL {
				result.Status, result.Evidence = StatusFail, "succeeded task has inconsistent identity or no valid video URL"
				return result
			}
			if mismatch := validateMiniMaxVideoOutputContract(task, run.Case.Options); mismatch != "" {
				result.Status, result.Evidence = StatusFail, mismatch
				return result
			}
			result.Status = StatusPass
			result.Evidence = fmt.Sprintf("task %s succeeded; video_host=%s", taskID, parsed.Hostname())
			return result
		case "failed", "cancelled":
			taskError, _ := task["error"].(map[string]any)
			code, _ := taskError["code"].(string)
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s ended with status %s; provider_code=%s", taskID, status, code)
			return result
		}
		select {
		case <-ctx.Done():
			result.Status, result.Evidence = StatusFail, "poll cancelled: "+ctx.Err().Error()
			return result
		case <-time.After(pollInterval):
		}
	}
	result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s timed out after %s", taskID, timeout)
	return result
}

func evaluateMiniMaxVideoAuthorizationRejection(result CaseResult, responseBody []byte) CaseResult {
	if result.HTTPStatus != http.StatusUnauthorized {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected authorization rejection, got HTTP %d", result.HTTPStatus)
		return result
	}
	response, err := decodeMiniMaxVideoResponse(responseBody)
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid authorization response: "+err.Error()
		return result
	}
	responseType, _ := response["type"].(string)
	detail, _ := response["error"].(map[string]any)
	errorType, _ := detail["type"].(string)
	message, _ := detail["message"].(string)
	httpCode, _ := detail["http_code"].(string)
	if responseType != "error" || errorType != "authorized_error" || strings.TrimSpace(message) == "" || httpCode != "401" {
		result.Status, result.Evidence = StatusFail, "authorization response does not match the documented error schema"
		return result
	}
	result.Status = StatusPass
	result.Evidence = "request rejected with HTTP 401 and authorized_error"
	return result
}

func validateMiniMaxVideoOutputContract(task map[string]any, options map[string]any) string {
	usage, _ := task["usage"].(map[string]any)
	if expected, ok := options["expected_resolution"].(string); ok && expected != "" {
		actual, _ := task["resolution"].(string)
		if actual != expected {
			return fmt.Sprintf("succeeded task resolution %q does not match expected %q", actual, expected)
		}
	}
	if expected, ok := miniMaxNumber(options["expected_duration"]); ok {
		actual, actualOK := miniMaxNumber(task["duration"])
		if !actualOK || actual != expected {
			return fmt.Sprintf("succeeded task duration does not match expected %v", expected)
		}
	}
	if expected, ok := options["expected_ratio"].(string); ok && expected != "" {
		actual, _ := task["ratio"].(string)
		if actual != expected {
			return fmt.Sprintf("succeeded task ratio %q does not match expected %q", actual, expected)
		}
	}
	if required, _ := options["require_video_usage"].(bool); required {
		total, totalOK := miniMaxNumber(usage["total_seconds"])
		input, inputOK := miniMaxNumber(usage["input_seconds"])
		output, outputOK := miniMaxNumber(usage["output_seconds"])
		if !totalOK || !inputOK || !outputOK || total != input+output {
			return "succeeded task has missing or inconsistent video usage seconds"
		}
	}
	if mismatch := miniMaxUsageMismatch(options, usage); mismatch != "" {
		return mismatch
	}
	return ""
}

func miniMaxUsageMismatch(options map[string]any, usage map[string]any) string {
	expected, ok := options["expected_usage"].(map[string]any)
	if !ok {
		return ""
	}
	for key, want := range expected {
		got, exists := usage[key]
		if !exists {
			return fmt.Sprintf("usage.%s is missing; expected %v", key, want)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			return fmt.Sprintf("usage.%s = %v; expected %v", key, got, want)
		}
	}
	return ""
}

func miniMaxNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func evaluateMiniMaxVideoRejection(result CaseResult, responseBody []byte) CaseResult {
	if result.HTTPStatus != http.StatusBadRequest {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected admission rejection, got HTTP %d", result.HTTPStatus)
		return result
	}
	response, err := decodeMiniMaxVideoResponse(responseBody)
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid rejection response: "+err.Error()
		return result
	}
	responseType, _ := response["type"].(string)
	detail, _ := response["error"].(map[string]any)
	errorType, _ := detail["type"].(string)
	message, _ := detail["message"].(string)
	httpCode, _ := detail["http_code"].(string)
	if responseType != "error" || errorType != "bad_request_error" || strings.TrimSpace(message) == "" || httpCode != "400" {
		result.Status, result.Evidence = StatusFail, "rejection response does not match the documented bad-request schema"
		return result
	}
	result.Status = StatusPass
	result.Evidence = "request rejected with HTTP 400 and bad_request_error"
	return result
}

func decodeMiniMaxVideoResponse(body []byte) (map[string]any, error) {
	var response map[string]any
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	return response, nil
}

func validMiniMaxVideoURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.User != nil || parsed.Hostname() == "" {
		return nil, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, false
	}
	return parsed, true
}
