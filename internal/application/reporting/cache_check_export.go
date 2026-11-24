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
		switch check.ID {
		case "cache_ttl":
			prefix = "TTL 对账"
			if locale == "en-US" {
				prefix = "TTL reconciliation: "
			}
		case "cache_msh_headers":
			prefix = "MSH 响应头"
			if locale == "en-US" {
				prefix = "MSH response headers: "
			}
		}
		label := prefix + localizedVerdict(string(check.Verification.Status), locale)
		if check.PresenceOnly && check.Verification.Status == testspec.VerdictPassed {
			label = "MSH 头齐全（旧规则）"
			if locale == "en-US" {
				label = "MSH headers present (legacy check)"
			}
			rows = append(rows, htmlVerificationCheck{Label: label, Class: "muted"})
			continue
		}
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
