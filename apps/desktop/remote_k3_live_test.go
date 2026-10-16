package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const liveK3Enable = "LLM_TEST_LIVE_REMOTE_K3"

// Credentials are JSON base64 byte strings read exclusively from stdin. Build
// the test binary first, then supply this document through an anonymous pipe:
// endpoint {Host,Port,User,Fingerprint,HostKeyType}, password OR private_key,
// optional passphrase, api_key, base_url, model, expected_node_id,
// expected_installation_id, optional expected_version/expected_build,
// case_concurrency (1..8, default 1), timeout_minutes (10..90, default 90),
// disconnect_seconds (5..60, default 15). Never put the document in argv, an
// environment variable, a temporary input file, or a test log.
type liveK3Input struct {
	Endpoint             remotessh.Endpoint `json:"endpoint"`
	Password             []byte             `json:"password"`
	PrivateKey           []byte             `json:"private_key"`
	Passphrase           []byte             `json:"passphrase"`
	APIKey               []byte             `json:"api_key"`
	BaseURL              string             `json:"base_url"`
	Model                string             `json:"model"`
	ExpectedNodeID       string             `json:"expected_node_id"`
	ExpectedInstallation string             `json:"expected_installation_id"`
	ExpectedVersion      string             `json:"expected_version"`
	ExpectedBuild        string             `json:"expected_build"`
	CaseConcurrency      uint32             `json:"case_concurrency"`
	TimeoutMinutes       uint32             `json:"timeout_minutes"`
	DisconnectSeconds    uint32             `json:"disconnect_seconds"`
}

func (liveK3Input) String() string   { return "liveK3Input{redacted}" }
func (liveK3Input) GoString() string { return "liveK3Input{redacted}" }
func (liveK3Input) MarshalJSON() ([]byte, error) {
	return nil, errors.New("live credentials cannot be serialized")
}

type liveK3Usage struct {
	Boundary         string `json:"boundary"`
	ObservedRequests uint64 `json:"observed_requests"`
	InputTokens      uint64 `json:"input_tokens"`
	OutputTokens     uint64 `json:"output_tokens"`
	CachedTokens     uint64 `json:"cached_tokens"`
}

type liveK3CaseFailure struct {
	CaseID         string `json:"case_id"`
	Key            string `json:"key"`
	Classification string `json:"classification"`
}

type liveK3Performance struct {
	ReportID                         string                         `json:"report_id"`
	SealedAt                         string                         `json:"sealed_at"`
	ObservationState                 string                         `json:"observation_state"`
	TerminalOriginVerified           bool                           `json:"terminal_origin_verified"`
	CheckpointOriginPresent          bool                           `json:"checkpoint_origin_present"`
	StatisticalArchiveOriginVerified bool                           `json:"statistical_archive_origin_verified"`
	SampleCount                      *int                           `json:"sample_count"`
	Profile                          *quicktest.PerformanceProfile  `json:"profile"`
	Progress                         *quicktest.PerformanceProgress `json:"progress"`
	Metrics                          *load.Metrics                  `json:"metrics"`
}

// This allow-listed document never embeds a domain report, observation,
// request/response evidence, provider URL/model, node endpoint, or credentials.
type liveK3JobSummary struct {
	JobID               string              `json:"job_id"`
	RunID               string              `json:"run_id,omitempty"`
	ReportID            string              `json:"report_id,omitempty"`
	SuiteKey            string              `json:"suite_key,omitempty"`
	Executor            jobs.ExecutorView   `json:"executor"`
	Delivery            string              `json:"delivery"`
	Status              string              `json:"status"`
	ExpectedCases       int                 `json:"expected_cases"`
	ActualCases         int                 `json:"actual_cases"`
	CaseIdentitiesMatch bool                `json:"case_identities_match"`
	Passed              uint64              `json:"passed"`
	Failed              uint64              `json:"failed"`
	Observed            uint64              `json:"observed"`
	Indeterminate       uint64              `json:"indeterminate"`
	FailureCases        []liveK3CaseFailure `json:"failure_cases"`
	Usage               *liveK3Usage        `json:"usage"`
	Performance         *liveK3Performance  `json:"performance"`
	RunnerReportOrigin  bool                `json:"runner_report_origin"`
}

type liveK3Summary struct {
	SchemaVersion           int                  `json:"schema_version"`
	Passed                  bool                 `json:"passed"`
	StartedAt               string               `json:"started_at"`
	CompletedAt             string               `json:"completed_at"`
	ObserverClosedAt        string               `json:"observer_closed_at"`
	ObserverReopenedAt      string               `json:"observer_reopened_at"`
	ObserverRestartVerified bool                 `json:"observer_restart_verified"`
	ContinuedWhileClosed    bool                 `json:"continued_while_closed"`
	ObserverAudit           *liveK3ObserverAudit `json:"observer_audit"`
	OfflineReopenVerified   bool                 `json:"offline_reopen_verified"`
	Jobs                    []liveK3JobSummary   `json:"jobs"`
	Failures                []string             `json:"failures"`
}

