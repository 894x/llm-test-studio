package openaichat

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
)

func TestStreamFinalUsagePlacement(t *testing.T) {
	content := `{"choices":[{"index":0,"delta":{"content":"OK"}}]}`
	finish := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	usage := `{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}`
	separate := `{"choices":[],"usage":` + usage + `}`
	merged := `{"choices":[{"index":0,"delta":{"content":"OK"},"finish_reason":"stop"}],"usage":` + usage + `}`
	mergedEmpty := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":` + usage + `}`
	mergedLength := `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}],"usage":` + usage + `}`
	mergedTools := `{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":` + usage + `}`
	nullUsage := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`
	invalidUsage := `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":42}`
	early := `{"choices":[{"index":0,"delta":{"content":"OK"}}],"usage":` + usage + `}`
	for _, test := range []struct {
		name          string
		events        []string
		wantUsage     float64
		wantComplete  bool
		wantAfterEnd  float64
		wantAfterDone float64
	}{
		{name: "separate", events: []string{content, finish, separate, "[DONE]"}, wantUsage: 1, wantComplete: true},
		{name: "merged", events: []string{merged, "[DONE]"}, wantUsage: 1, wantComplete: true},
		{name: "merged empty delta", events: []string{mergedEmpty, "[DONE]"}, wantUsage: 1, wantComplete: true},
		{name: "merged length", events: []string{mergedLength, "[DONE]"}, wantUsage: 1, wantComplete: true},
		{name: "merged tools", events: []string{mergedTools, "[DONE]"}, wantUsage: 1, wantComplete: true},
		{name: "missing", events: []string{content, finish, "[DONE]"}, wantComplete: true},
		{name: "early only", events: []string{early, finish, "[DONE]"}, wantComplete: true},
		{name: "usage before finish", events: []string{separate, finish, "[DONE]"}, wantComplete: true},
		{name: "null usage", events: []string{nullUsage, "[DONE]"}, wantComplete: true},
		{name: "invalid usage", events: []string{invalidUsage, "[DONE]"}, wantComplete: true},
		{
			name: "duplicate separate", events: []string{content, finish, separate, separate, "[DONE]"},
			wantUsage: 2, wantComplete: true,
		},
		{name: "merged and separate", events: []string{merged, separate, "[DONE]"}, wantUsage: 2, wantComplete: true},
		{name: "merged interrupted", events: []string{merged}, wantUsage: 1},
		{
			name: "content after merged", events: []string{merged, content, "[DONE]"},
			wantUsage: 1, wantComplete: true, wantAfterEnd: 1,
		},
		{
			name: "data after done", events: []string{merged, "[DONE]", separate},
			wantUsage: 2, wantComplete: true, wantAfterDone: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stream strings.Builder
			for _, event := range test.events {
				stream.WriteString("data: " + event + "\n\n")
			}
			observation := runtime.NewObservation("openai-chat")
			parseStream(runtime.Response{
				Started: time.Now(),
				HTTP:    &http.Response{Body: io.NopCloser(strings.NewReader(stream.String()))},
			}, &observation)
			if got := observation.Metrics[protocol.StreamFinalUsageMetric]; got != test.wantUsage {
				t.Fatalf("final usage count = %v, want %v", got, test.wantUsage)
			}
			if observation.StreamCompleted == nil || *observation.StreamCompleted != test.wantComplete {
				t.Fatalf("stream completion = %v, want %v", observation.StreamCompleted, test.wantComplete)
			}
			if got := observation.Metrics["stream_content_after_finish"]; got != test.wantAfterEnd {
				t.Fatalf("content after finish = %v, want %v", got, test.wantAfterEnd)
			}
			if got := observation.Metrics["stream_data_after_done"]; got != test.wantAfterDone {
				t.Fatalf("data after DONE = %v, want %v", got, test.wantAfterDone)
			}
			if test.wantUsage == 1 && !json.Valid(observation.Usage) {
				t.Fatalf("final usage was not preserved: %s", observation.Usage)
			}
		})
	}
}
