package testspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
)

func validateAssertion(assertion Assertion, depth int, seen map[string]bool) error {
	if depth > 8 || len(seen) >= 1024 {
		return errors.New("assertion tree exceeds its limit")
	}
	if !validName(assertion.ID) || seen[assertion.ID] {
		return errors.New("assertion IDs must be unique identifiers")
	}
	seen[assertion.ID] = true
	if !validTransform(assertion.Transform) {
		return errors.New("unknown assertion transform")
	}
	if len(assertion.All) > 0 || len(assertion.Any) > 0 {
		both := len(assertion.All) > 0 && len(assertion.Any) > 0
		leafFields := assertion.Source != "" || assertion.Operator != "" || assertion.Pointer != "" || len(assertion.Value) > 0
		if both || leafFields || len(assertion.Each) > 0 || assertion.ExpectedFrom != nil || assertion.Transform != "" {
			return errors.New("assertion group must contain exactly all or any without leaf fields")
		}
		children := append(append([]Assertion{}, assertion.All...), assertion.Any...)
		for _, child := range children {
			if err := validateAssertion(child, depth+1, seen); err != nil {
				return err
			}
		}
		return nil
	}
	switch assertion.Source {
	case "http.status", "response", "request", "model", "text", "stream.completed", "metrics", "usage", "task", "requests", "issues", "item":
	default:
		return errors.New("assertion source is not available in the observation contract")
	}
	if _, valid := jsonpointer.Parse(assertion.Pointer); !valid || len(assertion.Pointer) > 1024 {
		return errors.New("assertion pointer must be a valid JSON Pointer")
	}
	if len(assertion.Each) > 0 {
		if assertion.EachMode != "" && assertion.EachMode != "all" && assertion.EachMode != "any" {
			return errors.New("each_mode must be all or any")
		}
		if assertion.Operator != "" || len(assertion.Value) > 0 || assertion.ExpectedFrom != nil {
			return errors.New("each assertion cannot contain a leaf operator")
		}
		for _, child := range assertion.Each {
			if err := validateAssertion(child, depth+1, seen); err != nil {
				return err
			}
		}
		return nil
	}
	if assertion.ExpectedFrom != nil {
		if len(assertion.Value) > 0 {
			return errors.New("assertion expected_from and value are mutually exclusive")
		}
		ref := assertion.ExpectedFrom
		if !validTransform(ref.Transform) {
			return errors.New("unknown expected value transform")
		}
		if _, valid := jsonpointer.Parse(ref.Pointer); !valid {
			return errors.New("expected value pointer is invalid")
		}
		switch assertion.Operator {
		case "equals", "not_equals", "gt", "gte", "lt", "lte", "in", "contains":
			return nil
		default:
			return errors.New("this operator does not accept expected_from")
		}
	}
	switch assertion.Operator {
	case "exists", "not_exists", "non_empty", "json_valid", "http_url":
		if len(assertion.Value) != 0 {
			return errors.New("unary assertion must not contain value")
		}
	case "equals", "not_equals", "contains", "not_contains":
		if _, err := decodeValue(assertion.Value); err != nil {
			return errors.New("assertion requires a JSON value")
		}
	case "in", "not_in":
		value, err := decodeValue(assertion.Value)
		if _, ok := value.([]any); err != nil || !ok {
			return errors.New("membership assertion requires an array value")
		}
	case "gt", "gte", "lt", "lte", "length_equals", "length_gte", "length_lte":
		value, err := decodeValue(assertion.Value)
		if _, ok := numeric(value); err != nil || !ok {
			return errors.New("numeric assertion requires a numeric value")
		}
	case "type":
		var value string
		if json.Unmarshal(assertion.Value, &value) != nil {
			return errors.New("type assertion requires a JSON type name")
		}
		switch value {
		case "null", "boolean", "string", "number", "integer", "array", "object":
		default:
			return errors.New("type assertion has an unknown type name")
		}
	case "regex":
		var pattern string
		if json.Unmarshal(assertion.Value, &pattern) != nil || len(pattern) > 2048 {
			return errors.New("regex assertion requires a bounded pattern")
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return errors.New("regex assertion contains an invalid pattern")
		}
	case "sum_equals", "unique_count_lte", "unique":
		if _, err := decodeValue(assertion.Value); err != nil {
			return errors.New("aggregate operator requires configuration")
		}
	default:
		return errors.New("assertion operator is not registered")
	}
	return nil
}

func Evaluate(assertions []Assertion, observation Observation) Verdict {
	verdict := Verdict{Status: VerdictNotApplicable, Assertions: []AssertionResult{}}
	if len(assertions) == 0 {
		return verdict
	}
	verdict.Status = VerdictPassed
	for _, assertion := range assertions {
		result := evaluateOne(assertion, observation)
		verdict.Assertions = append(verdict.Assertions, result)
		verdict.Status = combineAll(verdict.Status, result.Status)
	}
	return verdict
}

func evaluateOne(assertion Assertion, observation Observation) AssertionResult {
	return evaluateScoped(assertion, observation, nil)
}

