package runtime

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/protocol"
)

func TestCacheWriteHeadersPreserveZeroAndRejectAmbiguousCounts(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		valid  bool
		count  float64
	}{
		{name: "zero", values: []string{"0"}, valid: true},
		{name: "positive", values: []string{"256"}, valid: true, count: 256},
		{name: "trim", values: []string{" 256 "}, valid: true, count: 256},
		{name: "exact maximum", values: []string{"9007199254740991"}, valid: true, count: 9007199254740991},
		{name: "empty", values: []string{""}},
		{name: "negative", values: []string{"-1"}},
		{name: "fraction", values: []string{"1.5"}},
		{name: "exponent", values: []string{"1e3"}},
		{name: "plus", values: []string{"+1"}},
		{name: "overflow", values: []string{"9007199254740992"}},
		{name: "duplicate", values: []string{"256", "256"}},
		{name: "combined", values: []string{"256, 256"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := NewObservation("openai-chat")
			header := http.Header{}
			for _, value := range test.values {
				header.Add(protocol.MSHCacheWriteHeader5m, value)
			}
			header.Set("Set-Cookie", "private-header-must-not-be-recorded")
			ObserveCacheWriteHeaders(&http.Response{Header: header}, &observation)
			count, present := observation.Metrics[protocol.MSHCacheWriteMetric5m]
			if present != test.valid || count != test.count {
				t.Fatalf("metric = %v, present = %v", count, present)
			}
			if (len(observation.Issues) == 0) != test.valid {
				t.Fatalf("issues = %+v", observation.Issues)
			}
			raw, err := json.Marshal(observation)
			if err != nil || strings.Contains(string(raw), "private-header") {
				t.Fatalf("unexpected recorded header: %s, %v", raw, err)
			}
			ObserveCacheWriteHeaders(&http.Response{Header: http.Header{}}, &observation)
			if _, present := observation.Metrics[protocol.MSHCacheWriteMetric5m]; present {
				t.Fatal("missing header inherited a previous step's count")
			}
		})
	}
}
