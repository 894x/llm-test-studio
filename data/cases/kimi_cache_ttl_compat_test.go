package casebundle_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestKimiCacheTTLEvidenceShapes(t *testing.T) {
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-usage/Kimi-cache-headers-*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	delayed, err := fs.Glob(casebundle.Bundle, "kimi-cache-ttl/*/case.json")
	if err != nil {
		t.Fatal(err)
	}
	paths = append(paths, delayed...)
	if len(paths) != 12 {
		t.Fatalf("combined and delayed cases = %d, want 12", len(paths))
	}
	shapes := []struct {
		name     string
		pass     bool
		readOnly bool
	}{
		{name: "both", pass: true},
		{name: "header_only", pass: true},
		{name: "official", pass: true},
		{name: "usage_only", pass: true},
		{name: "usage_only_top_level", pass: true},
		{name: "usage_only_both_locations", pass: true},
		{name: "usage_only_zero_bucket_omitted", pass: true},
		{name: "partial_headers_with_usage", pass: true},
		{name: "no_ttl"},
		{name: "empty_usage_ttl"},
		{name: "null_usage_ttl"},
		{name: "partial_headers_without_usage"},
		{name: "malformed_header_with_usage"},
		{name: "usage_only_wrong_ttl"},
		{name: "usage_only_missing_selected"},
		{name: "conflicting_body_locations"},
		{name: "cold_read_null"},
		{name: "cold_read_string"},
		{name: "cold_read_fractional"},
		{name: "usage_only_zero_write_omitted", pass: true, readOnly: true},
		{name: "usage_only_omitted_write_incomplete_ttl", readOnly: true},
		{name: "usage_only_no_hit", readOnly: true},
		{name: "usage_only_missing_hit", readOnly: true},
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
		read := strings.Contains(item.Key, ".read_")
		for _, proto := range item.Protocols() {
			spec, err := item.SpecFor(proto)
			if err != nil {
				t.Fatal(err)
			}
			for _, shape := range shapes {
				if shape.readOnly && !read {
					continue
				}
				if proto == domain.ProtocolAnthropicMessages && shape.name == "conflicting_body_locations" {
					continue
				}
				t.Run(item.Key+"/"+string(proto)+"/"+shape.name, func(t *testing.T) {
					synctest.Test(t, func(t *testing.T) {
						fixture := &kimiCacheHTTP{
							t: t, protocol: proto, key: item.Key, read: read,
							stream: strings.HasSuffix(item.Key, "_stream"),
						}
						configureKimiTTLShape(fixture, shape.name)
						lease, err := credentials.NewTemporaryLease([]byte("local-ttl-shapes"))
						if err != nil {
							t.Fatal(err)
						}
						defer lease.Close()
						transport := cacheTTLTransport(func(r *http.Request) (*http.Response, error) {
							response := httptest.NewRecorder()
							fixture.ServeHTTP(response, r)
							return response.Result(), nil
						})
						client, err := protocols.NewClient(
							protocols.NewRegistry(),
							lease,
							domain.ChannelSnapshot{
								EntityRevisionRef: domain.EntityRevisionRef{
									ID: "00000000-0000-4000-8000-000000000001", Revision: 1,
								},
								Name: "local TTL shapes", Protocol: proto,
								BaseURL: "https://cache.test", UpstreamModelName: "kimi-k3.1-pro",
							},
							transport,
						)
						if err != nil {
							t.Fatal(err)
						}
						defer client.Close()
						result, err := client.Execute(context.Background(), protocols.Execution{
							Spec: spec, Random: testspec.RandomContext{Seed: 894, ExecutionItemID: t.Name()},
						})
						if err != nil {
							t.Fatal(err)
						}
						wantPass := shape.pass
						if shape.name == "usage_only_zero_write_omitted" {
							wantPass = proto != domain.ProtocolAnthropicMessages
						}
						if (result.Verdict.Status == testspec.VerdictPassed) != wantPass {
							failed := []string{}
							for _, a := range result.Verdict.Assertions {
								if a.Status != testspec.VerdictPassed {
									failed = append(failed, a.ID+":"+a.Reason)
								}
							}
							t.Fatalf("verdict=%s, want pass=%t; failed=%v; issues=%v",
								result.Verdict.Status, wantPass, failed, result.Observation.Issues)
						}
						if shape.name == "no_ttl" || shape.name == "empty_usage_ttl" ||
							shape.name == "partial_headers_without_usage" || shape.name == "usage_only_missing_selected" ||
							(shape.name == "usage_only_omitted_write_incomplete_ttl" && proto != domain.ProtocolAnthropicMessages) {
							if result.Verdict.Status != testspec.VerdictIndeterminate {
								t.Fatalf("missing TTL evidence = %s, want indeterminate", result.Verdict.Status)
							}
						}
						encoded, err := json.Marshal(result)
						if err != nil {
							t.Fatal(err)
						}
						var restored testspec.Result
						if err := json.Unmarshal(encoded, &restored); err != nil {
							t.Fatal(err)
						}
						if testspec.Evaluate(spec.Assertions, restored.Observation).Status != result.Verdict.Status {
							t.Fatal("serialization changed TTL evidence verdict")
						}
						if shape.name == "official" && proto != domain.ProtocolAnthropicMessages {
							var response struct {
								Usage map[string]json.RawMessage `json:"usage"`
							}
							if err := json.Unmarshal(restored.Observation.Exchanges[0].Response, &response); err != nil {
								t.Fatal(err)
							}
							field := "prompt_tokens_details"
							if proto == domain.ProtocolOpenAIResponses {
								field = "input_tokens_details"
							}
							var details map[string]json.RawMessage
							if err := json.Unmarshal(response.Usage[field], &details); err != nil {
								t.Fatal(err)
							}
							if _, exists := details["cached_tokens"]; exists {
								t.Fatal("omitted first-read count was replaced with a synthetic zero")
							}
						}
						if !wantPass || !strings.HasPrefix(shape.name, "usage_only") {
							return
						}
						for _, exchange := range restored.Observation.Exchanges {
							if _, exists := exchange.Metrics[protocol.MSHCacheWriteMetric5m]; exists {
								t.Fatal("usage-only response acquired a synthetic MSH header metric")
							}
						}
					})
				})
			}
		}
	}
}

