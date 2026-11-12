package casebundle_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestKimiCombinedCacheSuitesUseOneProbePerScenario(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-usage/Kimi-cache-headers-*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	probes := map[string]domain.TestCase{}
	for _, path := range paths {
		raw, err := fs.ReadFile(casebundle.Bundle, path)
		if err != nil {
			t.Fatal(err)
		}
		item, err := casecodec.DecodeFilesystemCase(path, raw)
		if err != nil {
			t.Fatal(err)
		}
		probes[item.ID] = item
	}
	for _, proto := range []domain.Protocol{
		domain.ProtocolOpenAIChat, domain.ProtocolOpenAIResponses, domain.ProtocolAnthropicMessages,
	} {
		t.Run(string(proto), func(t *testing.T) {
			raw, err := fs.ReadFile(suitebundle.Bundle, string(proto)+"/kimi-cache/suite.json")
			if err != nil {
				t.Fatal(err)
			}
			var suite struct {
				Cases []struct {
					CaseID string `json:"case_id"`
				} `json:"cases"`
			}
			if err := json.Unmarshal(raw, &suite); err != nil {
				t.Fatal(err)
			}
			if len(suite.Cases) != 10 {
				t.Fatalf("combined suite has %d cases, want 10", len(suite.Cases))
			}
			seen := map[string]bool{}
			requests := 0
			for _, ref := range suite.Cases {
				item, ok := probes[ref.CaseID]
				if !ok || seen[ref.CaseID] || !item.SupportsProtocol(proto) {
					t.Fatalf("combined suite repeats or lacks a merged probe: %s", ref.CaseID)
				}
				seen[ref.CaseID] = true
				spec, err := item.SpecFor(proto)
				if err != nil {
					t.Fatal(err)
				}
				requests++
				if spec.Workflow != nil {
					requests += len(spec.Workflow.Steps)
				}
			}
			if requests != 14 {
				t.Fatalf("combined suite makes %d requests, want 14", requests)
			}
		})
	}
}

