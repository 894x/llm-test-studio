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

const wanTaskPollPath = "/api/v1/tasks/"

func RunWanVideoCase(ctx context.Context, doer HTTPDoer, config RunConfig, run PlannedRun) (result CaseResult) {
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
		if input, inputOK := body["input"].(map[string]any); inputOK {
			input["prompt"] = strings.Repeat("测", int(promptLength))
		}
	}
	switch modelMode, _ := run.Case.Options["model_mode"].(string); modelMode {
	case "body":
		// Preserve the case-defined value so missing type/enum boundaries can be exercised.
	case "omit":
		delete(body, "model")
	default:
		body["model"] = run.Model
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

	exchange, responseBody, requestErr := performRequest(ctx, doer, config, run.Case.Request, body)
	result.Exchanges = append(result.Exchanges, exchange)
	if requestErr != nil {
		result.Status, result.Evidence = StatusFail, "create request failed: "+requestErr.Error()
		return result
	}
	result.HTTPStatus = exchange.StatusCode
	isRejectionCase := run.Case.Kind == "wan_task_rejected"
	if isRejectionCase && exchange.StatusCode == http.StatusBadRequest {
		return evaluateWanRejection(result, responseBody)
	}
	if exchange.StatusCode < 200 || exchange.StatusCode >= 300 {
		if isRejectionCase {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected parameter rejection, got HTTP %d", exchange.StatusCode)
		} else {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task creation returned HTTP %d", exchange.StatusCode)
		}
		return result
	}

	createResponse, parseErr := decodeWanResponse(responseBody)
	if parseErr != nil {
		result.Status, result.Evidence = StatusFail, "invalid create response: "+parseErr.Error()
		return result
	}
	output, _ := createResponse["output"].(map[string]any)
	taskID, _ := output["task_id"].(string)
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
	pollDefinition := RequestDefinition{Method: http.MethodGet, Path: wanTaskPollPath + url.PathEscape(taskID)}
	for time.Now().Before(deadline) {
		exchange, responseBody, requestErr = performRequest(ctx, doer, config, pollDefinition, nil)
		result.Exchanges = append(result.Exchanges, exchange)
		if requestErr != nil {
			result.Status, result.Evidence = StatusFail, "poll request failed: "+requestErr.Error()
			return result
		}
		result.HTTPStatus = exchange.StatusCode
		if exchange.StatusCode < 200 || exchange.StatusCode >= 300 {
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task poll returned HTTP %d", exchange.StatusCode)
			return result
		}
		pollResponse, pollErr := decodeWanResponse(responseBody)
		if pollErr != nil {
			result.Status, result.Evidence = StatusFail, "invalid poll response: "+pollErr.Error()
			return result
		}
		output, _ = pollResponse["output"].(map[string]any)
		status, _ := output["task_status"].(string)
		status = strings.ToUpper(strings.TrimSpace(status))
		if usage, ok := pollResponse["usage"].(map[string]any); ok {
			result.Usage = usage
		}
		switch status {
		case "SUCCEEDED":
			if isRejectionCase {
				result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected parameter rejection, task %s succeeded", taskID)
				return result
			}
			if mismatch := wanUsageMismatch(run.Case.Options, result.Usage); mismatch != "" {
				result.Status, result.Evidence = StatusFail, mismatch
				return result
			}
			videoURL, _ := output["video_url"].(string)
			parsed, valid := validWanVideoURL(videoURL)
			if !valid {
				result.Status, result.Evidence = StatusFail, "succeeded task has no valid video URL"
				return result
			}
			result.Status = StatusPass
			result.Evidence = fmt.Sprintf("task %s succeeded; video_host=%s", taskID, parsed.Hostname())
			return result
		case "FAILED":
			if isRejectionCase {
				return evaluateWanTerminalRejection(result, taskID, output)
			}
			code, _ := output["code"].(string)
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s ended with status failed; provider_code=%s", taskID, code)
			return result
		case "CANCELED", "UNKNOWN":
			code, _ := output["code"].(string)
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s ended with status %s; provider_code=%s", taskID, strings.ToLower(status), code)
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

func evaluateWanTerminalRejection(result CaseResult, taskID string, output map[string]any) CaseResult {
	code, _ := output["code"].(string)
	message, _ := output["message"].(string)
	if strings.TrimSpace(code) == "" || strings.TrimSpace(message) == "" {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s parameter rejection requires provider code and message", taskID)
		return result
	}
	if !isWanParameterRejectionCode(code) {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s failed with unexpected provider code %s", taskID, code)
		return result
	}
	result.Status = StatusPass
	result.Evidence = fmt.Sprintf("task %s rejected parameters at terminal state with provider code %s", taskID, code)
	return result
}

func wanUsageMismatch(options map[string]any, usage map[string]any) string {
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

func evaluateWanRejection(result CaseResult, responseBody []byte) CaseResult {
	if result.HTTPStatus != http.StatusBadRequest {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("expected admission rejection, got HTTP %d", result.HTTPStatus)
		return result
	}
	response, err := decodeWanResponse(responseBody)
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid rejection response: "+err.Error()
		return result
	}
	code, _ := response["code"].(string)
	message, _ := response["message"].(string)
	if strings.TrimSpace(code) == "" || strings.TrimSpace(message) == "" {
		result.Status, result.Evidence = StatusFail, "rejection response requires provider code and message"
		return result
	}
	if !isWanParameterRejectionCode(code) {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("unexpected rejection provider code %s", code)
		return result
	}
	result.Status = StatusPass
	result.Evidence = fmt.Sprintf("request rejected with HTTP %d and provider code %s", result.HTTPStatus, code)
	return result
}

func isWanParameterRejectionCode(code string) bool {
	normalized := strings.ToLower(strings.TrimSpace(code))
	return normalized == "invalidparameter" ||
		strings.HasPrefix(normalized, "invalidparameter.") ||
		strings.HasSuffix(normalized, ".invalidparameter")
}

func validWanVideoURL(raw string) (*url.URL, bool) {
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

func decodeWanResponse(body []byte) (map[string]any, error) {
	var response map[string]any
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, err
	}
	return response, nil
}
