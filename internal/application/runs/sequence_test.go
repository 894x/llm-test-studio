package runs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestProtocolSchedulerAllowsMultiStepWaitBeyondHTTPRequestBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture, cases := concurrentSuiteFixture(t, 2)
		for id, item := range cases {
			var spec map[string]any
			if err := json.Unmarshal(item.Definitions[domain.ProtocolOpenAIChat], &spec); err != nil {
				t.Fatal(err)
			}
			spec["workflow"] = map[string]any{"mode": "sequence", "steps": []any{
				map[string]any{"id": "after_6m", "delay_ms": 360000, "reuse_request_from": "initial"},
			}}
			raw, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			item.Definitions[domain.ProtocolOpenAIChat] = raw
			cases[id] = item
		}
		request := concurrentExecutionRequest(t, fixture, cases, 2)
		originalTimeout := request.Entry.Load.RequestTimeoutMS
		calls := 0
		executor := runs.NewProtocolExecutor(protocolRoundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`)),
			}, nil
		}))
		drafts := []runs.ResultDraft{}
		started := time.Now()
		err := executor.Execute(context.Background(), request, func(draft runs.ResultDraft) error {
			drafts = append(drafts, draft)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(drafts) != 2 || calls != 4 || time.Since(started) != 6*time.Minute {
			t.Fatalf(
				"unfinished sequences: drafts=%d calls=%d elapsed=%s",
				len(drafts),
				calls,
				time.Since(started),
			)
		}
		for _, draft := range drafts {
			if draft.Verification.Status != testspec.VerdictPassed || len(draft.Observation.Exchanges) != 2 {
				t.Fatalf("scheduler timed out a waiting Case: %+v", draft)
			}
		}
		if request.Entry.Load.RequestTimeoutMS != originalTimeout {
			t.Fatal("execution changed the frozen request timeout")
		}
	})
}
