package casebundle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type cacheTTLTransport func(*http.Request) (*http.Response, error)

func (send cacheTTLTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return send(request)
}

func TestKimiCacheTTLDelayedReplayAndFirstWriteComparison(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-ttl/*/case.json")
	if err != nil || len(paths) != 2 {
		t.Fatalf("TTL cases = %v, %v", paths, err)
	}
	for _, path := range paths {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		item, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		if item.Default || len(item.Definitions) != 3 {
			t.Fatal("TTL probes must be optional and support three protocols")
		}
		for _, proto := range item.Protocols() {
			spec, err := item.SpecFor(proto)
			if err != nil {
				t.Fatal(err)
			}
			for _, fault := range []string{
				"", "partial_hit", "warm_initial", "no_hit", "rewrite", "zero_write",
				"wrong_ttl_prime", "header_mismatch_repeat", "body_mismatch_repeat", "missing_usage", "interrupted",
				"omitted_after_6m", "omitted_initial", "omitted_warm_initial", "omitted_after_no_hit",
				"omitted_after_partial_hit", "omitted_after_new_write", "omitted_after_missing_header",
				"omitted_after_ttl_conflict", "null_after_6m", "invalid_after_6m",
				"negative_after_6m", "fractional_after_6m",
			} {
				t.Run(item.Key+"/"+string(proto)+"/"+fault, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						stream := strings.HasSuffix(item.Key, "_stream")
						previous := []byte{}
						// Two executions prove that replay freezes the request within a
						// Case while the next execution receives a fresh cache prefix.
						for iteration := range uint64(2) {
							fixture := &kimiCacheHTTP{
								t: t, protocol: proto, key: item.Key, fault: fault, read: true, stream: stream,
							}
							configureTTLWriteFault(fixture, fault)
							var initialFinished time.Time
							transport := cacheTTLTransport(func(r *http.Request) (*http.Response, error) {
								if fixture.calls == 1 && time.Since(initialFinished) != 6*time.Minute {
									t.Fatal("followup did not wait six minutes from the initial response")
								}
								response := httptest.NewRecorder()
								fixture.ServeHTTP(response, r)
								if fixture.calls == 1 {
									initialFinished = time.Now()
								}
								return response.Result(), nil
							})
							lease, err := credentials.NewTemporaryLease([]byte("local-ttl-fixture"))
							if err != nil {
								t.Fatal(err)
							}
							defer lease.Close()
							client, err := protocols.NewClient(protocols.NewRegistry(), lease, domain.ChannelSnapshot{
								EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
								Name:              "local TTL fixture", Protocol: proto,
								BaseURL: "https://cache.test", UpstreamModelName: "kimi-k3.1-pro",
							}, transport)
							if err != nil {
								t.Fatal(err)
							}
							result, err := client.Execute(context.Background(), protocols.Execution{
								Spec: spec, Settings: testspec.RunSettings{TimeoutMS: 1000},
								Random: testspec.RandomContext{Seed: 42, ExecutionItemID: "ttl-test", MemberID: item.ID, Iteration: iteration},
							})
							client.Close()
							if err != nil {
								t.Fatal(err)
							}
							wantPass := fault == "" || (!stream && fault == "interrupted")
							if fault == "omitted_after_6m" {
								wantPass = stream && proto != domain.ProtocolAnthropicMessages
							}
							if (result.Verdict.Status == testspec.VerdictPassed) != wantPass {
								t.Fatalf("TTL verdict=%s, want pass=%t; issues=%v", result.Verdict.Status, wantPass, result.Observation.Issues)
							}
							if len(previous) > 0 && bytes.Equal(previous, fixture.first) {
								t.Fatal("a new Case execution reused its cold prefix")
							}
							previous = bytes.Clone(fixture.first)
							encoded, err := json.Marshal(result)
							if err != nil {
								t.Fatal(err)
							}
							var restored testspec.Result
							if err := json.Unmarshal(encoded, &restored); err != nil {
								t.Fatal(err)
							}
							if testspec.Evaluate(spec.Assertions, restored.Observation).Status != result.Verdict.Status {
								t.Fatal("serialized evidence lost the multi-step conclusion")
							}
						}
					})
				})
			}
		}
	}
}

func configureTTLWriteFault(fixture *kimiCacheHTTP, fault string) {
	baseFaults := map[string]string{
		"omitted_warm_initial":         "warm_initial",
		"omitted_after_no_hit":         "no_hit",
		"omitted_after_partial_hit":    "partial_hit",
		"omitted_after_new_write":      "rewrite",
		"omitted_after_missing_header": "missing_header_1h",
		"omitted_after_ttl_conflict":   "body_mismatch_repeat",
	}
	if base, exists := baseFaults[fault]; exists {
		fixture.fault = base
	}
	mutations := map[string]any{
		"null_after_6m": nil, "invalid_after_6m": "0",
		"negative_after_6m": -1, "fractional_after_6m": 0.5,
	}
	value, invalidValue := mutations[fault]
	omitted := strings.HasPrefix(fault, "omitted_")
	if !omitted && !invalidValue {
		return
	}
	call := 2
	if fault == "omitted_initial" || fault == "omitted_warm_initial" {
		call = 1
	}
	fixture.mutateUsage = func(usage map[string]any, current int) {
		if current != call || usage == nil {
			return
		}
		details, field := usage, "cache_creation_input_tokens"
		if fixture.protocol != domain.ProtocolAnthropicMessages {
			name := "input_tokens_details"
			if fixture.protocol == domain.ProtocolOpenAIChat {
				name = "prompt_tokens_details"
			}
			details, _ = usage[name].(map[string]any)
			field = "cache_write_tokens"
		}
		if omitted {
			delete(details, field)
			return
		}
		details[field] = value
	}
}
