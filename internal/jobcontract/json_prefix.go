package jobcontract

import "github.com/894x/llm-test-studio/internal/testspec"

func requiresJSONPrefixAssertions(assertions []testspec.Assertion) bool {
	for _, assertion := range assertions {
		switch assertion.Operator {
		case "json_prefix_valid", "json_prefix_equals":
			return true
		}
		for _, children := range [][]testspec.Assertion{assertion.All, assertion.Any, assertion.Each} {
			if requiresJSONPrefixAssertions(children) {
				return true
			}
		}
	}
	return false
}