func evaluateScoped(assertion Assertion, observation Observation, item any) AssertionResult {
	result := AssertionResult{
		ID: assertion.ID, Source: assertion.Source, Pointer: assertion.Pointer,
		Operator: assertion.Operator, Expected: append(json.RawMessage{}, assertion.Value...),
		Status: VerdictIndeterminate, Children: []AssertionResult{},
	}
	if len(assertion.All) > 0 || len(assertion.Any) > 0 {
		return evaluateGroup(assertion, observation, result, item)
	}
	root, available := observationSource(observation, assertion.Source)
	if assertion.Source == "item" {
		root, available = item, true
	}
	if !available {
		result.Reason = "source_unavailable"
		return result
	}
	actual, exists := jsonpointer.Lookup(root, assertion.Pointer)
	if exists {
		result.Actual, _ = json.Marshal(actual)
	}
	if assertion.Operator == "exists" || assertion.Operator == "not_exists" {
		matched := exists
		if assertion.Operator == "not_exists" {
			matched = !exists
		}
		result.Status = matchStatus(matched)
		return result
	}
	if !exists {
		result.Status = VerdictFailed
		result.Reason = "field_missing"
		return result
	}
	if len(assertion.Each) > 0 {
		return evaluateEach(assertion, observation, result, actual)
	}
	transformed, err := transformValue(actual, assertion.Transform)
	if err != nil {
		result.Status = VerdictFailed
		result.Reason = "transform_failed"
		return result
	}
	actual = transformed
	expected, _ := decodeValue(assertion.Value)
	if ref := assertion.ExpectedFrom; ref != nil {
		expectedRoot, ok := observationSource(observation, ref.Source)
		if ref.Source == "item" {
			expectedRoot, ok = item, true
		}
		if !ok {
			result.Reason = "expected_source_unavailable"
			return result
		}
		value, found := jsonpointer.Lookup(expectedRoot, ref.Pointer)
		if !found {
			result.Status = VerdictFailed
			result.Reason = "expected_field_missing"
			return result
		}
		expected, err = transformValue(value, ref.Transform)
		if err != nil {
			result.Status = VerdictFailed
			result.Reason = "expected_transform_failed"
			return result
		}
		result.Expected, _ = json.Marshal(expected)
	}
	matched, err := compare(assertion.Operator, actual, expected)
	if err != nil {
		result.Reason = "operator_cannot_evaluate"
		return result
	}
	result.Status = matchStatus(matched)
	if !matched {
		result.Reason = "expectation_not_met"
	}
	return result
}

func evaluateGroup(assertion Assertion, observation Observation, result AssertionResult, item any) AssertionResult {
	children := assertion.All
	result.Status = VerdictPassed
	if len(assertion.Any) > 0 {
		children = assertion.Any
		result.Status = VerdictFailed
	}
	for _, child := range children {
		entry := evaluateScoped(child, observation, item)
		result.Children = append(result.Children, entry)
		if len(assertion.All) > 0 {
			result.Status = combineAll(result.Status, entry.Status)
			continue
		}
		if result.Status == VerdictPassed || entry.Status == VerdictPassed {
			result.Status = VerdictPassed
			continue
		}
		if entry.Status == VerdictIndeterminate {
			result.Status = VerdictIndeterminate
		}
	}
	return result
}

func combineAll(left VerdictStatus, right VerdictStatus) VerdictStatus {
	if left == VerdictFailed || right == VerdictFailed {
		return VerdictFailed
	}
	if left == VerdictIndeterminate || right == VerdictIndeterminate {
		return VerdictIndeterminate
	}
	return VerdictPassed
}

func matchStatus(matched bool) VerdictStatus {
	if matched {
		return VerdictPassed
	}
	return VerdictFailed
}

