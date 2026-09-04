package runs_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

func TestInputLatencyLadderExecutorWarmsEachStageAndEmitsOnlyMeasuredSamples(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	var contents []string
	var firstRoles []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		mu.Lock()
		requestCount++
		if len(body.Messages) > 0 {
			firstRoles = append(firstRoles, body.Messages[0].Role)
			contents = append(contents, body.Messages[len(body.Messages)-1].Content)
		}
		mu.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":2}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	fixture := newRunFixture(t)
	fixture.channel.BaseURL = server.URL
	spec, err := json.Marshal(casetypes.InputLatencyLadderSpec{
		Request: domain.TestRequest{Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{}, Body: json.RawMessage(`{"messages":[{"role":"system","content":"keep this instruction"},{"role":"user","content":"placeholder"}]}`)},
		Stages: []casetypes.InputLatencyStage{
			{InputTokens: 8},
			{InputTokens: 16, Warmups: pointerToUint32(0), Samples: pointerToUint32(1)},
		},
		WarmupsPerStep: 1, SamplesPerStep: 2,
		OutputTokens: 4, TimeoutMS: 1000, CacheMode: casetypes.CacheModeCold,
	})
	if err != nil {
		t.Fatal(err)
	}
	fixture.testCase.Definition = domain.TestCaseDefinition{
		SchemaVersion: 2, Type: casetypes.TypeInputLatencyLadder, TypeVersion: 2, Spec: spec,
	}
	snapshot := fixture.snapshot()
	snapshot.Channel.BaseURL = server.URL
	snapshot.CaseDefinitions[0] = fixture.testCase
	run, err := domain.NewRun(domain.EntityMeta{ID: "30000000-0000-4000-8000-000000000097", SchemaVersion: 1, Revision: 1, CreatedAt: fixture.now, UpdatedAt: fixture.now}, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	storeRef, _ := credentials.StoreRefFromCredential(fixture.credential)
	store := credentials.NewMemoryStore()
	_ = store.Set(context.Background(), storeRef, []byte("test-secret"))
	lease, _ := store.Get(context.Background(), storeRef)
	defer lease.Close()

	executor := runs.NewInputLatencyLadderExecutor(server.Client().Transport)
	var drafts []runs.ResultDraft
	err = executor.Execute(context.Background(), runs.ExecutionRequest{Run: run, Cases: []domain.TestCase{fixture.testCase}, Credential: lease}, func(draft runs.ResultDraft) error {
		drafts = append(drafts, draft)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if requestCount != 4 || len(drafts) != 3 {
		t.Fatalf("requests/drafts = %d/%d, want 4/3", requestCount, len(drafts))
	}
	for index, draft := range drafts {
		wantStep := "8"
		if index >= 2 {
			wantStep = "16"
		}
		if !draft.Success.Overall() || draft.Dimensions["input_tokens_target"] != wantStep || draft.Dimensions["stage"] == "" || draft.Dimensions["sample"] == "" || draft.Dimensions["case_key"] != fixture.testCase.Key {
			t.Fatalf("draft %d = %#v", index, draft)
		}
		wantSamples := "2"
		wantWarmups := "1"
		if index >= 2 {
			wantSamples = "1"
			wantWarmups = "0"
		}
		if draft.Dimensions["stage_samples"] != wantSamples || draft.Dimensions["stage_warmups"] != wantWarmups {
			t.Fatalf("draft schedule %d = %#v", index, draft.Dimensions)
		}
		if draft.Metrics["prompt_tokens"] != 9 || draft.Metrics["ttft_ms"] <= 0 {
			t.Fatalf("draft metrics %d = %#v", index, draft.Metrics)
		}
	}
	if len(contents) != 4 || contents[0] == contents[1] {
		t.Fatalf("cold-cache prompts did not vary by attempt: %#v", contents)
	}
	for _, role := range firstRoles {
		if role != "system" {
			t.Fatalf("request template system message was not preserved: %#v", firstRoles)
		}
	}
}

func pointerToUint32(value uint32) *uint32 { return &value }
