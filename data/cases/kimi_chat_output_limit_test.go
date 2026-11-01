package casebundle_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols/openaichat"
	"github.com/894x/llm-test-studio/internal/protocols/runtime"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestKimiChatOutputLimitCases(t *testing.T) {
	for _, item := range []struct {
		directory string
		field     string
		other     string
	}{
		{directory: "K3-max-tokens-enforced", field: "max_tokens", other: "max_completion_tokens"},
		{directory: "K3-max-completion-tokens-enforced", field: "max_completion_tokens", other: "max_tokens"},
	} {
		t.Run(item.field, func(t *testing.T) {
			path := "openai-chat/" + item.directory + "/case.json"
			raw, err := fs.ReadFile(casebundle.Bundle, path)
			if err != nil {
				t.Fatal(err)
			}
			caseItem, err := casecodec.DecodeFilesystemCase(path, raw)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := caseItem.SpecFor(domain.ProtocolOpenAIChat)
			if err != nil {
				t.Fatal(err)
			}
			for _, result := range []struct {
				name   string
				finish string
				usage  any
				status int
				passed bool
			}{
				{name: "bounded truncation", finish: "length", usage: 64, status: 200, passed: true},
				{name: "exceeded", finish: "length", usage: 65, status: 200},
				{name: "zero usage", finish: "length", usage: 0, status: 200},
				{name: "missing usage", finish: "length", status: 200},
				{name: "normal stop", finish: "stop", usage: 64, status: 200},
				{name: "http error", finish: "length", usage: 64, status: 400},
			} {
				t.Run(result.name, func(t *testing.T) {
					root := map[string]any{
						"choices": []any{map[string]any{
							"finish_reason": result.finish, "message": map[string]any{"role": "assistant"},
						}},
					}
					if result.usage != nil {
						root["usage"] = map[string]any{"completion_tokens": result.usage}
					}
					fixture := &kimiResponsesFixture{status: result.status, root: root}
					observation := openaichat.New().Execute(context.Background(), runtime.Execution{
						Spec: spec,
						Inputs: testspec.ResolvedInputs{
							JSONValues: map[string]json.RawMessage{},
							Files:      map[string]testspec.FileBinding{},
						},
						Transport: fixture,
					})
					var body map[string]any
					if len(observation.Exchanges) != 1 {
						t.Fatalf("expected one exchange: %+v", observation.Issues)
					}
					if err := json.Unmarshal(observation.Exchanges[0].RequestBody, &body); err != nil {
						t.Fatal(err)
					}
					if body[item.field] != float64(64) || body[item.other] != nil {
						t.Fatalf("unexpected request budget: %+v", body)
					}
					verdict := testspec.Evaluate(spec.Assertions, observation)
					if (verdict.Status == testspec.VerdictPassed) != result.passed {
						t.Fatalf("verdict = %+v, want passed=%v", verdict, result.passed)
					}
				})
			}
		})
	}
}
