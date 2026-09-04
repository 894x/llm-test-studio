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

const DefaultSeedanceModel = "doubao-seedance-2-0-260128"

var DefaultSeedanceModels = []string{
	DefaultSeedanceModel,
	"doubao-seedance-2-0-fast-260128",
	"doubao-seedance-2-0-mini-260615",
}

func ExpandRuns(config RunConfig, cases []CaseDefinition) ([]PlannedRun, error) {
	models := append([]string(nil), config.Models...)
	if len(models) == 0 {
		model := strings.TrimSpace(config.Model)
		if model == "" && config.Suite == "seedance" {
			model = DefaultSeedanceModel
		}
		if model == "" {
			return nil, fmt.Errorf("model is required")
		}
		models = []string{model}
	}
	for _, model := range models {
		if strings.TrimSpace(model) == "" {
			return nil, fmt.Errorf("model list contains an empty value")
		}
	}

	runs := make([]PlannedRun, 0, len(cases)*len(models))
	for _, definition := range cases {
		for _, model := range models {
			resultID := definition.ID
			if len(models) > 1 {
				resultID += "@" + model
			}
			runs = append(runs, PlannedRun{Case: definition, Model: model, ResultID: resultID})
		}
	}
	if config.Suite == "seedance" && !config.DryRun && len(runs) > 1 && !config.ConfirmPaidSuite {
		return nil, fmt.Errorf("live Seedance plan contains %d paid tasks; pass --confirm-paid-suite", len(runs))
	}
	if config.Suite == "wan-video" && !config.DryRun && len(runs) > 0 && !config.ConfirmPaidSuite {
		return nil, fmt.Errorf("live Wan video plan contains %d paid-capable tasks; pass --confirm-paid-suite", len(runs))
	}
	if config.Suite == "minimax-video" && !config.DryRun && len(runs) > 0 && !config.ConfirmPaidSuite {
		return nil, fmt.Errorf("live MiniMax video plan contains %d paid-capable tasks; pass --confirm-paid-suite", len(runs))
	}
	return runs, nil
}

func RunSeedanceCase(ctx context.Context, doer HTTPDoer, config RunConfig, run PlannedRun) (result CaseResult) {
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
	if err != nil {
		result.Status, result.Evidence = StatusFail, "invalid request body: "+err.Error()
		return result
	}
	body["model"] = run.Model
	baseURL := strings.TrimRight(config.BaseURL, "/")
	path := run.Case.Request.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if config.DryRun {
		result.Status = StatusUnknown
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
	if exchange.StatusCode < 200 || exchange.StatusCode >= 300 {
		result.Status, result.Evidence = StatusFail, fmt.Sprintf("task creation returned HTTP %d", exchange.StatusCode)
		return result
	}
	var createResponse map[string]any
	if err := common.Unmarshal(responseBody, &createResponse); err != nil {
		result.Status, result.Evidence = StatusFail, "invalid create response: "+err.Error()
		return result
	}
	taskID, _ := createResponse["id"].(string)
	if taskID == "" {
		taskID, _ = createResponse["task_id"].(string)
	}
	if taskID == "" {
		result.Status, result.Evidence = StatusFail, "create response has no task id"
		return result
	}
	if config.NoWait {
		result.Status, result.Evidence = StatusWarning, "task "+taskID+" was accepted but terminal status was not checked"
		return result
	}

	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	pollDefinition := RequestDefinition{Method: http.MethodGet, Path: path + "/" + url.PathEscape(taskID)}
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
		var pollResponse map[string]any
		if err := common.Unmarshal(responseBody, &pollResponse); err != nil {
			result.Status, result.Evidence = StatusFail, "invalid poll response: "+err.Error()
			return result
		}
		status, _ := pollResponse["status"].(string)
		status = strings.ToLower(status)
		if usage, ok := pollResponse["usage"].(map[string]any); ok {
			result.Usage = usage
		}
		switch status {
		case "succeeded":
			videoHost := ""
			if content, ok := pollResponse["content"].(map[string]any); ok {
				if videoURL, ok := content["video_url"].(string); ok {
					if parsed, parseErr := url.Parse(videoURL); parseErr == nil {
						videoHost = parsed.Hostname()
					}
				}
			}
			result.Status, result.Evidence = StatusPass, fmt.Sprintf("task %s succeeded; video_host=%s", taskID, videoHost)
			return result
		case "failed", "cancelled", "expired":
			result.Status, result.Evidence = StatusFail, fmt.Sprintf("task %s ended with status %s", taskID, status)
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