type liveK3Suite struct {
	key      string
	protocol domain.Protocol
	count    int
	suite    catalog.SuiteSummary
	caseKeys map[string]string
}

type liveK3ObserverAudit struct {
	BeforeCloseSequence    uint64 `json:"before_close_sequence"`
	ClosedBaselineSequence uint64 `json:"closed_baseline_sequence"`
	ObservedSequence       uint64 `json:"observed_sequence"`
	Outcome                string `json:"outcome"`
}

type liveK3AuditSettings struct {
	ConfigurationRoot string
	Credentials       credentials.Store
	ConfigurationID   string
	Expected          jobs.JobView
	Window            time.Duration
}

// Both observations happen after DesktopApp.shutdown has returned and before
// another DesktopApp starts. This independent management connection cannot
// launch, control, synchronize or execute a task. Sequence growth is durable
// runner progress, without comparing clocks on different machines.
func observeLiveK3ClosedDesktop(ctx context.Context, settings liveK3AuditSettings) liveK3ObserverAudit {
	result := liveK3ObserverAudit{Outcome: "unavailable"}
	if settings.Expected.Execution == nil || settings.Window <= 0 {
		return result
	}
	result.BeforeCloseSequence = settings.Expected.Execution.LatestSequence
	ctx, cancel := context.WithTimeout(ctx, settings.Window)
	defer cancel()
	connection, err := openLiveK3Audit(ctx, settings)
	if err != nil {
		return result
	}
	defer connection.Close()
	baseline, err := connection.GetJob(ctx, settings.Expected.JobID)
	if err != nil || !liveK3AuditIdentity(settings.Expected, baseline) {
		return result
	}
	result.ClosedBaselineSequence, result.ObservedSequence = baseline.LatestSequence, baseline.LatestSequence
	if baseline.LatestSequence < result.BeforeCloseSequence {
		result.Outcome = "sequence_regressed"
		return result
	}
	if jobcontract.Terminal(baseline.Receipt.Status) {
		// Work may have completed before the first closed-window observation.
		// Its receipt survives the restart, but that is a different fact.
		result.Outcome = "already_terminal"
		return result
	}
	for {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			result.Outcome = "no_advance_observed"
			return result
		case <-timer.C:
		}
		current, err := connection.GetJob(ctx, settings.Expected.JobID)
		if err != nil {
			continue
		}
		result.ObservedSequence = current.LatestSequence
		result.Outcome = classifyLiveK3Audit(settings.Expected, baseline, current)
		if result.Outcome != "unchanged" {
			return result
		}
	}
}

func openLiveK3Audit(ctx context.Context, settings liveK3AuditSettings) (runnerConnection, error) {
	directory, _, err := productionStoragePaths(settings.ConfigurationRoot)
	if err != nil {
		return nil, jobs.ErrInvalid
	}
	directory = filepath.Join(directory, "remote-nodes")
	scope, err := credentialScopeForRoot(directory)
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	keys, err := credentials.NewScopedStore(settings.Credentials, scope)
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	store, err := remotenodes.NewFileStore(filepath.Join(directory, "nodes.json"))
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	// No Manager, watcher, cleanup, Save or credential mutation is created.
	configuration, err := remotenodes.New(remotenodes.Dependencies{Store: store, Credentials: keys})
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	snapshot, err := configuration.Snapshot(ctx)
	if err != nil {
		return nil, jobs.ErrUnavailable
	}
	for _, node := range snapshot.Nodes {
		if node.ID != settings.ConfigurationID {
			continue
		}
		executor := settings.Expected.Executor
		if node.RunnerNodeID != executor.NodeID || node.InstallationID != executor.InstallationID || node.HostKey == nil {
			return nil, jobs.ErrTargetChanged
		}
		return openProductionRunner(ctx, configuration, remotesync.Node{Revision: node.Revision,
			Origin: remoteresults.Origin{ConfigurationID: node.ID,
				NodeID: node.RunnerNodeID, InstallationID: node.InstallationID}}, jobs.ObservationAccess)
	}
	return nil, jobs.ErrNotFound
}

func liveK3AuditIdentity(expected jobs.JobView, snapshot jobcontract.Snapshot) bool {
	receipt := snapshot.Receipt
	owner := receipt.JobID == expected.JobID && receipt.Kind == expected.Kind &&
		receipt.ComparisonID == expected.ComparisonID
	children := slices.Equal(receipt.RunIDs, expected.RunIDs) && slices.Equal(receipt.ReportIDs, expected.ReportIDs)
	return snapshot.ValidateState() == nil && owner && children && liveK3OriginMatches(&receipt.Origin, expected.Executor)
}

func classifyLiveK3Audit(expected jobs.JobView, baseline, current jobcontract.Snapshot) string {
	matching := liveK3AuditIdentity(expected, baseline) && liveK3AuditIdentity(expected, current) &&
		current.Receipt.AcceptedAt.Equal(baseline.Receipt.AcceptedAt)
	if !matching {
		return "identity_changed"
	}
	if current.LatestSequence < baseline.LatestSequence {
		return "sequence_regressed"
	}
	if current.LatestSequence > baseline.LatestSequence {
		return "advanced"
	}
	return "unchanged"
}