func observationSource(observation Observation, source string) (any, bool) {
	var value any
	switch source {
	case "model":
		value = observation.Model
	case "http.status":
		if observation.HTTPStatus == nil {
			return nil, false
		}
		value = *observation.HTTPStatus
	case "response":
		decoded, err := decodeValue(observation.Response)
		return decoded, err == nil
	case "request":
		if len(observation.Exchanges) == 0 {
			return nil, false
		}
		decoded, err := decodeValue(observation.Exchanges[len(observation.Exchanges)-1].RequestBody)
		return decoded, err == nil
	case "text":
		if observation.Text == nil {
			return nil, false
		}
		value = *observation.Text
	case "stream.completed":
		if observation.StreamCompleted == nil {
			return nil, false
		}
		value = *observation.StreamCompleted
	case "metrics":
		value = observation.Metrics
	case "usage":
		decoded, err := decodeValue(observation.Usage)
		return decoded, err == nil
	case "task":
		if observation.Task == nil {
			return nil, false
		}
		value = observation.Task
	case "requests":
		value = observation.Exchanges
	case "issues":
		value = observation.Issues
	default:
		return nil, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoded, err := decodeValue(raw)
	return decoded, err == nil
}

func compare(operator string, actual any, expected any) (bool, error) {
	switch operator {
	case "unique":
		items, ok := actual.([]any)
		if !ok {
			return false, nil
		}
		pointer, ok := expected.(string)
		if !ok {
			return false, errors.New("unique requires a pointer string")
		}
		seen := map[string]bool{}
		for _, item := range items {
			value, exists := jsonpointer.Lookup(item, pointer)
			if !exists {
				return false, nil
			}
			raw, _ := json.Marshal(value)
			key := string(raw)
			if seen[key] {
				return false, nil
			}
			seen[key] = true
		}
		return true, nil
	case "sum_equals":
		return sumEquals(actual, expected)
	case "unique_count_lte":
		return uniqueCount(actual, expected)
	case "equals":
		return jsonEqual(actual, expected), nil
	case "not_equals":
		return !jsonEqual(actual, expected), nil
	case "in", "not_in":
		values, ok := expected.([]any)
		if !ok {
			return false, errors.New("array required")
		}
		found := false
		for _, value := range values {
			if jsonEqual(actual, value) {
				found = true
				break
			}
		}
		if operator == "not_in" {
			found = !found
		}
		return found, nil
	case "contains", "not_contains":
		matched, err := contains(actual, expected)
		if operator == "not_contains" {
			matched = !matched
		}
		return matched, err
	case "non_empty":
		length, ok := lengthOf(actual)
		return ok && length > 0, nil
	case "type":
		name, ok := expected.(string)
		if !ok {
			return false, errors.New("type name required")
		}
		if name == "integer" {
			value, number := numeric(actual)
			return number && math.Trunc(value) == value, nil
		}
		return valueType(actual) == name, nil
	case "regex":
		text, ok := actual.(string)
		pattern, patternOK := expected.(string)
		if !ok || !patternOK {
			return false, nil
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return false, err
		}
		return compiled.MatchString(text), nil
	case "json_valid":
		text, ok := actual.(string)
		return ok && json.Valid([]byte(text)), nil
	case "http_url":
		text, ok := actual.(string)
		if !ok {
			return false, nil
		}
		parsed, err := url.Parse(text)
		if err != nil {
			return false, nil
		}
		return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Hostname() != "" && parsed.User == nil, nil
	default:
		return compareNumber(operator, actual, expected)
	}
}

func compareNumber(operator string, actual any, expected any) (bool, error) {
	left, ok := numeric(actual)
	if strings.HasPrefix(operator, "length_") {
		length, hasLength := lengthOf(actual)
		left, ok = float64(length), hasLength
	}
	right, expectedOK := numeric(expected)
	if !expectedOK {
		return false, errors.New("number required")
	}
	if !ok {
		return false, nil
	}
	switch operator {
	case "gt":
		return left > right, nil
	case "gte", "length_gte":
		return left >= right, nil
	case "lt":
		return left < right, nil
	case "lte", "length_lte":
		return left <= right, nil
	case "length_equals":
		return left == right, nil
	default:
		return false, errors.New("operator not registered")
	}
}

func contains(actual any, expected any) (bool, error) {
	switch value := actual.(type) {
	case string:
		fragment, ok := expected.(string)
		return ok && strings.Contains(value, fragment), nil
	case []any:
		for _, member := range value {
			if jsonEqual(member, expected) {
				return true, nil
			}
		}
		return false, nil
	case map[string]any:
		key, ok := expected.(string)
		if !ok {
			return false, nil
		}
		_, found := value[key]
		return found, nil
	default:
		return false, nil
	}
}

func numeric(value any) (float64, bool) {
	switch number := value.(type) {
	case json.Number:
		result, err := number.Float64()
		return result, err == nil && !math.IsInf(result, 0)
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	case int:
		return float64(number), true
	default:
		return 0, false
	}
}

func valueType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number, float64, int:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

func lengthOf(value any) (int, bool) {
	switch current := value.(type) {
	case string:
		return utf8.RuneCountInString(current), true
	case []any:
		return len(current), true
	case map[string]any:
		return len(current), true
	default:
		return 0, false
	}
}

func jsonEqual(left any, right any) bool {
	if a, ok := numeric(left); ok {
		b, valid := numeric(right)
		return valid && a == b
	}
	if reflect.DeepEqual(left, right) {
		return true
	}
	// Recursive JSON equality treats 1 and 1.0 as the same number in objects too.
	switch a := left.(type) {
	case []any:
		b, ok := right.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for index := range a {
			if !jsonEqual(a[index], b[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		b, ok := right.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for key, value := range a {
			other, exists := b[key]
			if !exists || !jsonEqual(value, other) {
				return false
			}
		}
		return true
	}
	return false
}

func (settings RunSettings) Validate() error {
	if settings.TimeoutMS > 3_600_000 || settings.TaskTimeoutMS > 86_400_000 || settings.PollIntervalMS > 300_000 {
		return fmt.Errorf("protocol execution settings exceed supported limits")
	}
	return nil
}
