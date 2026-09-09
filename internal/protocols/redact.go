package protocols

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/testspec"
)

const maxEvidenceValueBytes = 64 << 10
const maxEvidenceTotalBytes = 2 << 20

func sanitize(result testspec.Result, secret string) testspec.Result {
	remaining := maxEvidenceTotalBytes
	clean := func(raw json.RawMessage) json.RawMessage {
		if len(raw) == 0 {
			return nil
		}
		if len(raw) > maxEvidenceValueBytes || len(raw) > remaining {
			return json.RawMessage(`{"_evidence":"omitted_size_limit"}`)
		}
		var value any
		if json.Unmarshal(raw, &value) != nil {
			return json.RawMessage(`{"_evidence":"unparsed"}`)
		}
		value = redactValue(value, secret)
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		remaining -= len(encoded)
		return encoded
	}
	observation := &result.Observation
	observation.Response = clean(observation.Response)
	observation.Usage = clean(observation.Usage)
	if observation.Text != nil {
		text := redactText(*observation.Text, secret)
		if len(text) > maxEvidenceValueBytes {
			text = "[omitted_size_limit]"
		}
		observation.Text = &text
	}
	if observation.Task != nil {
		observation.Task.ID = redactText(observation.Task.ID, secret)
		observation.Task.Status = redactText(observation.Task.Status, secret)
	}
	for index := range observation.Artifacts {
		observation.Artifacts[index].URL = redactText(observation.Artifacts[index].URL, secret)
	}
	for index := range observation.Exchanges {
		exchange := &observation.Exchanges[index]
		exchange.Path = redactText(exchange.Path, secret)
		exchange.RequestBody = clean(exchange.RequestBody)
		exchange.Response = clean(exchange.Response)
		for event := range exchange.Events {
			exchange.Events[event] = clean(exchange.Events[event])
		}
	}
	var cleanAssertion func(*testspec.AssertionResult)
	cleanAssertion = func(assertion *testspec.AssertionResult) {
		assertion.Expected = clean(assertion.Expected)
		assertion.Actual = clean(assertion.Actual)
		for index := range assertion.Children {
			cleanAssertion(&assertion.Children[index])
		}
	}
	for index := range result.Verdict.Assertions {
		cleanAssertion(&result.Verdict.Assertions[index])
	}
	return result
}

func redactValue(value any, secret string) any {
	switch current := value.(type) {
	case map[string]any:
		for key, nested := range current {
			if credentialField(key) {
				current[key] = "[redacted]"
				continue
			}
			current[key] = redactValue(nested, secret)
		}
	case []any:
		for index, nested := range current {
			current[index] = redactValue(nested, secret)
		}
	case string:
		return redactText(current, secret)
	}
	return value
}

func credentialField(value string) bool {
	name := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, value)
	switch name {
	case "authorization", "apikey", "password", "secret", "clientsecret", "accesstoken", "refreshtoken", "cookie", "setcookie", "credential":
		return true
	default:
		return false
	}
}

func redactText(value string, secret string) string {
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[redacted]")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return value
	}
	parsed.User = nil
	query := parsed.Query()
	for key := range query {
		if credentialField(key) {
			query.Set(key, "[redacted]")
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
