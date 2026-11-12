package reporting

import "github.com/894x/llm-test-studio/internal/testspec"

func verdictClass(status string) string {
	switch status {
	case "pass", "passed":
		return "pass"
	case "indeterminate":
		return "warning"
	case "observed", "not_applicable":
		return "muted"
	default:
		return "fail"
	}
}

func htmlVerificationChecks(checks []VerificationCheck, locale string) []htmlVerificationCheck {
	rows := []htmlVerificationCheck{}
	for _, check := range checks {
		prefix := "缓存读写"
		if locale == "en-US" {
			prefix = "Cache reads/writes: "
		}
		if check.ID == "cache_ttl" {
			prefix = "TTL 对账"
			if locale == "en-US" {
				prefix = "TTL reconciliation: "
			}
		}
		label := prefix + localizedVerdict(string(check.Verification.Status), locale)
		if check.ID == "cache_ttl" && check.Verification.Status == testspec.VerdictIndeterminate {
			label = "TTL 证据不足"
			if locale == "en-US" {
				label = "Insufficient TTL evidence"
			}
		}
		rows = append(rows, htmlVerificationCheck{Label: label, Class: verdictClass(string(check.Verification.Status))})
	}
	return rows
}
