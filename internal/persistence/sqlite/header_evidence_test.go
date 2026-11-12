package sqlite_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestRepositoryHeaderEvidenceRoundTripsThroughResultsAndReportDetails(t *testing.T) {
	tests := []struct {
		name     string
		request  *testspec.HeaderEvidence
		response *testspec.HeaderEvidence
	}{
		{name: "legacy_not_captured"},
		{
			name:     "captured_empty",
			request:  &testspec.HeaderEvidence{Values: map[string][]string{}},
			response: &testspec.HeaderEvidence{Values: map[string][]string{}},
		},
		{
			name: "sanitized_values",
			request: &testspec.HeaderEvidence{Values: map[string][]string{
				"Authorization": {"[redacted]"},
				"Content-Type":  {"application/json"},
				"X-Trace":       {"first", "second"},
			}},
			response: &testspec.HeaderEvidence{Values: map[string][]string{
				"Content-Type":                    {"application/json"},
				"Msh-Usage-Cache-Write-Tokens-5m": {"3584"},
				"Msh-Usage-Cache-Write-Tokens-1h": {"0"},
			}},
		},
		{
			name: "only_response_captured",
			response: &testspec.HeaderEvidence{Values: map[string][]string{
				"Msh-Usage-Cache-Write-Tokens-5m": {"0"},
			}},
		},
		{
			name: "truncated_stream_response",
			request: &testspec.HeaderEvidence{Values: map[string][]string{
				"Authorization": {"[redacted]"},
			}},
			response: &testspec.HeaderEvidence{
				Values: map[string][]string{
					"Content-Type":                    {"text/event-stream"},
					"Msh-Usage-Cache-Write-Tokens-1h": {"3584"},
				},
				Truncated: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := openOperationalRepository(t)
			defer repository.Close()
			fixture := newRepositoryFixture(t)
			running := createRunningOutputs(t, repository, fixture)
			status := 200
			request := fixture.result
			request.ID = "10000000-0000-4000-8000-000000000020"
			request.RequestID = "header-evidence-request"
			request.Observation = &testspec.Observation{
				Model: "upstream-fixture", Protocol: string(domain.ProtocolOpenAIChat),
				HTTPStatus: &status, Response: json.RawMessage(`{"usage":{"prompt_tokens":3584}}`),
				Metrics: map[string]float64{}, Artifacts: []testspec.Artifact{}, Issues: []testspec.Issue{},
				Exchanges: []testspec.Exchange{{
					Step: "initial", Method: "POST", Path: "/v1/chat/completions",
					RequestBody: json.RawMessage(`{"model":"upstream-fixture","messages":[]}`),
					HTTPStatus:  &status, Response: json.RawMessage(`{"usage":{"prompt_tokens":3584}}`),
					RequestHeaders: tt.request, ResponseHeaders: tt.response,
				}},
			}
			if err := repository.AppendResults(t.Context(), request); err != nil {
				t.Fatal(err)
			}
			stored, err := repository.GetResult(t.Context(), request.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(t, "request result with optional header evidence", request, stored)
			transitionRun(t, repository, running, domain.RunCompleted)
			if err := repository.CreateReport(t.Context(), fixture.report); err != nil {
				t.Fatal(err)
			}
			report, err := repository.GetReport(t.Context(), fixture.report.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(t, "sealed report", fixture.report, report)
			results, err := repository.ListResults(t.Context(), fixture.report.RunID)
			if err != nil {
				t.Fatal(err)
			}
			requests := []domain.Result{}
			for _, result := range results {
				if result.RequestID != "" {
					requests = append(requests, result)
				}
			}
			if len(requests) != 1 {
				t.Fatalf("report request results = %d, want 1", len(requests))
			}
			assertRoundTrip(t, "sealed report request evidence", request, requests[0])
			detail, err := reporting.New(repository).Detail(t.Context(), fixture.report.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Entries) != 1 {
				t.Fatal("report detail lost its entry")
			}
			if len(detail.Entries[0].Cases) != 1 || len(detail.Entries[0].Cases[0].RequestResults) != 1 {
				t.Fatal("report detail lost request ownership")
			}
			got := detail.Entries[0].Cases[0].RequestResults[0].Observation.Exchanges[0]
			if !reflect.DeepEqual(got.RequestHeaders, tt.request) || !reflect.DeepEqual(got.ResponseHeaders, tt.response) {
				t.Fatalf("header evidence changed: request=%+v response=%+v", got.RequestHeaders, got.ResponseHeaders)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if tt.request == nil && strings.Contains(string(encoded), `"request_headers"`) {
				t.Fatal("uncaptured request acquired synthetic headers")
			}
			if tt.response == nil && strings.Contains(string(encoded), `"response_headers"`) {
				t.Fatal("uncaptured response acquired synthetic headers")
			}
			if tt.name == "captured_empty" && (got.RequestHeaders.Values == nil || got.ResponseHeaders.Values == nil) {
				t.Fatal("captured empty headers became uncaptured/null evidence")
			}
		})
	}
}