// This test is intentionally excluded from ordinary runs. It submits at most
// three paid jobs, once each. Delivery uncertainty is resolved only by queries;
// neither a pending response nor an error can create a replacement job.
func TestLiveRemoteK3DesktopExecution(t *testing.T) {
	if os.Getenv(liveK3Enable) != "1" {
		t.Skip("explicit physical-runner K3 acceptance only; bounded configuration must arrive on stdin")
	}
	input := readLiveK3Input(t)
	defer func() {
		clear(input.Password)
		clear(input.PrivateKey)
		clear(input.Passphrase)
		clear(input.APIKey)
	}()
	ctx, cancel := context.WithTimeout(t.Context(), time.Duration(input.TimeoutMinutes)*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "llm-remote-k3-live-")
	if err != nil {
		t.Fatal("cannot create the isolated live workspace")
	}
	// Deliberately retain the isolated DB and safe summary for acceptance review.
	t.Logf("live workspace=%s", root)
	summary := liveK3Summary{SchemaVersion: 1, StartedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Jobs: []liveK3JobSummary{}, Failures: []string{}}
	defer func() {
		if t.Failed() && len(summary.Failures) == 0 {
			summary.Failures = append(summary.Failures, "acceptance_incomplete")
		}
		summary.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		summary.Passed = !t.Failed() && len(summary.Failures) == 0 && summary.OfflineReopenVerified
		writeLiveK3Summary(t, root, summary)
	}()
	configurationRoot, executableRoot := filepath.Join(root, "configuration"), filepath.Join(root, "catalog")
	artifactRoot := filepath.Join(root, "no-runner-artifacts")
	for _, directory := range []string{configurationRoot, executableRoot, artifactRoot} {
		if os.MkdirAll(directory, 0o700) != nil {
			t.Fatal("cannot prepare the isolated live workspace")
		}
	}
	// A real empty DirectorySource prevents this acceptance check from silently
	// installing/upgrading a runner. Existing healthy runners need no artifacts.
	source, err := runnerartifacts.NewDirectorySource(artifactRoot)
	if err != nil {
		t.Fatal("cannot disable automatic runner installation")
	}
	store := credentials.NewMemoryStore()
	options := productionOptions{userConfigDir: func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(executableRoot, "live-desktop.exe"), nil },
		appVersion:     "live-remote-k3-harness", credentialStore: store, runnerSource: source}
	app := startLiveK3App(t, ctx, options)
	defer func() {
		if app != nil {
			_ = app.shutdown()
		}
	}()
	suites := liveK3FullSuites(t, app)
	nodeID := pairLiveK3Node(t, ctx, app, input)
	for index := range 3 {
		location := liveK3RemoteDefault(t, app, nodeID, input)
		command := jobs.LaunchCommand{Location: location, Kind: jobs.LaunchPerformance,
			Performance: &quicktest.PerformanceCommand{Protocol: domain.ProtocolOpenAIChat,
				AddressMode: quicktest.AddressModeBaseURL, URL: input.BaseURL, APIKey: string(input.APIKey), ModelID: input.Model,
				LoadMode: domain.LoadFixedConcurrency, WorkloadMode: quicktest.PerformanceWorkloadFixed,
				RequestCount: 3, Concurrency: 1, TimeoutMS: 120_000, InputTokens: 32, OutputTokens: 128}}
		if index < 2 {
			command.Kind, command.Performance = jobs.LaunchQuickTask, nil
			command.QuickTask = &runs.QuickTaskCommand{SuiteID: suites[index].suite.ID, Model: input.Model,
				BaseURL: input.BaseURL, APIKey: string(input.APIKey), RequestTimeoutMS: 120_000,
				CaseConcurrency: input.CaseConcurrency, Inputs: map[string]json.RawMessage{}}
		}
		view, launchErr := app.LaunchJob(command) // Exactly one call per job; never retry this line.
		if launchErr != nil {
			// Retain any persisted identity even when a binding rejects. Reads
			// cannot issue another submission or conceal an uncertain paid job.
			if outbox, readErr := app.GetJobs("", 25); readErr == nil {
				known := map[string]bool{}
				for _, item := range summary.Jobs {
					known[item.JobID] = true
				}
				for _, candidate := range outbox.Jobs {
					if !known[candidate.JobID] {
						summary.Jobs = append(summary.Jobs, liveK3JobSummary{JobID: candidate.JobID,
							Executor: candidate.Executor, Delivery: string(candidate.Delivery), FailureCases: []liveK3CaseFailure{}})
					}
				}
			}
			summary.Failures = append(summary.Failures, "launch_unconfirmed_no_resubmission")
			t.Fatal("live launch failed; no replacement job was submitted")
		}
		assertLiveK3Executor(t, view, input)
		item := liveK3JobSummary{JobID: view.JobID, Executor: view.Executor, Delivery: string(view.Delivery),
			FailureCases: []liveK3CaseFailure{}}
		if index < 2 {
			item.SuiteKey, item.ExpectedCases = suites[index].key, suites[index].count
		}
		summary.Jobs = append(summary.Jobs, item)
		writeLiveK3Summary(t, root, summary)
		view = waitLiveK3Job(t, ctx, app, view.JobID, true)
		assertLiveK3Executor(t, view, input)
		assertLiveK3Outbox(t, app, summary.Jobs)
		if index == 0 {
			if view.Execution == nil || jobcontract.Terminal(view.Execution.Status) {
				summary.Failures = append(summary.Failures, "first_job_not_running_at_observer_close")
			}
			if app.shutdown() != nil {
				t.Fatal("cannot close the desktop observer cleanly")
			}
			app = nil
			summary.ObserverClosedAt = time.Now().UTC().Format(time.RFC3339Nano)
			audit := observeLiveK3ClosedDesktop(ctx, liveK3AuditSettings{
				ConfigurationRoot: configurationRoot, Credentials: store, ConfigurationID: nodeID,
				Expected: view, Window: time.Duration(input.DisconnectSeconds) * time.Second,
			})
			summary.ObserverAudit = &audit
			summary.ContinuedWhileClosed = audit.Outcome == "advanced"
			summary.ObserverReopenedAt = time.Now().UTC().Format(time.RFC3339Nano)
			app = startLiveK3App(t, ctx, options)
			reopened, readErr := app.GetJob(view.JobID)
			if readErr != nil || reopened.JobID != view.JobID || reopened.Location != view.Location {
				t.Fatal("reopening the same workspace lost accepted work")
			}
			assertLiveK3Executor(t, reopened, input)
			summary.ObserverRestartVerified = true
		}
		view = waitLiveK3Job(t, ctx, app, view.JobID, false)
		assertLiveK3Executor(t, view, input)
		item = summary.Jobs[index]
		item.Executor, item.Delivery, item.Status = view.Executor, string(view.Delivery), string(view.Execution.Status)
		if index < 2 {
			detail := waitLiveK3Report(t, ctx, app, view)
			collectLiveK3Suite(&item, detail, suites[index], view.Executor)
			if item.ActualCases != item.ExpectedCases || !item.CaseIdentitiesMatch || !item.RunnerReportOrigin {
				summary.Failures = append(summary.Failures, "suite_incomplete_or_runner_origin_mismatch")
			}
			if len(item.FailureCases) != 0 || !detail.Report.Conclusion.Passed {
				summary.Failures = append(summary.Failures, "authored_suite_assertions_failed")
			}
		} else {
			result := waitLiveK3Performance(t, ctx, app, view.JobID)
			item.ReportID = result.ReportID
			item.Performance = summarizeLiveK3Performance(result)
			item.Performance.TerminalOriginVerified = liveK3PerformanceOriginMatches(result, view)
			if result.Observation != nil {
				item.Performance.CheckpointOriginPresent = result.Observation.Report.ExecutionOrigin != nil
			}
			archive := waitLiveK3Archive(t, ctx, app, result.ReportID)
			item.Performance.StatisticalArchiveOriginVerified = liveK3ArchiveOriginMatches(archive, result)
			item.RunnerReportOrigin = item.Performance.TerminalOriginVerified && item.Performance.StatisticalArchiveOriginVerified
			if !liveK3MinimumPerformance(result) || !item.RunnerReportOrigin {
				summary.Failures = append(summary.Failures, "minimum_performance_incomplete_or_failed")
			}
		}
		summary.Jobs[index] = item
		writeLiveK3Summary(t, root, summary)
	}
	if app.shutdown() != nil {
		t.Fatal("cannot close the desktop before offline verification")
	}
	app = nil
	// Explicit deny transports return no fabricated server/provider data. They
	// make an ordinary production-initializer reopen physically unable to read
	// the runner while the public query methods read the durable local mirror.
	offline := options
	offline.remoteConnector = func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
		return nil, remotessh.ErrConnection
	}
	offline.runnerConnector = func(context.Context, *remotenodes.Service, remotesync.Node, jobs.Access) (runnerConnection, error) {
		return nil, jobs.ErrUnavailable
	}
	app = startLiveK3App(t, ctx, offline)
	assertLiveK3Outbox(t, app, summary.Jobs)
	for index, item := range summary.Jobs {
		view, readErr := app.GetJob(item.JobID)
		if readErr != nil || view.Execution == nil || !jobcontract.Terminal(view.Execution.Status) {
			t.Fatal("terminal jobs are not readable after an offline production reopen")
		}
		assertLiveK3Executor(t, view, input)
		if index < 2 {
			detail, reportErr := app.GetReportDetail(item.ReportID)
			if reportErr != nil || detail.Report.ID != item.ReportID || len(detail.Report.CaseResults) != item.ActualCases ||
				!liveK3OriginMatches(detail.Report.ExecutionOrigin, view.Executor) {
				t.Fatal("complete suite report is not readable offline")
			}
		} else {
			result, performanceErr := app.GetJobPerformanceResult(item.JobID)
			if performanceErr != nil || result.SealedAt != item.Performance.SealedAt || result.ReportID != item.ReportID ||
				!liveK3PerformanceOriginMatches(result, view) {
				t.Fatal("sealed performance result is not readable offline")
			}
			archive, archiveErr := app.GetReportDetail(item.ReportID)
			if archiveErr != nil || archive.Performance == nil || !liveK3ArchiveOriginMatches(*archive.Performance, result) {
				t.Fatal("actual runner statistical archive is not readable offline")
			}
		}
	}
	summary.OfflineReopenVerified = true
	if !summary.ContinuedWhileClosed {
		summary.Failures = append(summary.Failures, "no_durable_sequence_advance_observed_while_desktop_closed")
	}
	if len(summary.Failures) != 0 {
		t.Error("live acceptance contains real failures; inspect the retained safe summary")
	}
}

