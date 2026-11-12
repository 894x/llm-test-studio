package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestSequenceReplayChainAndDelayedResponseTemplateOwnTheirData(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		spec, err := testspec.Decode(json.RawMessage(`{"inputs":{},"request":{"body":{
		"nonce":{"$generate":"random_text","length":16,"alphabet":"0123456789abcdef"}}},"assertions":[],
		"workflow":{"mode":"sequence","steps":[
		{"id":"replay","reuse_request_from":"initial"},
		{"id":"chain","reuse_request_from":"replay"},
		{"id":"derived","delay_ms":1000,"request":{"body":{"reply":{"$response":"initial","pointer":"/id"}}}}
		]}}`))
		if err != nil {
			t.Fatal(err)
		}
		sequence := NewSequence(Execution{Spec: spec, Random: testspec.RandomContext{Seed: 42}})
		first, _, err := sequence.Prepare(context.Background(), testspec.Step{ID: "initial", Request: spec.Request})
		if err != nil {
			t.Fatal(err)
		}
		expected := bytes.Clone(first.Body)
		first.Body[0] = 'X' // A transport must not mutate the stored replay source.
		response := json.RawMessage(`{"id":"reply_1"}`)
		sequence.RecordResponse("initial", response)
		response[0] = 'X'
		for _, step := range spec.Workflow.Steps[:2] {
			replayed, _, err := sequence.Prepare(context.Background(), step)
			if err != nil || !bytes.Equal(replayed.Body, expected) {
				t.Fatalf("replay %s changed the first rendered body: %s, %v", step.ID, replayed.Body, err)
			}
			replayed.Body[0] = 'X'
		}
		started := time.Now()
		derived, waited, err := sequence.Prepare(context.Background(), spec.Workflow.Steps[2])
		if err != nil || string(derived.Body) != `{"reply":"reply_1"}` {
			t.Fatalf("delayed response reference failed: %s, %v", derived.Body, err)
		}
		if waited != 1000 || time.Since(started) != time.Second {
			t.Fatal("template step bypassed delay")
		}
		other := NewSequence(Execution{Spec: spec})
		if _, _, err := other.Prepare(context.Background(), spec.Workflow.Steps[0]); err == nil {
			t.Fatal("another Case execution accessed the stored request")
		}
	})
}
