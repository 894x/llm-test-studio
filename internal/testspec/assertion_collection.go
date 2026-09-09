package testspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

func validTransform(transform string) bool {
	switch transform {
	case "", "trim", "json", "lower", "digits", "letter_prefix", "json_types":
		return true
	default:
		return false
	}
}

func transformValue(value any, transform string) (any, error) {
	if transform == "" {
		return value, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, errors.New("transform requires string")
	}
	switch transform {
	case "trim":
		return strings.TrimSpace(text), nil
	case "lower":
		return strings.ToLower(text), nil
	case "json":
		return decodeValue(json.RawMessage(text))
	case "json_types":
		decoded, err := decodeValue(json.RawMessage(text))
		if err != nil {
			return nil, err
		}
		object, ok := decoded.(map[string]any)
		if !ok {
			return nil, errors.New("JSON object required")
		}
		types := map[string]any{}
		for key, value := range object {
			types[key] = valueType(value)
		}
		return types, nil
	case "digits":
		return strings.Map(func(character rune) rune {
			if character >= '0' && character <= '9' {
				return character
			}
			return -1
		}, text), nil
	case "letter_prefix":
		end := 0
		for _, character := range text {
			if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') {
				break
			}
			end++
		}
		return text[:end], nil
	default:
		return nil, errors.New("unknown transform")
	}
}

func evaluateEach(assertion Assertion, observation Observation, result AssertionResult, actual any) AssertionResult {
	items, ok := actual.([]any)
	if !ok {
		result.Status = VerdictFailed
		result.Reason = "expected_array"
		return result
	}
	if len(items)*len(assertion.Each) > 4096 {
		result.Reason = "collection_evaluation_limit"
		return result
	}
	result.Status = VerdictPassed
	if assertion.EachMode == "any" { result.Status = VerdictFailed }
	for index, item := range items {
		itemStatus := VerdictPassed
		for _, child := range assertion.Each {
			entry := evaluateScoped(child, observation, item)
			entry.ID = fmt.Sprintf("%s[%d].%s", assertion.ID, index, entry.ID)
			result.Children = append(result.Children, entry)
			itemStatus = combineAll(itemStatus, entry.Status)
		}
		if assertion.EachMode != "any" { result.Status = combineAll(result.Status, itemStatus); continue }
		if result.Status == VerdictPassed || itemStatus == VerdictPassed { result.Status = VerdictPassed; continue }
		if itemStatus == VerdictIndeterminate { result.Status = VerdictIndeterminate }
	}
	return result
}

func sumEquals(actual any, expected any) (bool, error) {
	config, ok := expected.(map[string]any)
	if !ok {
		return false, errors.New("sum configuration required")
	}
	totalPointer, _ := config["total"].(string)
	parts, ok := config["parts"].([]any)
	if !ok || len(parts) == 0 {
		return false, errors.New("sum parts required")
	}
	totalValue, exists := jsonpointer.Lookup(actual, totalPointer)
	if !exists {
		return false, nil
	}
	total, number := numeric(totalValue)
	if !number {
		return false, nil
	}
	sum := float64(0)
	for _, raw := range parts {
		pointer, ok := raw.(string)
		if !ok {
			return false, errors.New("sum pointer required")
		}
		value, exists := jsonpointer.Lookup(actual, pointer)
		if !exists {
			return false, nil
		}
		part, number := numeric(value)
		if !number {
			return false, nil
		}
		sum += part
	}
	return total == sum, nil
}

func uniqueCount(actual any, expected any) (bool, error) {
	items, ok := actual.([]any)
	if !ok {
		return false, nil
	}
	config, ok := expected.(map[string]any)
	if !ok {
		return false, errors.New("unique-count configuration required")
	}
	pointer, _ := config["pointer"].(string)
	transform, _ := config["transform"].(string)
	maximum, ok := numeric(config["maximum"])
	if !ok {
		return false, errors.New("unique-count maximum required")
	}
	seen := map[string]bool{}
	for _, item := range items {
		value, exists := jsonpointer.Lookup(item, pointer)
		if !exists {
			return false, nil
		}
		value, err := transformValue(value, transform)
		if err != nil {
			return false, nil
		}
		raw, _ := json.Marshal(value)
		seen[string(raw)] = true
	}
	return float64(len(seen)) <= maximum, nil
}