func readLiveK3Input(t *testing.T) liveK3Input {
	t.Helper()
	const maximum = 64 << 10
	document, err := io.ReadAll(io.LimitReader(os.Stdin, maximum+1))
	if err != nil || len(document) > maximum {
		t.Fatal("invalid bounded live configuration")
	}
	defer clear(document)
	var input liveK3Input
	accepted := false
	defer func() {
		if !accepted {
			clear(input.Password)
			clear(input.PrivateKey)
			clear(input.Passphrase)
			clear(input.APIKey)
		}
	}()
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		t.Fatal("invalid live configuration fields")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("live configuration must contain exactly one JSON object")
	}
	if input.CaseConcurrency == 0 {
		input.CaseConcurrency = 1
	}
	if input.TimeoutMinutes == 0 {
		input.TimeoutMinutes = 90
	}
	if input.DisconnectSeconds == 0 {
		input.DisconnectSeconds = 15
	}
	validBounds := input.CaseConcurrency <= 8 && input.TimeoutMinutes >= 10 && input.TimeoutMinutes <= 90 &&
		input.DisconnectSeconds >= 5 && input.DisconnectSeconds <= 60
	validPin := input.Endpoint.Validate() == nil && input.Endpoint.Fingerprint != "" && input.Endpoint.HostKeyType != ""
	validIdentity := domain.IsUUID(input.ExpectedNodeID) && domain.IsUUID(input.ExpectedInstallation)
	password := len(input.Password) > 0 && len(input.Password) <= 8192 && len(input.PrivateKey) == 0 && len(input.Passphrase) == 0
	privateKey := len(input.PrivateKey) > 0 && len(input.PrivateKey) <= 24<<10 && len(input.Password) == 0 && len(input.Passphrase) <= 8192
	parsed, urlErr := url.Parse(input.BaseURL)
	validURL := urlErr == nil && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == ""
	if validURL {
		validURL = parsed.Scheme == "https" || parsed.Scheme == "http"
	}
	validProvider := len(input.APIKey) > 0 && len(input.APIKey) <= 8192 && strings.TrimSpace(input.Model) != "" && len(input.Model) <= 256
	if !validBounds || !validPin || !validIdentity || (!password && !privateKey) || !validURL || !validProvider {
		clear(input.Password)
		clear(input.PrivateKey)
		clear(input.Passphrase)
		clear(input.APIKey)
		t.Fatal("live configuration requires bounded settings, a pinned SSH identity, and transient credentials")
	}
	accepted = true
	return input
}

