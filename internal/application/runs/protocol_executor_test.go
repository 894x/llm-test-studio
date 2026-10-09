package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

type protocolRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip protocolRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestProtocolWarmupPreservesMeasuredSeedAndStopsBeforeMeasurement(t *testing.T) {
	fixture := newRunFixture(t)
	fixture.testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{"inputs":{"prompt":{"type":"string","default":"hello"}},"request":{"body":{"messages":[{"role":"user","content":{"$generate":"random_text","length":16,"alphabet":"abcXYZ012"}}]}},"assertions":[{"id":"http","source":"http.status","operator":"equals","value":200}]}`)
	snapshot := fixture.snapshot()
	run, err := domain.NewRun(fixture.plan.EntityMeta, fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := credentials.NewTemporaryLease([]byte("protocol-executor-secret"))
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	request := runs.ExecutionRequest{Run: run, Entry: snapshot.Entries[0], Cases: []domain.TestCase{fixture.testCase}, Credential: lease}
	calls := 0
	executor := runs.NewProtocolExecutor(protocolRoundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"OK"}}]}`))}, nil
	}))
	var measured []runs.ResultDraft
	if err := executor.Execute(context.Background(), request, func(result runs.ResultDraft) error { measured = append(measured, result); return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(measured) != 1 {
		t.Fatal("single execution count")
	}
	request.Entry.WarmupCount = 2
	var withWarmup []runs.ResultDraft
	if err := executor.Execute(context.Background(), request, func(result runs.ResultDraft) error { withWarmup = append(withWarmup, result); return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || len(withWarmup) != 3 {
		t.Fatalf("warmup execution counts = %d/%d", calls, len(withWarmup))
	}
	if string(measured[0].Observation.Exchanges[0].RequestBody) != string(withWarmup[2].Observation.Exchanges[0].RequestBody) {
		t.Fatal("warmup changed deterministic measurement")
	}
	if withWarmup[0].Dimensions["phase"] != "warmup" || withWarmup[1].Dimensions["phase"] != "warmup" || withWarmup[2].Dimensions["phase"] != "measured" {
		t.Fatal("phase attribution lost")
	}
	if withWarmup[0].RequestID == withWarmup[2].RequestID || withWarmup[2].Metrics["started_offset_ms"] < withWarmup[1].Metrics["finished_offset_ms"] {
		t.Fatal("request identity or timing overlaps phases")
	}
	for _, cancelRun := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		stop := make(chan struct{})
		request.StopSending = stop
		emitted := 0
		err := executor.Execute(ctx, request, func(result runs.ResultDraft) error {
			emitted++
			if result.Dimensions["phase"] != "warmup" {
				t.Error("measurement began after stop")
			}
			if emitted == 1 {
				if cancelRun {
					cancel()
				} else {
					close(stop)
				}
			}
			return nil
		})
		cancel()
		if emitted != 1 {
			t.Fatalf("executions after stop = %d", emitted)
		}
		if cancelRun && !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
		if !cancelRun && err != nil {
			t.Fatal(err)
		}
	}
}
