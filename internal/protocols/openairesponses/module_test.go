package openairesponses

import (
	"testing"

	"github.com/894x/llm-test-studio/internal/protocols/runtime"
)

func TestJSONSeparatesErrorEnvelopesFromSuccessfulResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		root   map[string]any
		issue  bool
	}{
		{
			name: "expected HTTP error", status: 400,
			root: map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "bad input"}},
		},
		{name: "missing error envelope", status: 400, root: map[string]any{}, issue: true},
		{
			name: "missing error message", status: 400,
			root: map[string]any{"error": map[string]any{"type": "invalid_request_error"}}, issue: true,
		},
		{name: "malformed success", status: 200, root: map[string]any{"status": "completed"}, issue: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			observation := runtime.NewObservation("openai-responses")
			observation.HTTPStatus = &test.status
			state := &decoder{}
			state.JSON(test.root, &observation)
			if (len(observation.Issues) > 0) != test.issue || len(observation.Response) == 0 {
				t.Fatalf("error envelope observation = %+v", observation)
			}
		})
	}
}