func startLiveK3App(t *testing.T, ctx context.Context, options productionOptions) *DesktopApp {
	t.Helper()
	app := newDesktopApp(newProductionInitializer(options))
	app.onStartup(ctx)
	if _, err := app.GetRemoteNodes(); err != nil {
		_ = app.shutdown()
		t.Fatal("cannot start the isolated production desktop")
	}
	return app
}

func liveK3FullSuites(t *testing.T, app *DesktopApp) []liveK3Suite {
	t.Helper()
	snapshot, err := app.GetCatalog()
	if err != nil {
		t.Fatal("cannot read the full authored catalog")
	}
	definitions := []liveK3Suite{
		{key: "kimi-k3.official-foundation", protocol: domain.ProtocolOpenAIChat, count: 84, caseKeys: map[string]string{}},
		{key: "openai-responses.kimi-k3.official-foundation", protocol: domain.ProtocolOpenAIResponses, count: 230, caseKeys: map[string]string{}},
	}
	keys := map[string]string{}
	for _, item := range snapshot.TestCases {
		keys[item.ID] = item.Key
	}
	for index := range definitions {
		definition := &definitions[index]
		for _, suite := range snapshot.Suites {
			if suite.Key == definition.key {
				definition.suite = suite
			}
		}
		suite := definition.suite
		if !domain.IsUUID(suite.ID) || suite.Protocol != definition.protocol || suite.CaseCount != definition.count || len(suite.Cases) != definition.count {
			t.Fatal("the production catalog does not contain both complete authored K3 suites")
		}
		for _, item := range suite.Cases {
			if keys[item.CaseID] == "" {
				t.Fatal("authored suite has an unresolved stable case identity")
			}
			definition.caseKeys[item.CaseID] = keys[item.CaseID]
		}
		if len(definition.caseKeys) != definition.count {
			t.Fatal("authored suite contains duplicate case identities")
		}
	}
	return definitions
}