func configureKimiTTLShape(fixture *kimiCacheHTTP, shape string) {
	fixture.mutateHeaders = func(headers http.Header, _ int) {
		switch {
		case strings.HasPrefix(shape, "usage_only"),
			shape == "no_ttl", shape == "empty_usage_ttl", shape == "null_usage_ttl":
			headers.Del(protocol.MSHCacheWriteHeader5m)
			headers.Del(protocol.MSHCacheWriteHeader1h)
		case shape == "official" && fixture.protocol == domain.ProtocolAnthropicMessages:
			headers.Del(protocol.MSHCacheWriteHeader5m)
			headers.Del(protocol.MSHCacheWriteHeader1h)
		case shape == "partial_headers_with_usage", shape == "partial_headers_without_usage":
			headers.Del(protocol.MSHCacheWriteHeader1h)
		case shape == "malformed_header_with_usage":
			headers.Set(protocol.MSHCacheWriteHeader5m, "invalid")
		}
	}
	fixture.mutateUsage = func(usage map[string]any, call int) {
		details := usage
		readField, writeField := "cache_read_input_tokens", "cache_creation_input_tokens"
		if fixture.protocol != domain.ProtocolAnthropicMessages {
			name := "prompt_tokens_details"
			if fixture.protocol == domain.ProtocolOpenAIResponses {
				name = "input_tokens_details"
			}
			details = usage[name].(map[string]any)
			readField, writeField = "cached_tokens", "cache_write_tokens"
		}
		body := details["cache_creation"].(map[string]any)
		selected := "ephemeral_5m_input_tokens"
		if strings.Contains(fixture.key, "1h") {
			selected = "ephemeral_1h_input_tokens"
		}
		switch shape {
		case "header_only", "no_ttl", "partial_headers_without_usage":
			delete(details, "cache_creation")
		case "official":
			if fixture.protocol != domain.ProtocolAnthropicMessages {
				delete(details, "cache_creation")
				if call == 1 {
					delete(details, readField)
				}
			}
		case "usage_only_top_level":
			delete(details, "cache_creation")
			usage["cache_creation"] = body
		case "usage_only_both_locations":
			usage["cache_creation"] = body
		case "usage_only_zero_bucket_omitted":
			for field, value := range body {
				if field != selected && value == 0 {
					delete(body, field)
				}
			}
		case "empty_usage_ttl":
			details["cache_creation"] = map[string]any{}
		case "null_usage_ttl":
			details["cache_creation"] = nil
		case "usage_only_wrong_ttl":
			body["ephemeral_5m_input_tokens"], body["ephemeral_1h_input_tokens"] =
				body["ephemeral_1h_input_tokens"], body["ephemeral_5m_input_tokens"]
		case "usage_only_missing_selected":
			delete(body, selected)
		case "conflicting_body_locations":
			usage["cache_creation"] = map[string]any{
				"ephemeral_5m_input_tokens": 1, "ephemeral_1h_input_tokens": 1,
			}
		case "cold_read_null":
			if call == 1 {
				details[readField] = nil
			}
		case "cold_read_string":
			if call == 1 {
				details[readField] = "0"
			}
		case "cold_read_fractional":
			if call == 1 {
				details[readField] = 0.5
			}
		case "usage_only_zero_write_omitted", "usage_only_omitted_write_incomplete_ttl":
			if call > 1 {
				delete(details, writeField)
				if shape == "usage_only_omitted_write_incomplete_ttl" {
					delete(body, selected)
				}
			}
		case "usage_only_no_hit":
			if call > 1 {
				details[readField] = 0
			}
		case "usage_only_missing_hit":
			if call > 1 {
				delete(details, readField)
			}
		}
	}
}
