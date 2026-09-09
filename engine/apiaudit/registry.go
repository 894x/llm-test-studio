package apiaudit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/protocols"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func SupportedProtocols() []string {
	result := []string{}
	registry := protocols.NewRegistry()
	for _, descriptor := range protocol.All() {
		for _, module := range registry.Descriptors() {
			if module.ID == descriptor.ID {
				result = append(result, descriptor.ID)
			}
		}
	}
	return result
}

func containsProtocol(protocol string) bool {
	return slices.Contains(SupportedProtocols(), protocol)
}

// RunCase is the CLI presentation boundary for the same runtime used by desktop.
func RunCase(ctx context.Context, doer HTTPDoer, config RunConfig, planned PlannedRun) (CaseResult, error) {
	registry := protocols.NewRegistry()
	if planned.Case.Protocol != config.Suite {
		return CaseResult{}, fmt.Errorf("case %s does not use protocol %s", planned.Case.ID, config.Suite)
	}
	if err := registry.Validate(config.Suite, planned.Case.Spec); err != nil {
		return CaseResult{}, err
	}
	secret := config.APIKey
	if config.DryRun && secret == "" {
		secret = "dry-run-unused-credential"
	}
	lease, err := credentials.NewTemporaryLease([]byte(secret))
	if err != nil {
		return CaseResult{}, err
	}
	defer lease.Close()
	preview := &previewTransport{}
	var transport http.RoundTripper = doerTransport{doer: doer}
	if client, ok := doer.(*http.Client); ok {
		transport = client.Transport
	}
	if doer == nil {
		transport = nil
	}
	if config.DryRun {
		transport = preview
	}
	channel := domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: "00000000-0000-4000-8000-000000000001", Revision: 1},
		Name:              "CLI", BaseURL: config.BaseURL, Protocol: domain.Protocol(config.Suite), UpstreamModelName: planned.Model,
	}
	client, err := protocols.NewClient(registry, lease, channel, transport)
	if err != nil {
		return CaseResult{}, err
	}
	defer client.Close()
	started := time.Now()
	result, err := client.Execute(ctx, protocols.Execution{
		Spec:   planned.Case.Spec,
		Random: testspec.RandomContext{Seed: config.Seed, ExecutionItemID: planned.ResultID, MemberID: planned.Case.ID},
		Inputs: config.Inputs,
		Settings: testspec.RunSettings{
			TimeoutMS: durationMS(config.Timeout), PollIntervalMS: durationMS(config.PollInterval),
			TaskTimeoutMS: durationMS(config.Timeout),
		},
	})
	if err != nil {
		return CaseResult{}, err
	}
	outcome := CaseResult{
		ID: planned.ResultID, Name: planned.Case.Name, Dimension: planned.Case.Dimension,
		Protocol: config.Suite, Model: planned.Model, Severity: planned.Case.Severity,
		ElapsedMS: time.Since(started).Milliseconds(), Verification: result.Verdict,
		Status: StatusUnknown, Observation: &result.Observation,
		Evidence: string(result.Verdict.Status), Metrics: map[string]any{},
	}
	if config.DryRun {
		if len(preview.exchanges) == 0 {
			return CaseResult{}, errors.New("dry-run could not generate the initial request; check case inputs and template")
		}
		outcome.Observation = nil
		outcome.Verification = testspec.Verdict{Status: testspec.VerdictIndeterminate, Assertions: []testspec.AssertionResult{}}
		outcome.Evidence = "dry-run: initial request prepared; no HTTP request sent or assertions evaluated"
		outcome.Exchanges = preview.exchanges
		return RedactCaseResult(outcome, config.APIKey), nil
	}
	switch result.Verdict.Status {
	case testspec.VerdictPassed:
		outcome.Status = StatusPass
	case testspec.VerdictFailed:
		outcome.Status = StatusFail
	case testspec.VerdictNotApplicable:
		outcome.Status = StatusObserved
	}
	if result.Observation.HTTPStatus != nil {
		outcome.HTTPStatus = *result.Observation.HTTPStatus
	}
	for name, value := range result.Observation.Metrics {
		outcome.Metrics[name] = value
	}
	if len(result.Observation.Usage) > 0 {
		_ = json.Unmarshal(result.Observation.Usage, &outcome.Usage)
	}
	for _, exchange := range result.Observation.Exchanges {
		item := HTTPExchange{Method: exchange.Method, URL: exchange.Path, ResponseBody: string(exchange.Response)}
		if exchange.HTTPStatus != nil {
			item.StatusCode = *exchange.HTTPStatus
		}
		if len(exchange.RequestBody) > 0 {
			_ = json.Unmarshal(exchange.RequestBody, &item.RequestBody)
		}
		outcome.Exchanges = append(outcome.Exchanges, item)
	}
	return RedactCaseResult(outcome, config.APIKey), nil
}

func durationMS(value time.Duration) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(max(1, value.Milliseconds()))
}

type doerTransport struct{ doer HTTPDoer }

func (transport doerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport.doer.Do(request)
}

// Preview stops at the shared HTTP boundary before any network operation.
type previewTransport struct{ exchanges []HTTPExchange }

func (transport *previewTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	item := HTTPExchange{Method: request.Method, URL: redactURL(request.URL.String())}
	if request.Body != nil && request.Body != http.NoBody {
		defer request.Body.Close()
		if err := json.NewDecoder(request.Body).Decode(&item.RequestBody); err != nil {
			return nil, err
		}
	}
	transport.exchanges = append(transport.exchanges, item)
	return nil, errors.New("dry-run preview complete")
}