func pairLiveK3Node(t *testing.T, ctx context.Context, app *DesktopApp, input liveK3Input) string {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal("cannot create a live node configuration identity")
	}
	authentication := remotenodes.AuthenticationPassword
	if len(input.PrivateKey) != 0 {
		authentication = remotenodes.AuthenticationPrivateKey
	}
	if app.SaveRemoteNode(RemoteNodeDraft{ID: meta.ID, Name: "live K3 acceptance", Region: "validation",
		Host: input.Endpoint.Host, Port: input.Endpoint.Port, User: input.Endpoint.User, Enabled: true,
		Authentication: authentication, Password: string(input.Password), PrivateKey: string(input.PrivateKey),
		Passphrase: string(input.Passphrase)}) != nil {
		t.Fatal("cannot save the isolated SSH node configuration")
	}
	deadline, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	confirmed := false
	for {
		snapshot, readErr := app.GetRemoteNodes()
		if readErr != nil {
			t.Fatal("cannot read physical node pairing state")
		}
		for _, node := range snapshot.Nodes {
			if node.ID != meta.ID {
				continue
			}
			if node.InstallationStage != "" {
				t.Fatal("live harness cannot install or upgrade a runner")
			}
			if !confirmed && node.ProposedHostKey != nil {
				key := *node.ProposedHostKey
				if key.Type != input.Endpoint.HostKeyType || key.Fingerprint != input.Endpoint.Fingerprint {
					t.Fatal("observed SSH host key differs from the supplied exact pin")
				}
				if app.ConfirmRemoteNodeHostKey(RemoteNodeHostKeyConfirmation{
					ID: node.ID, ExpectedRevision: node.Revision, Key: key}) != nil {
					t.Fatal("cannot confirm the supplied SSH host key pin")
				}
				confirmed = true
			}
			if !node.Eligible {
				continue
			}
			if node.HostKey == nil || node.HostKey.Type != input.Endpoint.HostKeyType || node.HostKey.Fingerprint != input.Endpoint.Fingerprint {
				t.Fatal("eligible runner did not retain the supplied SSH pin")
			}
			assertLiveK3Runner(t, node.Runner, input)
			if app.SelectDefaultExecution(DefaultExecutionCommand{ExpectedRevision: snapshot.DefaultRevision,
				Location: jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: node.ID}}) != nil {
				t.Fatal("cannot select the eligible physical runner")
			}
			return node.ID
		}
		liveK3Pause(t, deadline, time.Second)
	}
}

func liveK3RemoteDefault(t *testing.T, app *DesktopApp, configurationID string, input liveK3Input) jobs.Location {
	t.Helper()
	snapshot, err := app.GetRemoteNodes()
	if err != nil || snapshot.Default.Kind != jobcontract.TargetRemote || snapshot.Default.ConfigurationID != configurationID {
		t.Fatal("saved execution default is not the selected physical node")
	}
	for _, node := range snapshot.Nodes {
		if node.ID == configurationID {
			assertLiveK3Runner(t, node.Runner, input)
			return snapshot.Default
		}
	}
	t.Fatal("saved physical execution default has no configuration")
	return jobs.Location{}
}

func assertLiveK3Runner(t *testing.T, runner *RemoteRunnerSummary, input liveK3Input) {
	t.Helper()
	if runner == nil || runner.NodeID != input.ExpectedNodeID || runner.InstallationID != input.ExpectedInstallation {
		t.Fatal("physical runner node or installation differs from the expected identity")
	}
	if (input.ExpectedVersion != "" && runner.Version != input.ExpectedVersion) ||
		(input.ExpectedBuild != "" && runner.BuildID != input.ExpectedBuild) {
		t.Fatal("physical runner release differs from the expected version/build")
	}
}

func assertLiveK3Executor(t *testing.T, view jobs.JobView, input liveK3Input) {
	t.Helper()
	executor := view.Executor
	if !domain.IsUUID(view.JobID) || view.Location.Kind != jobcontract.TargetRemote ||
		executor.NodeID != input.ExpectedNodeID || executor.InstallationID != input.ExpectedInstallation {
		t.Fatal("job delivery changed its physical executor identity")
	}
	if executor.Version == "" || executor.BuildID == "" || executor.OS != "linux" || executor.Arch == "" {
		t.Fatal("job lacks actual Linux runner release metadata")
	}
	if (input.ExpectedVersion != "" && executor.Version != input.ExpectedVersion) ||
		(input.ExpectedBuild != "" && executor.BuildID != input.ExpectedBuild) {
		t.Fatal("job execution release differs from the expected version/build")
	}
}

