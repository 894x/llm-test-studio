package jobcontract

import "github.com/894x/llm-test-studio/internal/testspec"

func requiresAssertionFailureStatus(assertions []testspec.Assertion) bool {
	for _, assertion := range assertions {
		if assertion.FailureStatus != "" {
			return true
		}
		for _, children := range [][]testspec.Assertion{assertion.All, assertion.Any, assertion.Each} {
			if requiresAssertionFailureStatus(children) {
				return true
			}
		}
	}
	return false
}
