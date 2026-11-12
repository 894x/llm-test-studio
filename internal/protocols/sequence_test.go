package protocols_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type sequenceRoundTrip func(*http.Request) (*http.Response, error)

func (send sequenceRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return send(request)
}

func TestSequenceWaitReplayCancellationAndRequestTimeout(t *testing.T) {
	for _, proto := range []domain.Protocol{
		domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages,
	} {
		for _, mode := range []string{"success", "cancel_wait", "timeout_initial", "timeout_followup"} {
			t.Run(string(proto)+"/"+mode, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					calls := 0
					first := []byte{}
					var firstFinished time.Time
					lease, err := credentials.NewTemporaryLease([]byte("local-sequence-test"))
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					transport := sequenceRoundTrip(func(r *http.Request) (*http.Response, error) {
						calls++
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Fatal(err)
						}
						if calls == 1 {
							first = bytes.Clone(body)
						} else if !bytes.Equal(first, body) || time.Since(firstFinished) != 6*time.Minute {
							t.Fatal("followup changed request or bypassed the declared wait")
						}
						if mode == "timeout_initial" || (mode == "timeout_followup" && calls == 2) {
							<-r.Context().Done()
							return nil, r.Context().Err()
						}
						// Each HTTP request takes less than its own one-second budget.
						time.Sleep(600 * time.Millisecond)
						firstFinished = time.Now()
						if mode == "cancel_wait" {
							time.AfterFunc(time.Minute, cancel)
						}
						payload := `{"choices":[{"message":{"content":"OK"}}]}`
						if proto == domain.ProtocolOpenAIResponses {
							payload = `{"id":"resp_local","status":"completed","output":[
							{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}]}`
						}
						if proto == domain.ProtocolAnthropicMessages {
							payload = `{"type":"message","role":"assistant",
							"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`
						}
						return &http.Response{
							StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
							Body: io.NopCloser(strings.NewReader(payload)),
						}, nil
					})
					client, err := protocols.NewClient(protocols.NewRegistry(), lease, domain.ChannelSnapshot{
						EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
						Name:              "local sequence", Protocol: proto,
						BaseURL: "https://sequence.test", UpstreamModelName: "model",
					}, transport)
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					spec := decodeSpec(t, `{"inputs":{},"request":{"body":{
					"nonce":{"$generate":"random_text","length":64,"alphabet":"0123456789abcdef"}}},
					"workflow":{"mode":"sequence","steps":[{"id":"after_6m","delay_ms":360000,"reuse_request_from":"initial"}]},
					"assertions":[{"id":"issues","source":"issues","operator":"length_equals","value":0},
					{"id":"count","source":"requests","operator":"length_equals","value":2},
					{"id":"same","source":"requests","pointer":"/1/request_body","operator":"equals",
					"expected_from":{"source":"requests","pointer":"/0/request_body"}}]}`)
					started := time.Now()
					result, err := client.Execute(ctx, protocols.Execution{
						Spec: spec, Random: testspec.RandomContext{Seed: 42}, Settings: testspec.RunSettings{TimeoutMS: 1000},
					})
					if err != nil {
						t.Fatal(err)
					}
					if mode == "success" {
						if calls != 2 || result.Verdict.Status != testspec.VerdictPassed || len(result.Observation.Issues) != 0 {
							t.Fatalf("sequence failed: %+v", result)
						}
						if result.Observation.Exchanges[1].Metrics["sequence_wait_ms"] != 360000 ||
							time.Since(started) != 6*time.Minute+1200*time.Millisecond {
							t.Fatal("wait or HTTP budgets were not kept separately")
						}
						return
					}
					if result.Verdict.Status == testspec.VerdictPassed || len(result.Observation.Issues) == 0 {
						t.Fatal("cancelled or timed-out sequence passed")
					}
					wantCalls, wantElapsed := 1, time.Second
					if mode == "cancel_wait" {
						wantElapsed = time.Minute + 600*time.Millisecond
						if len(result.Observation.Exchanges) != 1 {
							t.Fatal("cancellation lost initial evidence or sent a followup")
						}
					}
					if mode == "timeout_followup" {
						wantCalls, wantElapsed = 2, 6*time.Minute+1600*time.Millisecond
					}
					if calls != wantCalls || time.Since(started) != wantElapsed {
						t.Fatalf("timeout/cancel not enforced: calls=%d elapsed=%s", calls, time.Since(started))
					}
				})
			})
		}
	}
}