func waitLiveK3Job(t *testing.T, ctx context.Context, app *DesktopApp, id string, accepted bool) jobs.JobView {
	t.Helper()
	last := ""
	var lastCompleted, lastTotal uint64
	for {
		view, err := app.GetJob(id)
		if err == nil {
			status := string(view.Delivery)
			if view.Execution != nil {
				status += "/" + string(view.Execution.Status)
			}
			completed, total := uint64(0), uint64(0)
			if workspace, readErr := app.GetWorkspace(); readErr == nil {
				for _, run := range workspace.Runs {
					if len(view.RunIDs) == 1 && run.ID == view.RunIDs[0] {
						completed, total = run.ObservedCaseCount, run.CaseCount
					}
				}
			}
			if status != last || completed != lastCompleted || total != lastTotal {
				t.Logf("job=%s status=%s cases=%d/%d", id, status, completed, total)
				last, lastCompleted, lastTotal = status, completed, total
			}
			if view.Execution != nil {
				terminal := jobcontract.Terminal(view.Execution.Status)
				running := view.Execution.Status == domain.RunRunning || view.Execution.Status == domain.RunDraining
				if terminal || (accepted && view.Confirmation != nil && running) {
					return view
				}
			}
		}
		liveK3Pause(t, ctx, 2*time.Second)
	}
}

func assertLiveK3Outbox(t *testing.T, app *DesktopApp, submitted []liveK3JobSummary) {
	t.Helper()
	page, err := app.GetJobs("", 25)
	if err != nil || page.HasMore || len(page.Jobs) != len(submitted) {
		t.Fatal("submission outbox has unexpected paid job identities")
	}
	ids := map[string]bool{}
	for _, item := range page.Jobs {
		ids[item.JobID] = true
	}
	for _, item := range submitted {
		if !ids[item.JobID] {
			t.Fatal("a stable paid job identity is missing from the local outbox")
		}
		controls, readErr := app.GetJobControls(item.JobID, "", 25)
		if readErr != nil || controls.HasMore || len(controls.Commands) != 0 {
			t.Fatal("live validation unexpectedly issued a task control command")
		}
	}
}

func waitLiveK3Report(t *testing.T, ctx context.Context, app *DesktopApp, view jobs.JobView) reporting.Detail {
	t.Helper()
	if len(view.RunIDs) != 1 {
		t.Fatal("authored suite did not preserve exactly one run identity")
	}
	for {
		page, err := app.GetReports()
		if err == nil {
			for _, item := range page.Reports {
				if item.RunID != view.RunIDs[0] {
					continue
				}
				detail, detailErr := app.GetReportDetail(item.ID)
				if detailErr == nil && detail.Source == reporting.SourceRun && detail.Report.Validate() == nil {
					return detail
				}
			}
		}
		liveK3Pause(t, ctx, 2*time.Second)
	}
}

