package runtime

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// ObserveCacheWriteHeaders keeps only the two public numeric usage counters.
// Missing headers stay missing; a later workflow step cannot inherit a value.
func ObserveCacheWriteHeaders(response *http.Response, observation *testspec.Observation) {
	for _, field := range []struct{ header, metric string }{
		{header: protocol.MSHCacheWriteHeader5m, metric: protocol.MSHCacheWriteMetric5m},
		{header: protocol.MSHCacheWriteHeader1h, metric: protocol.MSHCacheWriteMetric1h},
	} {
		delete(observation.Metrics, field.metric)
		if response == nil {
			continue
		}
		values := response.Header.Values(field.header)
		if len(values) == 0 {
			continue
		}
		value := strings.TrimSpace(values[0])
		digits := value != "" && len(value) <= 16
		for _, character := range value {
			decimal := character >= '0' && character <= '9'
			digits = digits && decimal
		}
		count, err := strconv.ParseUint(value, 10, 53)
		invalidValue := !digits || err != nil
		if len(values) != 1 || invalidValue {
			AddIssue(observation, "headers", "invalid_cache_write_header")
			continue
		}
		observation.Metrics[field.metric] = float64(count)
	}
}
