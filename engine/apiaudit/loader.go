package apiaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"llm-test/engine/common"
)

var safeCaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var safeResultIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]*$`)

var supportedKinds = map[string]map[string]bool{
	"openai-chat": {
		"manual_unknown": true, "id_consistency": true, "stream_usage": true,
		"chat_stream": true, "error_schema": true, "models_contains": true,
		"response_id": true, "stop_parameter": true, "structured_json": true,
		"tool_call": true, "chat_sync": true,
		"usage_baseline": true, "stream_parity": true, "sse_integrity": true,
		"reasoning_visibility": true, "logprobs_contract": true,
		"deterministic_stability": true, "multi_turn_usage": true,
		"structured_stability": true, "concurrency_probe": true,
		"stream_ttft": true, "long_stream_stability": true,
		"cache_visibility": true, "stream_throughput": true,
		"sampling_effect": true, "parameter_boundaries": true,
		"usage_growth": true, "needle_retrieval": true,
		"error_no_usage": true, "padding_ratio": true,
	},
	"seedance": {"seedance_task": true},
}

func LoadSuite(root, suite string) ([]CaseDefinition, error) {
	if strings.TrimSpace(suite) == "" {
		return nil, fmt.Errorf("suite is required")
	}
	suiteDir := filepath.Join(root, suite)
	entries, err := os.ReadDir(suiteDir)
	if err != nil {
		return nil, fmt.Errorf("read suite %s: %w", suite, err)
	}

	cases := make([]CaseDefinition, 0, len(entries))
	seen := make(map[string]string)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(suiteDir, entry.Name())
		file, err := os.Open(filepath.Join(dir, "case.json"))
		if err != nil {
			return nil, fmt.Errorf("open case %s: %w", entry.Name(), err)
		}
		var definition CaseDefinition
		decodeErr := common.DecodeJson(file, &definition)
		closeErr := file.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode case %s: %w", entry.Name(), decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close case %s: %w", entry.Name(), closeErr)
		}
		if definition.Disabled {
			continue
		}
		definition.ID = strings.TrimSpace(definition.ID)
		definition.Name = strings.TrimSpace(definition.Name)
		definition.Dimension = strings.TrimSpace(definition.Dimension)
		definition.Protocol = strings.TrimSpace(definition.Protocol)
		definition.Kind = strings.TrimSpace(definition.Kind)
		definition.Severity = strings.TrimSpace(definition.Severity)
		if definition.Severity == "" {
			definition.Severity = "normal"
		}
		if definition.ID == "" || definition.Name == "" || definition.Dimension == "" || definition.Kind == "" {
			return nil, fmt.Errorf("case %s requires id, name, dimension, and kind", entry.Name())
		}
		if !safeCaseIDPattern.MatchString(definition.ID) {
			return nil, fmt.Errorf("unsafe case id %q in %s", definition.ID, entry.Name())
		}
		if definition.Protocol != suite {
			return nil, fmt.Errorf("case %s protocol %q does not match suite %q", definition.ID, definition.Protocol, suite)
		}
		if !supportedKinds[suite][definition.Kind] {
			return nil, fmt.Errorf("case %s has unsupported kind %q for suite %q", definition.ID, definition.Kind, suite)
		}
		definition.Request.Method = strings.ToUpper(strings.TrimSpace(definition.Request.Method))
		definition.Request.Path = strings.TrimSpace(definition.Request.Path)
		if definition.Kind != "manual_unknown" && definition.Request.Path == "" {
			return nil, fmt.Errorf("case %s request path is required", definition.ID)
		}
		if definition.Request.Path != "" && !strings.HasPrefix(definition.Request.Path, "/") {
			return nil, fmt.Errorf("case %s request path must start with /", definition.ID)
		}
		if definition.Request.Method == "" && definition.Kind != "manual_unknown" {
			definition.Request.Method = "POST"
		}
		if definition.Request.Method != "" && definition.Request.Method != "GET" && definition.Request.Method != "POST" {
			return nil, fmt.Errorf("case %s request method %q is not supported", definition.ID, definition.Request.Method)
		}
		if suite == "seedance" && definition.Request.Body == nil {
			return nil, fmt.Errorf("case %s request body is required", definition.ID)
		}
		if err := validateCaseOptions(definition); err != nil {
			return nil, err
		}
		if previous, exists := seen[definition.ID]; exists {
			return nil, fmt.Errorf("duplicate case id %s in %s and %s", definition.ID, previous, entry.Name())
		}
		seen[definition.ID] = entry.Name()
		definition.Dir = dir
		cases = append(cases, definition)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("suite %s has no case directories", suite)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func validateCaseOptions(definition CaseDefinition) error {
	stringOptions := []string{"reason", "expected_exact", "expected_digit_sequence", "stop_text", "short_prompt", "long_prompt", "needle"}
	for _, key := range stringOptions {
		if value, exists := definition.Options[key]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("case %s option %s must be a string", definition.ID, key)
			}
		}
	}
	boolOptions := []string{"require_usage", "require_content", "require_image_input", "forbid_tool_calls"}
	for _, key := range boolOptions {
		if value, exists := definition.Options[key]; exists {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("case %s option %s must be a boolean", definition.ID, key)
			}
		}
	}
	numberOptions := []string{"max_completion_tokens", "max_elapsed_ms", "min_prompt_tokens", "repetitions", "max_channels", "max_first_frame_ms"}
	for _, key := range numberOptions {
		if value, exists := definition.Options[key]; exists {
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("case %s option %s must be a number", definition.ID, key)
			}
		}
	}
	listOptions := []string{"required_substrings", "forbidden_substrings", "required_keys"}
	for _, key := range listOptions {
		value, exists := definition.Options[key]
		if !exists {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("case %s option %s must be a string array", definition.ID, key)
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("case %s option %s must be a string array", definition.ID, key)
			}
		}
	}
	if value, ok := definition.Options["repetitions"].(float64); ok && (value < 1 || value > 10 || value != float64(int(value))) {
		return fmt.Errorf("case %s option repetitions must be between 1 and 10", definition.ID)
	}
	if value, ok := definition.Options["max_first_frame_ms"].(float64); ok && value <= 0 {
		return fmt.Errorf("case %s option max_first_frame_ms must be positive", definition.ID)
	}
	return nil
}

func SelectCases(cases []CaseDefinition, ids []string, all bool) ([]CaseDefinition, error) {
	if all && len(ids) > 0 {
		return nil, fmt.Errorf("explicit case ids cannot be combined with all cases")
	}
	if all {
		return append([]CaseDefinition(nil), cases...), nil
	}
	if len(ids) == 0 {
		selected := make([]CaseDefinition, 0, len(cases))
		for _, definition := range cases {
			if definition.Default {
				selected = append(selected, definition)
			}
		}
		if len(selected) == 0 {
			return nil, fmt.Errorf("suite has no default cases")
		}
		return selected, nil
	}

	byID := make(map[string]CaseDefinition, len(cases))
	for _, definition := range cases {
		byID[definition.ID] = definition
	}
	selected := make([]CaseDefinition, 0, len(ids))
	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		definition, exists := byID[id]
		if !exists {
			return nil, fmt.Errorf("unknown case %s", id)
		}
		if !seen[id] {
			selected = append(selected, definition)
			seen[id] = true
		}
	}
	return selected, nil
}
