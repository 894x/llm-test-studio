package testspec

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONPrefixAssertionsValidateConfiguration(t *testing.T) {
	for _, raw := range []string{
		`{"id":"a","source":"text","operator":"json_prefix_valid","value":"{"}`,
		`{"id":"a","source":"text","operator":"json_prefix_valid","value":""}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"prefix":"{","expected":{"answer":"PARIS"}}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"prefix":"","expected":null}}`,
	} {
		var assertion Assertion
		if err := json.Unmarshal([]byte(raw), &assertion); err != nil {
			t.Fatal(err)
		}
		if err := validateAssertion(assertion, 0, map[string]bool{}); err != nil {
			t.Errorf("valid configuration rejected: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"id":"a","source":"text","operator":"json_prefix_valid"}`,
		`{"id":"a","source":"text","operator":"json_prefix_valid","value":null}`,
		`{"id":"a","source":"text","operator":"json_prefix_valid","value":1}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"expected":{}}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"prefix":"{"}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"prefix":null,"expected":{}}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","value":{"prefix":"{","expected":{},"extra":true}}`,
		`{"id":"a","source":"text","operator":"json_prefix_equals","expected_from":{"source":"text"}}`,
	} {
		var assertion Assertion
		if err := json.Unmarshal([]byte(raw), &assertion); err != nil {
			t.Fatal(err)
		}
		if err := validateAssertion(assertion, 0, map[string]bool{}); err == nil {
			t.Errorf("invalid configuration accepted: %s", raw)
		}
	}
}

func TestJSONPrefixAssertionsParseAndCompareWithoutNormalizingText(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prefix   string
		suffix   string
		expected string
		valid    bool
		equal    bool
	}{
		{name: "object", prefix: "{", suffix: ` "answer": "PARIS" }`,
			expected: `{"answer":"PARIS"}`, valid: true, equal: true},
		{name: "field", prefix: `{"answer":`, suffix: "\n\t\"PARIS\" }\r\n",
			expected: `{"answer":"PARIS"}`, valid: true, equal: true},
		{name: "string", prefix: `{"answer":"PA`, suffix: `RIS"}`,
			expected: `{"answer":"PARIS"}`, valid: true, equal: true},
		{name: "wrong value", prefix: `{"answer":"PA`, suffix: `IRS"}`,
			expected: `{"answer":"PARIS"}`, valid: true},
		{name: "whitespace inside string", prefix: `{"answer":"PA`, suffix: ` RIS"}`,
			expected: `{"answer":"PARIS"}`, valid: true},
		{name: "equivalent escape", prefix: "{", suffix: `"answer":"\u0050ARIS"}`,
			expected: `{"answer":"PARIS"}`, valid: true, equal: true},
		{name: "key order and numbers", prefix: "{", suffix: `"b":2,"a":1.0}`,
			expected: `{"a":1,"b":2}`, valid: true, equal: true},
		{name: "extra field", prefix: "{", suffix: `"answer":"PARIS","extra":1}`,
			expected: `{"answer":"PARIS"}`, valid: true},
		{name: "missing field", prefix: "{", suffix: `}`,
			expected: `{"answer":"PARIS"}`, valid: true},
		{name: "wrong type", prefix: "{", suffix: `"answer":42}`,
			expected: `{"answer":"PARIS"}`, valid: true},
		{name: "empty suffix", prefix: "{", expected: `{"answer":"PARIS"}`},
		{name: "repeated full object", prefix: "{", suffix: `{"answer":"PARIS"}`,
			expected: `{"answer":"PARIS"}`},
		{name: "trailing value", prefix: "{", suffix: `"answer":"PARIS"} {}`,
			expected: `{"answer":"PARIS"}`},
		{name: "curly quotes", prefix: "{", suffix: `“answer”:“PARIS”}`,
			expected: `{"answer":"PARIS"}`},
		{name: "markdown", prefix: "{", suffix: "```json\n\"answer\":\"PARIS\"}\n```",
			expected: `{"answer":"PARIS"}`},
		{name: "invalid JSON whitespace", prefix: "{", suffix: "\f\"answer\":\"PARIS\"}",
			expected: `{"answer":"PARIS"}`},
		{name: "empty prefix control", suffix: `{"answer":"PARIS"}`,
			expected: `{"answer":"PARIS"}`, valid: true, equal: true},
		{name: "null value", suffix: "null", expected: "null", valid: true, equal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prefix, err := json.Marshal(tc.prefix)
			if err != nil {
				t.Fatal(err)
			}
			config := `{"prefix":` + string(prefix) + `,"expected":` + tc.expected + `}`
			for _, check := range []struct {
				operator string
				value    string
				passed   bool
			}{
				{operator: "json_prefix_valid", value: string(prefix), passed: tc.valid},
				{operator: "json_prefix_equals", value: config, passed: tc.equal},
			} {
				raw := `{"id":"a","source":"text","operator":"` + check.operator + `","value":` + check.value + `}`
				result := evaluateOneJSON(t, raw, textObservation(tc.suffix))
				want := VerdictFailed
				if check.passed {
					want = VerdictPassed
				}
				if result.Status != want {
					t.Errorf("%s: %+v, want %s", check.operator, result, want)
				}
			}
		})
	}
}

func TestJSONPrefixDecodingRejectsNonStringsAndBoundsCombinedSize(t *testing.T) {
	if _, valid := decodePrefixedJSON(map[string]any{"answer": "PARIS"}, ""); valid {
		t.Fatal("accepted an already-decoded actual value")
	}
	suffix := strings.Repeat(" ", MaxDocumentBytes-2) + "}"
	if _, valid := decodePrefixedJSON(suffix, "{"); !valid {
		t.Fatal("rejected JSON exactly at the composed-value limit")
	}
	if _, valid := decodePrefixedJSON(suffix+" ", "{"); valid {
		t.Fatal("accepted JSON exceeding the composed-value limit")
	}
}
