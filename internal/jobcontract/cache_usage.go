package jobcontract

import (
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/jsonpointer"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func requiresCacheWriteHeaders(assertions []testspec.Assertion) bool {
	for _, assertion := range assertions {
		if cacheWriteMetricPointer(assertion.Source, assertion.Pointer) {
			return true
		}
		if ref := assertion.ExpectedFrom; ref != nil && cacheWriteMetricPointer(ref.Source, ref.Pointer) {
			return true
		}
		if assertion.Operator == "sum_equals" {
			var config struct {
				Total string   `json:"total"`
				Parts []string `json:"parts"`
			}
			if json.Unmarshal(assertion.Value, &config) == nil {
				for _, pointer := range append(config.Parts, config.Total) {
					if cacheWriteMetricPointer(assertion.Source, assertion.Pointer+pointer) {
						return true
					}
				}
			}
		}
		for _, children := range [][]testspec.Assertion{assertion.All, assertion.Any, assertion.Each} {
			if requiresCacheWriteHeaders(children) {
				return true
			}
		}
	}
	return false
}

func cacheWriteMetricPointer(source, pointer string) bool {
	parts, valid := jsonpointer.Parse(pointer)
	if !valid || len(parts) == 0 {
		return false
	}
	last := parts[len(parts)-1]
	switch last {
	case protocol.MSHCacheWriteMetric5m, protocol.MSHCacheWriteMetric1h, protocol.StreamFinalUsageMetric:
	default:
		return false
	}
	if source == "metrics" {
		return len(parts) == 1
	}
	exchangeSource := source == "requests" || source == "item"
	nestedMetric := len(parts) >= 2 && parts[len(parts)-2] == "metrics"
	return exchangeSource && nestedMetric
}