func collectLiveK3Suite(item *liveK3JobSummary, detail reporting.Detail, suite liveK3Suite, executor jobs.ExecutorView) {
	report := detail.Report
	item.RunID, item.ReportID = report.RunID, report.ID
	item.ActualCases = len(report.CaseResults)
	item.CaseIdentitiesMatch = true
	item.Passed, item.Failed = report.Verification.Passed, report.Verification.Failed
	item.Observed, item.Indeterminate = report.Verification.Observed, report.Verification.Indeterminate
	item.RunnerReportOrigin = liveK3OriginMatches(report.ExecutionOrigin, executor)
	seen := map[string]bool{}
	for _, result := range report.CaseResults {
		key := suite.caseKeys[result.CaseID]
		if key == "" || seen[result.CaseID] {
			item.CaseIdentitiesMatch = false
			continue
		}
		seen[result.CaseID] = true
		classification := ""
		switch result.Verification.Status {
		case testspec.VerdictFailed:
			classification = "assertion_failed"
		case testspec.VerdictIndeterminate:
			classification = "indeterminate"
		}
		if result.Verification.Status != testspec.VerdictPassed && result.Failure.Validate() == nil {
			classification = string(result.Failure)
		}
		if classification != "" {
			item.FailureCases = append(item.FailureCases, liveK3CaseFailure{
				CaseID: result.CaseID, Key: key, Classification: classification})
		}
	}
	if len(seen) != suite.count {
		item.CaseIdentitiesMatch = false
	}
	sort.Slice(item.FailureCases, func(a, b int) bool { return item.FailureCases[a].CaseID < item.FailureCases[b].CaseID })
	usage := liveK3Usage{Boundary: "reported_request_observation_usage"}
	for _, result := range detail.RequestResults {
		if result.Observation == nil {
			continue
		}
		var raw struct {
			Prompt        *uint64 `json:"prompt_tokens"`
			Completion    *uint64 `json:"completion_tokens"`
			Input         *uint64 `json:"input_tokens"`
			Output        *uint64 `json:"output_tokens"`
			PromptDetails struct {
				Cached uint64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
			InputDetails struct {
				Cached uint64 `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		}
		if json.Unmarshal(result.Observation.Usage, &raw) != nil {
			continue
		}
		input, output := raw.Prompt, raw.Completion
		cached := raw.PromptDetails.Cached
		if report.Protocol == domain.ProtocolOpenAIResponses {
			input, output, cached = raw.Input, raw.Output, raw.InputDetails.Cached
		}
		if input == nil || output == nil {
			continue
		}
		usage.ObservedRequests++
		usage.InputTokens += *input
		usage.OutputTokens += *output
		usage.CachedTokens += cached
	}
	if usage.ObservedRequests > 0 {
		item.Usage = &usage
	}
}

func waitLiveK3Performance(t *testing.T, ctx context.Context, app *DesktopApp, id string) jobs.PerformanceResultView {
	t.Helper()
	for {
		view, err := app.GetJob(id)
		if err == nil && view.PerformanceResultReady {
			result, resultErr := app.GetJobPerformanceResult(id)
			matchingOwner := result.JobID == id && result.Executor == view.Executor && result.Location == view.Location
			matchingSeal := result.SealedAt != "" && view.Execution != nil && result.Terminal.Revision == view.Execution.Revision
			if resultErr == nil && matchingOwner && matchingSeal {
				return result
			}
		}
		liveK3Pause(t, ctx, 2*time.Second)
	}
}

func waitLiveK3Archive(t *testing.T, ctx context.Context, app *DesktopApp, reportID string) quicktest.PerformanceReport {
	t.Helper()
	for {
		detail, err := app.GetReportDetail(reportID)
		if err == nil && detail.Source == reporting.SourceQuickPerformance && detail.Performance != nil {
			return *detail.Performance
		}
		liveK3Pause(t, ctx, 2*time.Second)
	}
}

// The public result Executor projects the validated Terminal Receipt origin.
// A checkpoint may omit its origin; an origin it does carry must agree.
func liveK3PerformanceOriginMatches(result jobs.PerformanceResultView, view jobs.JobView) bool {
	if view.Execution == nil || result.JobID != view.JobID || result.Location != view.Location || result.Executor != view.Executor {
		return false
	}
	terminal := result.Terminal
	matchingTerminal := terminal.Revision == view.Execution.Revision && terminal.Status == view.Execution.Status &&
		terminal.AcceptedAt == view.Execution.AcceptedAt && terminal.LatestSequence <= view.Execution.LatestSequence
	if !matchingTerminal || !jobcontract.Terminal(terminal.Status) || result.SealedAt == "" {
		return false
	}
	if result.Observation != nil {
		origin := result.Observation.Report.ExecutionOrigin
		if origin != nil && !liveK3OriginMatches(origin, result.Executor) {
			return false
		}
	}
	return true
}

func liveK3ArchiveOriginMatches(archive quicktest.PerformanceReport, result jobs.PerformanceResultView) bool {
	if _, err := quicktest.ValidateArchivedPerformanceReport(archive); err != nil {
		return false
	}
	return archive.ReportID == result.ReportID && archive.GeneratedAt == result.SealedAt &&
		archive.Archived && archive.ArchiveStatus == quicktest.PerformanceArchiveArchived &&
		liveK3OriginMatches(archive.ExecutionOrigin, result.Executor)
}

func summarizeLiveK3Performance(result jobs.PerformanceResultView) *liveK3Performance {
	value := &liveK3Performance{ReportID: result.ReportID, SealedAt: result.SealedAt,
		ObservationState: string(result.ObservationState)}
	if result.Observation != nil {
		report := result.Observation.Report
		count, profile, progress, metrics := len(report.Samples), report.Profile, report.Progress, report.Metrics
		value.SampleCount, value.Profile, value.Progress, value.Metrics = &count, &profile, &progress, &metrics
	}
	return value
}

func liveK3OriginMatches(origin *domain.ExecutionOrigin, executor jobs.ExecutorView) bool {
	if origin == nil || origin.Validate() != nil || origin.Kind != "remote" {
		return false
	}
	return origin.NodeID == executor.NodeID && origin.InstallationID == executor.InstallationID &&
		origin.RunnerVersion == executor.Version && origin.EngineBuildID == executor.BuildID &&
		origin.OS == executor.OS && origin.Arch == executor.Arch
}

func liveK3MinimumPerformance(result jobs.PerformanceResultView) bool {
	if !result.ExecutionStarted || result.ObservationState != jobcontract.PerformanceAvailable || result.Observation == nil {
		return false
	}
	report := result.Observation.Report
	profile := report.Profile
	minimal := profile.RequestCount == 3 && profile.Concurrency == 1 && profile.TimeoutMS == 120_000 &&
		profile.InputTokens == 32 && profile.OutputTokens == 128
	extra := profile.WarmupRequests != 0 || profile.RampDurationMS != 0 || profile.CapacityEnabled ||
		report.Warmup != nil || report.Ramp != nil || report.CapacityResult != nil
	complete := report.Progress.Planned == 3 && report.Progress.Completed == 3 &&
		report.Progress.Launched == 3 && len(report.Samples) == 3
	return minimal && !extra && complete && report.Success
}

func liveK3Pause(t *testing.T, ctx context.Context, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		t.Fatal("live acceptance deadline reached; accepted jobs were neither cancelled nor resubmitted")
	case <-timer.C:
	}
}

func writeLiveK3Summary(t *testing.T, root string, summary liveK3Summary) {
	t.Helper()
	document, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Error("cannot encode the safe acceptance summary")
		return
	}
	file, err := os.OpenFile(filepath.Join(root, "acceptance-summary.json"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		t.Error("cannot create the safe acceptance summary")
		return
	}
	_, writeErr := file.Write(document)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		t.Error("cannot persist the safe acceptance summary")
	}
}