func TestKimiCacheUsageCasesThroughHTTP(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-usage/*/case.json")
	if err != nil || len(paths) != 20 {
		t.Fatalf("cache cases = %v, %v", paths, err)
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
			t.Fatalf("cache probe must be non-default and support all three protocols: %s", item.Key)
		}
		for _, proto := range item.Protocols() {
			spec, err := item.SpecFor(proto)
			if err != nil {
				t.Fatal(err)
			}
			read := strings.Contains(item.Key, ".read_")
			stream := strings.HasSuffix(item.Key, "_stream")
			headers := strings.Contains(item.Key, ".headers.")
			faults := []string{
				"", "missing_write", "negative_write", "fractional_write",
				"missing_input", "negative_read", "missing_output", "fractional_output",
			}
			if read {
				faults = append(faults, "no_hit", "rewrite", "body_mismatch_repeat", "wrong_ttl_prime")
				faults = append(faults,
					"missing_write_repeat", "missing_write_warm_prime", "missing_write_no_hit",
					"missing_write_new_write", "missing_write_header", "missing_write_ttl",
					"null_write", "invalid_write",
				)
			} else {
				faults = append(faults, "zero_write")
			}
			headerFaults := []string{
				"missing_header", "missing_header_5m", "missing_header_1h", "header_mismatch",
				"negative_header", "fractional_header", "duplicate_header",
			}
			if read {
				headerFaults = append(headerFaults, "header_mismatch_repeat")
			}
			faults = append(faults, headerFaults...)
			faults = append(faults,
				"wrong_ttl", "body_wrong_ttl", "body_missing_selected", "body_wrong_type",
				"body_negative", "body_fractional", "body_sum_mismatch", "no_body_ttl", "omit_zero_bucket",
			)
			if proto != domain.ProtocolAnthropicMessages {
				faults = append(faults,
					"wrong_total", "missing_total", "negative_total", "invalid_total",
					"top_level_ttl", "both_ttl", "top_level_invalid",
					"top_level_wrong_ttl", "nested_wrong_ttl", "write_over_input",
				)
				if read {
					faults = append(faults, "read_over_input")
				}
			}
			if stream {
				faults = append(faults, "missing_usage", "interrupted")
				if proto == domain.ProtocolOpenAIChat {
					faults = append(faults, "usage_only_initial")
				}
			}
			for _, fault := range faults {
				t.Run(item.Key+"/"+string(proto)+"/"+fault, func(t *testing.T) {
					fixture := &kimiCacheHTTP{t: t, protocol: proto, key: item.Key, fault: fault, read: read, stream: stream}
					server := httptest.NewServer(fixture)
					defer server.Close()
					lease, err := credentials.NewTemporaryLease([]byte("local-cache-fixture"))
					if err != nil {
						t.Fatal(err)
					}
					defer lease.Close()
					client, err := protocols.NewClient(
						protocols.NewRegistry(),
						lease,
						domain.ChannelSnapshot{
							EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
							Name:              "local cache fixture", Protocol: proto, BaseURL: server.URL,
							UpstreamModelName: "kimi-k3.1-pro",
						},
						server.Client().Transport,
					)
					if err != nil {
						t.Fatal(err)
					}
					defer client.Close()
					result, err := client.Execute(context.Background(), protocols.Execution{
						Spec: spec, Random: testspec.RandomContext{Seed: 31, ExecutionItemID: t.Name()},
					})
					if err != nil {
						t.Fatal(err)
					}
					wantPassed := fault == ""
					if fault == "missing_write_repeat" || fault == "missing_write_warm_prime" {
						wantPassed = headers && proto != domain.ProtocolAnthropicMessages
					}
					if fault == "missing_write_header" {
						wantPassed = headers && proto != domain.ProtocolAnthropicMessages
					}
					if fault == "top_level_ttl" || fault == "both_ttl" {
						wantPassed = true
					}
					if !headers && slices.Contains(headerFaults, fault) {
						wantPassed = true
					}
					if headers && slices.Contains([]string{"missing_header", "missing_header_5m", "missing_header_1h"}, fault) {
						wantPassed = true
					}
					if fault == "no_body_ttl" || fault == "omit_zero_bucket" {
						wantPassed = headers || proto != domain.ProtocolAnthropicMessages
					}
					if fault == "body_missing_selected" && headers {
						wantPassed = true
					}
					if slices.Contains([]string{"wrong_total", "missing_total", "negative_total", "invalid_total"}, fault) {
						wantPassed = true
					}
					if (result.Verdict.Status == testspec.VerdictPassed) != wantPassed {
						failed := []string{}
						for _, assertion := range result.Verdict.Assertions {
							if assertion.Status != testspec.VerdictPassed {
								failed = append(failed, assertion.ID+":"+assertion.Reason)
							}
						}
						t.Fatalf("verdict = %s, want passed=%t; failed=%v; issues=%+v",
							result.Verdict.Status, wantPassed, failed, result.Observation.Issues)
					}
					wantCalls := 1
					if read {
						wantCalls = 2
					}
					completeSequence := wantPassed || slices.Contains(headerFaults, fault)
					if completeSequence && fixture.calls != wantCalls {
						t.Fatalf("HTTP calls = %d, want %d", fixture.calls, wantCalls)
					}
					if fault == "negative_header" || fault == "fractional_header" || fault == "duplicate_header" {
						if len(result.Observation.Issues) != wantCalls {
							t.Fatalf("header diagnostics = %+v, want one per request", result.Observation.Issues)
						}
						for _, issue := range result.Observation.Issues {
							if issue.Stage != "headers" || issue.Code != "invalid_cache_write_header" {
								t.Fatalf("unexpected header diagnostic: %+v", issue)
							}
						}
					}
					if wantPassed {
						encoded, err := json.Marshal(result)
						if err != nil || !json.Valid(encoded) {
							t.Fatalf("evidence cannot be persisted: %v", err)
						}
						var restored testspec.Result
						if err := json.Unmarshal(encoded, &restored); err != nil {
							t.Fatal(err)
						}
						if testspec.Evaluate(spec.Assertions, restored.Observation).Status != testspec.VerdictPassed {
							t.Fatal("recorded evidence lost cache accounting")
						}
					}
				})
			}
		}
	}
}

type kimiCacheHTTP struct {
	t             *testing.T
	protocol      domain.Protocol
	key           string
	fault         string
	read          bool
	stream        bool
	calls         int
	first         []byte
	mutateUsage   func(map[string]any, int)
	mutateHeaders func(http.Header, int)
}

func (fixture *kimiCacheHTTP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fixture.calls++
	body, err := io.ReadAll(r.Body)
	if err != nil {
		fixture.t.Error(err)
		return
	}
	if fixture.calls == 1 {
		fixture.first = bytes.Clone(body)
	} else if !bytes.Equal(fixture.first, body) {
		fixture.t.Error("read probe changed the repeated request, invalidating the cache claim")
	}
	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		fixture.t.Error(err)
		return
	}
	ttl := "5m"
	if strings.Contains(fixture.key, "1h") {
		ttl = "1h"
	}
	option := "prompt_cache_options"
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		option = "cache_control"
	}
	options, present := request[option].(map[string]any)
	if strings.Contains(fixture.key, "default") {
		if present {
			fixture.t.Error("default-TTL probe must omit cache options")
		}
	} else if !present || options["ttl"] != ttl {
		fixture.t.Errorf("TTL request = %+v, want %s", options, ttl)
	}
	if fixture.stream && fixture.protocol == domain.ProtocolOpenAIChat {
		options, _ := request["stream_options"].(map[string]any)
		if options["include_usage"] != true {
			fixture.t.Error("Chat streaming probe did not request final usage")
		}
	}
	write, read := 512, 0
	if fixture.read && fixture.calls > 1 {
		write, read = 0, 512
	}
	if fixture.fault == "missing_write_warm_prime" {
		write, read = 0, 512
	}
	if fixture.fault == "zero_write" {
		write = 0
	}
	if fixture.fault == "no_hit" || fixture.fault == "missing_write_no_hit" {
		read = 0
	}
	if fixture.fault == "partial_hit" && fixture.calls > 1 {
		read = 511
	}
	if fixture.fault == "warm_initial" && fixture.calls == 1 {
		read = 1
	}
	rewrite := fixture.fault == "rewrite" || fixture.fault == "missing_write_new_write"
	if rewrite && fixture.calls > 1 {
		write = 1
	}
	short, long := write, 0
	if ttl == "1h" {
		short, long = 0, write
	}
	wrongTTL := fixture.fault == "wrong_ttl"
	wrongPrimingTTL := fixture.fault == "wrong_ttl_prime" && fixture.calls == 1
	if wrongTTL || wrongPrimingTTL {
		short, long = long, short
		if short == long {
			short = 1
		}
	}
	missingHeader := fixture.fault == "missing_header"
	primingRead := fixture.calls == 1 && fixture.read
	if !missingHeader || primingRead {
		w.Header().Set(protocol.MSHCacheWriteHeader5m, strconv.Itoa(short))
		w.Header().Set(protocol.MSHCacheWriteHeader1h, strconv.Itoa(long))
	}
	if !primingRead {
		switch fixture.fault {
		case "missing_header_5m":
			w.Header().Del(protocol.MSHCacheWriteHeader5m)
		case "missing_header_1h":
			w.Header().Del(protocol.MSHCacheWriteHeader1h)
		case "missing_write_header":
			w.Header().Del(protocol.MSHCacheWriteHeader5m)
		}
	}
	switch fixture.fault {
	case "header_mismatch":
		w.Header().Set(protocol.MSHCacheWriteHeader5m, "7")
	case "header_mismatch_repeat":
		if fixture.calls > 1 {
			w.Header().Set(protocol.MSHCacheWriteHeader5m, "7")
		}
	case "negative_header":
		w.Header().Set(protocol.MSHCacheWriteHeader5m, "-1")
	case "fractional_header":
		w.Header().Set(protocol.MSHCacheWriteHeader5m, "1.5")
	case "duplicate_header":
		w.Header().Add(protocol.MSHCacheWriteHeader5m, "0")
	}
	cacheCreation := map[string]any{
		"ephemeral_5m_input_tokens": short,
		"ephemeral_1h_input_tokens": long,
	}
	selected := "ephemeral_5m_input_tokens"
	if ttl == "1h" {
		selected = "ephemeral_1h_input_tokens"
	}
	switch fixture.fault {
	case "body_wrong_ttl":
		cacheCreation["ephemeral_5m_input_tokens"], cacheCreation["ephemeral_1h_input_tokens"] = long, short
	case "body_missing_selected":
		delete(cacheCreation, selected)
	case "body_negative":
		cacheCreation[selected] = -1
	case "body_fractional":
		cacheCreation[selected] = 1.5
	case "body_sum_mismatch":
		cacheCreation[selected] = write + 1
	case "body_mismatch_repeat", "missing_write_ttl":
		if fixture.calls > 1 {
			cacheCreation[selected] = write + 1
		}
	case "omit_zero_bucket":
		for key, value := range cacheCreation {
			if value == 0 {
				delete(cacheCreation, key)
			}
		}
	}
	usage := map[string]any{}
	var details map[string]any
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		usage = map[string]any{
			"input_tokens": 512, "output_tokens": 1, "cache_read_input_tokens": read,
			"cache_creation_input_tokens": write,
			"cache_creation":              cacheCreation,
		}
		details = usage
	} else {
		input, output := "prompt_tokens", "completion_tokens"
		if fixture.protocol == domain.ProtocolOpenAIResponses {
			input, output = "input_tokens", "output_tokens"
		}
		details = map[string]any{
			"cached_tokens": read, "cache_write_tokens": write, "cache_creation": cacheCreation,
		}
		usage = map[string]any{input: 1024, output: 1, "total_tokens": 1025, input + "_details": details}
		switch fixture.fault {
		case "wrong_total":
			usage["total_tokens"] = 1026
		case "missing_total":
			delete(usage, "total_tokens")
		case "negative_total":
			usage["total_tokens"] = -1
		case "invalid_total":
			usage["total_tokens"] = "unavailable"
		}
	}
	input, output := "input_tokens", "output_tokens"
	if fixture.protocol == domain.ProtocolOpenAIChat {
		input, output = "prompt_tokens", "completion_tokens"
	}
	readField := "cached_tokens"
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		readField = "cache_read_input_tokens"
	}
	switch fixture.fault {
	case "missing_input":
		delete(usage, input)
	case "negative_read":
		details[readField] = -1
	case "missing_output":
		delete(usage, output)
	case "fractional_output":
		usage[output] = 1.5
	case "write_over_input":
		usage[input] = 1
	case "read_over_input":
		if fixture.calls > 1 {
			usage[input] = 1
		}
	}

	switch fixture.fault {
	case "no_body_ttl":
		delete(details, "cache_creation")
	case "body_wrong_type":
		details["cache_creation"] = "invalid"
	case "top_level_ttl":
		usage["cache_creation"] = cacheCreation
		delete(details, "cache_creation")
	case "both_ttl":
		usage["cache_creation"] = cacheCreation
	case "top_level_invalid":
		usage["cache_creation"] = "invalid"
	case "top_level_wrong_ttl":
		usage["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": long,
			"ephemeral_1h_input_tokens": short,
		}
	case "nested_wrong_ttl":
		usage["cache_creation"] = cacheCreation
		details["cache_creation"] = map[string]any{
			"ephemeral_5m_input_tokens": long,
			"ephemeral_1h_input_tokens": short,
		}
	}
	field := "cache_write_tokens"
	if fixture.protocol == domain.ProtocolAnthropicMessages {
		field = "cache_creation_input_tokens"
	}
	switch fixture.fault {
	case "missing_write":
		delete(details, field)
	case "missing_write_warm_prime":
		delete(details, field)
	case "missing_write_repeat", "missing_write_no_hit", "missing_write_new_write",
		"missing_write_header", "missing_write_ttl":
		if fixture.calls > 1 {
			delete(details, field)
		}
	case "null_write":
		details[field] = nil
	case "invalid_write":
		details[field] = "0"
	case "negative_write":
		details[field] = -1
	case "fractional_write":
		details[field] = 1.5
	}
	if fixture.fault == "missing_usage" {
		usage = nil
	}
	if fixture.mutateUsage != nil {
		fixture.mutateUsage(usage, fixture.calls)
	}
	if fixture.mutateHeaders != nil {
		fixture.mutateHeaders(w.Header(), fixture.calls)
	}
	fixture.respond(w, usage)
}

func (fixture *kimiCacheHTTP) respond(w http.ResponseWriter, usage map[string]any) {
	message := map[string]any{"role": "assistant", "content": "OK"}
	root := map[string]any{"id": "cache_fixture", "model": "kimi-k3.1-pro", "usage": usage}
	switch fixture.protocol {
	case domain.ProtocolOpenAIChat:
		root["object"] = "chat.completion"
		root["choices"] = []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}
	case domain.ProtocolOpenAIResponses:
		root["object"], root["status"] = "response", "completed"
		root["output"] = []any{map[string]any{
			"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "OK"}},
		}}
	case domain.ProtocolAnthropicMessages:
		root["type"], root["role"], root["stop_reason"] = "message", "assistant", "end_turn"
		root["content"] = []any{map[string]any{"type": "text", "text": "OK"}}
	}
	if !fixture.stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(root)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(value any) {
		raw, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	switch fixture.protocol {
	case domain.ProtocolOpenAIChat:
		root["object"] = "chat.completion.chunk"
		root["choices"] = []any{map[string]any{"index": 0, "delta": message, "finish_reason": "stop"}}
		if fixture.fault != "usage_only_initial" {
			delete(root, "usage")
		}
		emit(root)
		if fixture.fault == "interrupted" {
			return
		}
		root["choices"], root["usage"] = []any{}, usage
		if fixture.fault == "usage_only_initial" {
			delete(root, "usage")
		}
		emit(root)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	case domain.ProtocolOpenAIResponses:
		emit(map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{
			"id": "cache_fixture", "object": "response", "status": "in_progress", "output": []any{},
		}})
		if fixture.fault != "interrupted" {
			emit(map[string]any{"type": "response.completed", "sequence_number": 1, "response": root})
		}
	case domain.ProtocolAnthropicMessages:
		root["content"], root["stop_reason"] = []any{}, nil
		emit(map[string]any{"type": "message_start", "message": root})
		if fixture.fault != "interrupted" {
			outputUsage := map[string]any{}
			if value, exists := usage["output_tokens"]; exists {
				outputUsage["output_tokens"] = value
			}
			emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"},
				"usage": outputUsage})
			emit(map[string]any{"type": "message_stop"})
		}
	}
}

func TestKimiCacheWritePrefixChangesBetweenExecutions(t *testing.T) {
	raw, err := fs.ReadFile(casebundle.Bundle, "kimi-cache-usage/Kimi-cache-write-5m/case.json")
	if err != nil {
		t.Fatal(err)
	}
	item, err := casecodec.DecodeFilesystemCase("cache-write", raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range item.Protocols() {
		spec, err := item.SpecFor(proto)
		if err != nil {
			t.Fatal(err)
		}
		first, err := testspec.Generate(spec, map[string]json.RawMessage{}, testspec.RandomContext{ExecutionItemID: "run-1"})
		if err != nil {
			t.Fatal(err)
		}
		second, err := testspec.Generate(spec, map[string]json.RawMessage{}, testspec.RandomContext{ExecutionItemID: "run-2"})
		if err != nil || bytes.Equal(first.Body, second.Body) {
			t.Fatalf("%s reused a cold-write prefix: %v", proto, err)
		}
	}
}
