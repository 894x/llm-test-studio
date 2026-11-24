package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	casebundle "github.com/894x/llm-test-studio/data/cases"
	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/casecodec"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/protocol"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestProductionKimiTTLProtocolComparison(t *testing.T) {
	ctx := t.Context()
	const planID = "c6c6b79c-1f26-4b4e-9716-fb121b3110b9"
	const suiteID = "cd08ae25-639c-5d5e-b139-a7c787189c78"
	const model = "kimi-k3.1-pro"
	fixture := &protocolCacheFixture{
		seen: map[string]int{}, routes: map[string]int{}, started: make(chan struct{}),
	}
	provider := httptest.NewServer(http.HandlerFunc(fixture.respond))
	defer provider.Close()
	root, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := NewInitializer(Options{
		UserConfigDir: func() (string, error) { return root, nil }, AppVersion: "protocol-comparison-test",
		ExecutablePath:  func() (string, error) { return filepath.Join(executableRoot, "desktop.exe"), nil },
		CredentialStore: credentials.NewMemoryStore(), ProtocolTransport: provider.Client().Transport,
	})(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.Close()

	shortenTTLFixtureWaits(t, dependencies.CatalogCommands)
	channel, err := dependencies.CatalogCommands.CreateChannel(ctx, catalog.CreateChannelCommand{
		Name: "Local TTL fixture", BaseURL: provider.URL + "/v1/chat/completions", APIKey: "fixture-secret", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(root, "llm-test-studio", "llm-test-studio.db")
	repository, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()

	launcher := dependencies.Launcher.(interface {
		PreviewProtocolComparison(context.Context, string, string) (runs.ProtocolComparisonPreview, error)
	})
	tests := []struct {
		name        string
		plan        string
		suite       string
		cases       int
		requests    int
		preparation runs.ProtocolComparisonCommand
	}{
		{name: "plan", plan: planID, cases: 12, requests: 18, preparation: runs.ProtocolComparisonCommand{
			Plan: &runs.StartCommand{PlanID: planID, CaseConcurrency: 2, Connection: &runs.ConnectionCommand{
				Protocol: domain.ProtocolOpenAIChat, Model: model, ChannelID: channel.ID,
			}},
		}},
		{name: "suite", suite: suiteID, cases: 10, requests: 14, preparation: runs.ProtocolComparisonCommand{
			Suite: &runs.QuickTaskCommand{SuiteID: suiteID, Model: model, ChannelID: channel.ID,
				Seed: 419065, CaseConcurrency: 2, RequestTimeoutSeconds: 3, Inputs: map[string]json.RawMessage{}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preview, err := launcher.PreviewProtocolComparison(ctx, test.plan, test.suite)
			if err != nil || len(preview.Targets) != 3 {
				t.Fatalf("preview: %+v, %v", preview, err)
			}
			for _, target := range preview.Targets {
				if !target.Available || target.Cases != test.cases || target.Requests == nil || *target.Requests != test.requests {
					t.Fatalf("preview target: %+v", target)
				}
			}
			accepted, err := dependencies.Launcher.Launch(ctx, jobs.LaunchCommand{
				Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchComparison,
				Comparison: &comparisons.StartCommand{Protocols: &test.preparation},
			})
			if err != nil || accepted.State != jobs.SubmissionAccepted || len(accepted.Receipt.RunIDs) != 3 {
				t.Fatalf("submit: %s %v", accepted.State, err)
			}
			capabilities := jobhost.CoreCapabilities()
			if !capabilities.SupportsJob(accepted.Prepared.Spec) {
				t.Fatal("local host rejected protocol comparison capability")
			}
			capabilities.Features = slices.DeleteFunc(capabilities.Features, func(feature string) bool {
				return feature == jobcontract.FeatureProtocolComparison
			})
			if capabilities.SupportsJob(accepted.Prepared.Spec) {
				t.Fatal("older runner accepted the new batch format")
			}
			checkProtocolBatchReport(t, dependencies, repository, accepted, test.cases, test.suite != "")
		})
	}
	t.Run("reject missing protocol", func(t *testing.T) {
		snapshot, err := dependencies.Catalog.Snapshot(ctx, catalog.ScopeDefinitions)
		if err != nil {
			t.Fatal(err)
		}
		missingSuiteID := ""
		for _, suite := range snapshot.Suites {
			if suite.Key == "kimi-k3.1-pro-highspeed.parameter-rejection" {
				missingSuiteID = suite.ID
				break
			}
		}
		if missingSuiteID == "" {
			t.Fatal("single-protocol fixture suite is missing")
		}
		preview, err := launcher.PreviewProtocolComparison(ctx, "", missingSuiteID)
		if err != nil || len(preview.Targets) != 3 || !preview.Targets[0].Available || preview.Targets[1].Available {
			t.Fatalf("missing-protocol preview: %+v, %v", preview, err)
		}
		_, err = dependencies.Launcher.Launch(ctx, jobs.LaunchCommand{
			Location: jobs.Location{Kind: jobcontract.TargetLocal}, Kind: jobs.LaunchComparison,
			Comparison: &comparisons.StartCommand{Protocols: &runs.ProtocolComparisonCommand{
				Suite: &runs.QuickTaskCommand{
					SuiteID: missingSuiteID, Model: model, ChannelID: channel.ID,
					RequestTimeoutSeconds: 3, Inputs: map[string]json.RawMessage{},
				},
			}},
		})
		if !errors.Is(err, runs.ErrNotRunnable) {
			t.Fatalf("missing protocol admitted: %v", err)
		}
	})
	reopened, err := sqlite.OpenRepository(ctx, database, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	history, err := reopened.ComparisonHistory(ctx, "", nil)
	if err != nil || len(history.Comparisons) != 2 {
		t.Fatalf("reopened history should contain the two accepted batches: %+v, %v", history, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.routes) != 3 {
		t.Fatalf("requested routes: %+v", fixture.routes)
	}
	for route, count := range fixture.routes {
		if count != 32 {
			t.Errorf("%s sent %d requests, want 32", route, count)
		}
	}
}

func shortenTTLFixtureWaits(t *testing.T, commands bridge.CatalogCommands) {
	t.Helper()
	paths, err := fs.Glob(casebundle.Bundle, "kimi-cache-ttl/*/case.json")
	if err != nil || len(paths) != 2 {
		t.Fatalf("load TTL fixtures: %v, %v", paths, err)
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
		for _, protocol := range item.Protocols() {
			definition, err := item.SpecFor(protocol)
			if err != nil || definition.Workflow == nil || len(definition.Workflow.Steps) != 1 {
				t.Fatalf("TTL definition: %v", err)
			}
			step := &definition.Workflow.Steps[0]
			if step.DelayMS != 360000 || step.ReuseRequestFrom != "initial" {
				t.Fatalf("unexpected TTL workflow: %+v", step)
			}
			step.DelayMS = 1
			if !shortenTTLWaitAssertion(definition.Assertions) {
				t.Fatal("TTL wait assertion is missing")
			}
			raw, err = json.Marshal(definition)
			if err != nil {
				t.Fatal(err)
			}
			item.Definitions[protocol] = raw
		}
		_, err = commands.UpdateTestCase(t.Context(), catalog.UpdateTestCaseCommand{
			ID: item.ID, ExpectedRevision: item.Revision, Key: item.Key, Name: item.Name,
			Description: item.Description, Dimension: item.Dimension, Enabled: item.Enabled,
			Default: item.Default, Severity: item.Severity, ExecutionMode: item.ExecutionMode, Definitions: item.Definitions,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func shortenTTLWaitAssertion(assertions []testspec.Assertion) bool {
	for index := range assertions {
		assertion := &assertions[index]
		if assertion.ID == "waited_6m" {
			assertion.Value = json.RawMessage("1")
			return true
		}
		if shortenTTLWaitAssertion(assertion.All) || shortenTTLWaitAssertion(assertion.Any) {
			return true
		}
	}
	return false
}

func checkProtocolBatchReport(
	t *testing.T,
	dependencies bridge.Dependencies,
	repository *sqlite.Repository,
	accepted jobs.SubmissionRecord,
	cases int,
	suite bool,
) {
	t.Helper()
	deadline := time.Now().Add(35 * time.Second)
	var reports []domain.Report
	for {
		job, err := repository.GetJob(t.Context(), accepted.Prepared.Spec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		reports, err = repository.ListReportsForRuns(t.Context(), accepted.Receipt.RunIDs)
		if err != nil {
			t.Fatal(err)
		}
		if job.Receipt.Status == domain.RunCompleted && len(reports) == 3 {
			break
		}
		if job.Receipt.Status == domain.RunFailed || time.Now().After(deadline) {
			t.Fatalf("batch did not settle: %s %+v, reports: %d", job.Receipt.Status, job.Failure, len(reports))
		}
		time.Sleep(20 * time.Millisecond)
	}
	page, err := repository.ComparisonHistory(t.Context(), "", []string{accepted.Receipt.ComparisonID})
	if err != nil || len(page.Comparisons) == 0 {
		t.Fatalf("read durable comparison: %+v %v", page, err)
	}
	group := page.Comparisons[0]
	if group.ID != accepted.Receipt.ComparisonID || group.JobID != accepted.Prepared.Spec.JobID || len(group.Channels) != 3 {
		t.Fatalf("wrong durable comparison: %+v", group)
	}
	details := make([]reporting.Detail, 0, 3)
	query := dependencies.Reports.(bridge.ReportDocumentQuery)
	for _, lane := range group.Channels {
		if lane.Protocol == "" || lane.ChannelID != group.Channels[0].ChannelID || !lane.ReportReady || lane.Suite != suite {
			t.Fatalf("invalid persisted lane: %+v", lane)
		}
		if lane.CaseProgress == nil || lane.CaseProgress.Completed != cases || lane.CaseProgress.Total != cases {
			t.Fatalf("invalid persisted lane: %+v", lane)
		}
		for _, report := range reports {
			if report.RunID != lane.RunID {
				continue
			}
			detail, err := query.Detail(t.Context(), report.ID)
			if err != nil {
				t.Fatal(err)
			}
			details = append(details, detail)
		}
	}
	comparison, err := reporting.Compare(details, details[0].Report.ID)
	if err != nil || len(comparison.ProtocolSummary) != 3 || len(comparison.Rows) != cases {
		t.Fatalf("matrix protocol count %d, row count %d, error %v", len(comparison.ProtocolSummary), len(comparison.Rows), err)
	}
	for _, summary := range comparison.ProtocolSummary {
		wantFailures := 0
		if summary.Protocol == domain.ProtocolOpenAIResponses {
			wantFailures = 10
		}
		if summary.Failed != wantFailures || summary.Passed != cases-wantFailures {
			t.Errorf("unexpected protocol verdict: %+v", summary)
		}
	}
}

type protocolCacheFixture struct {
	mu      sync.Mutex
	seen    map[string]int
	routes  map[string]int
	started chan struct{}
}

func (fixture *protocolCacheFixture) respond(w http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	path := request.URL.Path
	key := path + string(body)
	fixture.mu.Lock()
	fixture.seen[key]++
	attempt := fixture.seen[key]
	first := fixture.routes[path] == 0
	fixture.routes[path]++
	if first && len(fixture.routes) == 3 {
		close(fixture.started)
	}
	fixture.mu.Unlock()

	select {
	case <-fixture.started:
	case <-time.After(5 * time.Second):
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var input struct {
		Stream bool `json:"stream"`
		Cache  struct {
			TTL string `json:"ttl"`
		} `json:"prompt_cache_options"`
		Control struct {
			TTL string `json:"ttl"`
		} `json:"cache_control"`
	}
	if json.Unmarshal(body, &input) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	read, write := 0, 512
	if attempt > 1 {
		read, write = 512, 0
	}
	short, long := write, 0
	if input.Cache.TTL == "1h" || input.Control.TTL == "1h" {
		short, long = 0, write
	}
	w.Header().Set(protocol.MSHCacheWriteHeader5m, strconv.Itoa(short))
	if path != "/v1/responses" {
		w.Header().Set(protocol.MSHCacheWriteHeader1h, strconv.Itoa(long))
	}
	creation := map[string]int{"ephemeral_5m_input_tokens": short, "ephemeral_1h_input_tokens": long}
	cache := map[string]any{"cached_tokens": read, "cache_write_tokens": write, "cache_creation": creation}
	message := map[string]any{"role": "assistant", "content": "OK"}
	root := map[string]any{"id": "cache-fixture", "model": "kimi-k3.1-pro"}
	var usage map[string]any
	switch path {
	case "/v1/chat/completions":
		root["object"] = "chat.completion"
		root["choices"] = []any{map[string]any{"index": 0, "message": message, "finish_reason": "stop"}}
		usage = map[string]any{"prompt_tokens": 1024, "completion_tokens": 1, "total_tokens": 1025, "prompt_tokens_details": cache}
	case "/v1/responses":
		root["object"], root["status"] = "response", "completed"
		root["output"] = []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "output_text", "text": "OK"}}}}
		usage = map[string]any{"input_tokens": 1024, "output_tokens": 1, "total_tokens": 1025, "input_tokens_details": cache}
	case "/v1/messages":
		root["type"], root["role"], root["stop_reason"] = "message", "assistant", "end_turn"
		root["content"] = []any{map[string]any{"type": "text", "text": "OK"}}
		usage = map[string]any{"input_tokens": 512, "output_tokens": 1, "cache_read_input_tokens": read,
			"cache_creation_input_tokens": write, "cache_creation": creation}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	root["usage"] = usage
	if !input.Stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(root)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(value any) {
		raw, _ := json.Marshal(value)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	switch path {
	case "/v1/chat/completions":
		root["object"] = "chat.completion.chunk"
		root["choices"] = []any{map[string]any{"index": 0, "delta": message, "finish_reason": "stop"}}
		delete(root, "usage")
		emit(root)
		root["choices"], root["usage"] = []any{}, usage
		emit(root)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	case "/v1/responses":
		emit(map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{
			"id": "cache-fixture", "object": "response", "status": "in_progress", "output": []any{},
		}})
		emit(map[string]any{"type": "response.completed", "sequence_number": 1, "response": root})
	case "/v1/messages":
		root["content"], root["stop_reason"] = []any{}, nil
		emit(map[string]any{"type": "message_start", "message": root})
		emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"},
			"usage": map[string]int{"output_tokens": 1}})
		emit(map[string]any{"type": "message_stop"})
	}
}
