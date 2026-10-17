package live

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	bootstrap "github.com/894x/llm-test-studio/apps/desktop/internal/bootstrap"
	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

const sharedRunnerLiveEnable = "LLM_TEST_LIVE_REMOTE_SHARED"

const (
	sharedRunnerExecutionWindow  = 170 * time.Second
	sharedRunnerSettlementWindow = 3 * time.Minute
	sharedRunnerMirrorWindow     = 3 * time.Minute
	sharedRunnerRoleWindow       = 9 * time.Minute
)

// One bounded document arrives on an anonymous stdin pipe. There is no real
// provider credential field: each role creates its own synthetic mock key.
// The coordinator owns an empty workspace root and deliberately retains it.
type sharedRunnerLiveInput struct {
	Role                 string             `json:"role"`
	Endpoint             remotessh.Endpoint `json:"endpoint"`
	Password             []byte             `json:"password"`
	BaseURL              string             `json:"base_url"`
	WorkspaceRoot        string             `json:"workspace_root"`
	ExpectedNodeID       string             `json:"expected_node_id"`
	ExpectedInstallation string             `json:"expected_installation_id"`
	ExpectedVersion      string             `json:"expected_version"`
	ExpectedBuild        string             `json:"expected_build"`
	PeerJobID            string             `json:"peer_job_id"`
}

func (sharedRunnerLiveInput) String() string { return "sharedRunnerLiveInput{redacted}" }

func (sharedRunnerLiveInput) GoString() string { return "sharedRunnerLiveInput{redacted}" }

func (sharedRunnerLiveInput) MarshalJSON() ([]byte, error) {
	return nil, errors.New("live credentials cannot be serialized")
}

type sharedRunnerLiveResult struct {
	started              time.Time
	Role                 string                   `json:"role"`
	ClientVersion        string                   `json:"client_version"`
	Executor             jobs.ExecutorView        `json:"executor"`
	Budget               jobcontract.RunnerBudget `json:"budget"`
	Active               uint32                   `json:"active"`
	Queued               uint32                   `json:"queued"`
	Pending              uint32                   `json:"pending"`
	Healthy              bool                     `json:"healthy"`
	PeerVisible          bool                     `json:"peer_visible"`
	LaunchCalls          uint32                   `json:"launch_calls"`
	ReportID             string                   `json:"report_id,omitempty"`
	ActualCases          int                      `json:"actual_cases"`
	CaseIdentitiesMatch  bool                     `json:"case_identities_match"`
	OriginVerified       bool                     `json:"origin_verified"`
	Sealed               bool                     `json:"sealed"`
	TerminalToSettledMS  int64                    `json:"terminal_to_settled_ms"`
	TerminalObservedMS   int64                    `json:"terminal_observed_ms"`
	SettlementWindowMS   int64                    `json:"settlement_window_ms"`
	MirrorAfterSettledMS int64                    `json:"mirror_after_settled_ms"`
	RoleElapsedMS        int64                    `json:"role_elapsed_ms"`
	FailurePhase         string                   `json:"failure_phase,omitempty"`
	FailureReason        string                   `json:"failure_reason,omitempty"`
}

type sharedLiveFailureKey struct{}

type sharedLiveFailure struct {
	phase    string
	reason   string
	deadline time.Time
}

func (failure *sharedLiveFailure) enter(phase string, deadline time.Time) {
	if failure.reason == "" {
		switch phase {
		case "input", "startup", "pairing", "baseline", "preparation", "launch", "acceptance", "execution", "settlement",
			"mirror", "report", "final_probe", "configuration", "credentials", "original_job", "remote_observation", "shutdown":
		default:
			phase = "verification"
		}
		failure.phase, failure.deadline = phase, deadline
	}
}

func (failure *sharedLiveFailure) first(reason string) {
	if failure.reason != "" {
		return
	}
	switch reason {
	case "input_invalid", "workspace_invalid", "baseline_read_failed", "baseline_not_idle", "launch_unconfirmed",
		"job_read_unavailable", "job_identity_changed", "runner_probe_unavailable", "runner_probe_invalid",
		"runner_unhealthy", "runner_origin_changed", "execution_not_completed", "queue_not_observed",
		"settlement_window_expired", "report_invalid", "phase_deadline", "phase_failed":
		failure.reason = reason
	default:
		failure.reason = "phase_failed"
	}
}

func failSharedLive(t *testing.T, ctx context.Context, reason string) {
	t.Helper()
	if failure, ok := ctx.Value(sharedLiveFailureKey{}).(*sharedLiveFailure); ok {
		failure.first(reason)
	}
	t.Fatal("allow-listed live verification failed")
}

func TestSharedRunnerFailureEvidenceIsFirstAndAllowListed(t *testing.T) {
	failure := &sharedLiveFailure{}
	failure.enter("execution", time.Now().Add(time.Minute))
	failure.first("job_read_unavailable")
	failure.enter("settlement", time.Time{})
	failure.first("runner_unhealthy")
	if failure.phase != "execution" || failure.reason != "job_read_unavailable" {
		t.Fatal("a later cleanup failure replaced the first verification boundary")
	}
	private := &sharedLiveFailure{}
	private.enter("synthetic-secret", time.Time{})
	private.first("synthetic-secret")
	result := sharedRunnerLiveResult{FailurePhase: private.phase, FailureReason: private.reason}
	document, err := json.Marshal(result)
	if err != nil || bytes.Contains(document, []byte("synthetic-secret")) || private.phase != "verification" {
		t.Fatal("unlisted diagnostic text escaped the fixed safe evidence boundary")
	}
}

type sharedRunnerLiveEvent struct {
	Stage  string                  `json:"stage"`
	JobID  string                  `json:"job_id"`
	State  string                  `json:"state"`
	Result *sharedRunnerLiveResult `json:"result,omitempty"`
}

// This subprocess protocol deliberately exits after deferred shutdown. That
// prevents testing's PASS/FAIL prose from mixing with its allow-listed JSONL.
// Ordinary go test runs only skip this explicit, physically connected role.
func TestLiveRemoteSharedRunnerRole(t *testing.T) {
	if os.Getenv(sharedRunnerLiveEnable) != "1" {
		t.Skip("explicit zero-provider-cost physical runner role; bounded stdin only")
	}
	failure := &sharedLiveFailure{phase: "input"}
	result := sharedRunnerLiveResult{started: time.Now()}
	final := sharedRunnerLiveEvent{Stage: "failed", State: "phase_failed", Result: &result}
	defer func() {
		if t.Failed() || final.Stage != "completed" {
			if failure.reason == "" && !failure.deadline.IsZero() && !time.Now().Before(failure.deadline) {
				failure.first("phase_deadline")
			}
			failure.first("phase_failed")
			result.FailurePhase, result.FailureReason = failure.phase, failure.reason
			final.State = failure.reason
		}
		emitSharedRunnerLive(final)
		code := 0
		if t.Failed() || final.Stage != "completed" {
			code = 1
		}
		os.Exit(code)
	}()
	input, err := decodeSharedRunnerLiveInput(os.Stdin)
	if err != nil {
		failure.first("input_invalid")
		t.Fatal("invalid bounded shared runner role configuration")
	}
	defer clear(input.Password)
	base := context.WithValue(t.Context(), sharedLiveFailureKey{}, failure)
	ctx, cancel := context.WithTimeout(base, sharedRunnerRoleWindow)
	defer cancel()
	executionCtx, cancelExecution := context.WithTimeout(ctx, sharedRunnerExecutionWindow)
	defer cancelExecution()
	result.Role, result.ClientVersion = input.Role, sharedRunnerClientVersion(input.Role)
	result.SettlementWindowMS = sharedRunnerSettlementWindow.Milliseconds()
	deadline, _ := executionCtx.Deadline()
	failure.enter("startup", deadline)
	app, configurationRoot, store := startSharedRunnerLiveApp(
		t,
		ctx,
		input,
		result.ClientVersion,
	)
	defer func() { _ = bridge.Shutdown(app) }()
	pairingInput := liveK3Input{Endpoint: input.Endpoint, Password: input.Password,
		ExpectedNodeID: input.ExpectedNodeID, ExpectedInstallation: input.ExpectedInstallation,
		ExpectedVersion: input.ExpectedVersion, ExpectedBuild: input.ExpectedBuild}
	failure.enter("pairing", deadline)
	configurationID := pairLiveK3Node(t, executionCtx, app, pairingInput)
	location := liveK3RemoteDefault(t, app, configurationID, pairingInput)
	failure.enter("baseline", deadline)
	audit := openSharedRunnerBaseline(t, ctx, sharedRunnerBaselineSettings{
		app: app, input: input, configurationRoot: configurationRoot,
		configurationID: configurationID, store: store,
	})
	defer audit.Close()
	failure.enter("preparation", deadline)
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal("cannot create a synthetic mock credential")
	}
	defer clear(secret)
	apiKey := []byte("r15-mock-" + base64.RawURLEncoding.EncodeToString(secret))
	defer clear(apiKey)
	command := sharedRunnerPerformanceCommand(location, input.BaseURL, apiKey)
	var suite liveK3Suite
	if input.Role == "settlement" {
		suite = liveK3FullSuites(t, app)[1]
		command.Kind, command.Performance = jobs.LaunchQuickTask, nil
		command.QuickTask = &runs.QuickTaskCommand{SuiteID: suite.suite.ID, Model: "r15-mock",
			BaseURL: input.BaseURL, APIKey: string(apiKey), RequestTimeoutSeconds: 120,
			CaseConcurrency: 4, Inputs: map[string]json.RawMessage{}}
	}
	if executionCtx.Err() != nil {
		t.Fatal("the bounded preparation window expired before the single launch")
	}
	failure.enter("launch", deadline)
	view, launchErr := app.LaunchJob(command) // One call only. Pending never starts a replacement.
	result.LaunchCalls = 1
	if launchErr != nil {
		ownJobs, readErr := readLiveK3OwnJobs(app.GetJobs)
		if readErr == nil && len(ownJobs) == 1 {
			final.JobID = ownJobs[0].JobID
		}
		final.State = "launch_unconfirmed_no_resubmission"
		failSharedLive(t, ctx, "launch_unconfirmed")
	}
	final.JobID = view.JobID
	assertLiveK3Executor(t, view, pairingInput)
	if view.Executor.Version == result.ClientVersion {
		t.Fatal("actual runner metadata was replaced by the desktop client label")
	}
	result.Executor = view.Executor
	failure.enter("acceptance", deadline)
	view = waitSharedRunnerAcceptance(t, executionCtx, app, view.JobID)
	assertLiveK3Executor(t, view, pairingInput)
	emitSharedRunnerLive(sharedRunnerLiveEvent{Stage: "accepted", JobID: view.JobID,
		State: string(view.Submission.Delivery), Result: &result})
	failure.enter("execution", deadline)
	terminalAt := observeSharedRunnerTerminal(
		t,
		executionCtx,
		audit,
		input,
		view,
		&result,
	)
	result.TerminalObservedMS = time.Since(result.started).Milliseconds()
	cancelExecution()
	failure.enter("settlement", terminalAt.Add(sharedRunnerSettlementWindow))
	waitSharedRunnerSettled(
		t,
		ctx,
		audit,
		terminalAt,
		&result,
	)
	emitSharedRunnerLive(sharedRunnerLiveEvent{Stage: "settled", JobID: view.JobID, State: "idle", Result: &result})
	mirrorStarted := time.Now()
	mirrorCtx, cancelMirror := context.WithTimeout(ctx, sharedRunnerMirrorWindow)
	defer cancelMirror()
	mirrorDeadline, _ := mirrorCtx.Deadline()
	failure.enter("mirror", mirrorDeadline)
	view = waitSharedRunnerMirror(t, mirrorCtx, app, view.JobID)
	assertLiveK3Executor(t, view, pairingInput)
	assertLiveK3Outbox(t, app, []liveK3JobSummary{{JobID: view.JobID}})
	failure.enter("report", mirrorDeadline)
	if input.Role == "settlement" {
		detail := waitLiveK3Report(t, mirrorCtx, app, view)
		item := liveK3JobSummary{FailureCases: []liveK3CaseFailure{}}
		collectLiveK3Suite(&item, detail, suite, view.Executor)
		result.ActualCases, result.CaseIdentitiesMatch = item.ActualCases, item.CaseIdentitiesMatch
		result.ReportID, result.OriginVerified = item.ReportID, item.RunnerReportOrigin
		if result.ActualCases != 230 || !result.CaseIdentitiesMatch || !result.OriginVerified {
			failSharedLive(t, ctx, "report_invalid")
		}
		// Mock 200 responses do not prove the authored negative assertions pass.
	} else {
		outcome := waitLiveK3Performance(t, mirrorCtx, app, view.JobID)
		archive := waitLiveK3Archive(t, mirrorCtx, app, outcome.ReportID)
		result.ReportID, result.Sealed = outcome.ReportID, outcome.SealedAt != ""
		result.OriginVerified = liveK3PerformanceOriginMatches(outcome, view) && liveK3ArchiveOriginMatches(archive, outcome)
		if !sharedRunnerMinimumPerformance(outcome) || !result.OriginVerified {
			failSharedLive(t, ctx, "report_invalid")
		}
	}
	result.MirrorAfterSettledMS = time.Since(mirrorStarted).Milliseconds()
	failure.enter("final_probe", time.Now().Add(5*time.Second))
	finalProbeCtx, cancelFinalProbe := context.WithTimeout(ctx, 5*time.Second)
	probe, err := audit.Probe(finalProbeCtx)
	cancelFinalProbe()
	if err != nil {
		failSharedLive(t, ctx, "runner_probe_unavailable")
	}
	applySharedRunnerProbe(t, ctx, probe, &result)
	if probe.Active != 0 || probe.Queued != 0 || probe.Pending != 0 {
		t.Fatal("the actual runner is no longer fully settled at final verification")
	}
	final = sharedRunnerLiveEvent{Stage: "completed", JobID: view.JobID,
		State: string(view.Execution.Status), Result: &result}
}

func decodeSharedRunnerLiveInput(reader io.Reader) (sharedRunnerLiveInput, error) {
	const maximum = 64 << 10
	document, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || len(document) > maximum {
		clear(document)
		return sharedRunnerLiveInput{}, errors.New("invalid bounded role input")
	}
	defer clear(document)
	var input sharedRunnerLiveInput
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoded := decoder.Decode(&input) == nil && decoder.Decode(&struct{}{}) == io.EOF
	validRole := input.Role == "a" || input.Role == "b" || input.Role == "settlement"
	validPeer := input.PeerJobID == "" || domain.IsUUID(input.PeerJobID)
	if input.Role == "b" {
		validPeer = domain.IsUUID(input.PeerJobID)
	}
	validPin := input.Endpoint.Validate() == nil && input.Endpoint.Fingerprint != "" && input.Endpoint.HostKeyType != ""
	validIdentity := domain.IsUUID(input.ExpectedNodeID) && domain.IsUUID(input.ExpectedInstallation)
	validRelease := strings.TrimSpace(input.ExpectedVersion) == input.ExpectedVersion && input.ExpectedVersion != "" &&
		len(input.ExpectedVersion) <= 128 && jobcontract.IsSHA256(input.ExpectedBuild)
	validSecret := len(input.Password) > 0 && len(input.Password) <= 8192
	filesystemRoot := filepath.VolumeName(input.WorkspaceRoot) + string(os.PathSeparator)
	validRoot := filepath.IsAbs(input.WorkspaceRoot) && filepath.Clean(input.WorkspaceRoot) != filesystemRoot
	validAuthority := validPin && validIdentity && validRelease
	validEnvelope := decoded && validRole && validPeer && validSecret && validRoot
	validInput := validEnvelope && validAuthority && sharedRunnerMockURL(input.Role, input.BaseURL)
	if !validInput {
		clear(input.Password)
		return sharedRunnerLiveInput{}, errors.New("invalid pinned loopback-only role input")
	}
	return input, nil
}

var sharedRunnerMockPath = regexp.MustCompile(`^/(r15|settle)/[a-f0-9]{32}/v1$`)

func sharedRunnerMockURL(role, value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	port, err := strconv.ParseUint(parsed.Port(), 10, 16)
	loopback := net.ParseIP(parsed.Hostname())
	if err != nil || port == 0 || loopback == nil || parsed.Hostname() != "127.0.0.1" || !loopback.IsLoopback() {
		return false
	}
	if parsed.RawPath != "" || !sharedRunnerMockPath.MatchString(parsed.Path) {
		return false
	}
	want := "/r15/"
	if role == "settlement" {
		want = "/settle/"
	}
	return strings.HasPrefix(parsed.Path, want)
}

func sharedRunnerClientVersion(role string) string {
	if role == "settlement" {
		return "r15-client-settlement"
	}
	return "r15-client-" + role
}

func startSharedRunnerLiveApp(
	t *testing.T,
	ctx context.Context,
	input sharedRunnerLiveInput,
	version string,
) (*bridge.DesktopApp, string, credentials.Store) {
	t.Helper()
	entries, err := os.ReadDir(input.WorkspaceRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("each live role requires its own new empty retained workspace")
	}
	configurationRoot := filepath.Join(input.WorkspaceRoot, "configuration")
	catalogRoot := filepath.Join(input.WorkspaceRoot, "catalog")
	artifactRoot := filepath.Join(input.WorkspaceRoot, "no-runner-artifacts")
	for _, directory := range []string{configurationRoot, catalogRoot, artifactRoot} {
		if os.Mkdir(directory, 0o700) != nil {
			t.Fatal("cannot prepare the isolated retained workspace")
		}
	}
	source, err := runnerartifacts.NewDirectorySource(artifactRoot)
	if err != nil {
		t.Fatal("cannot disable runner installation and upgrade")
	}
	store := credentials.NewMemoryStore()
	options := bootstrap.Options{UserConfigDir: func() (string, error) { return configurationRoot, nil },
		ExecutablePath: func() (string, error) { return filepath.Join(catalogRoot, "shared-live.exe"), nil },
		AppVersion:     version, CredentialStore: store, RunnerSource: source}
	return startLiveK3App(t, ctx, options), configurationRoot, store
}

type sharedRunnerBaselineSettings struct {
	app               *bridge.DesktopApp
	input             sharedRunnerLiveInput
	configurationRoot string
	configurationID   string
	store             credentials.Store
}

func openSharedRunnerBaseline(
	t *testing.T,
	ctx context.Context,
	settings sharedRunnerBaselineSettings,
) bootstrap.RunnerConnection {
	t.Helper()
	snapshot, err := settings.app.GetRemoteNodes()
	if err != nil {
		t.Fatal("cannot read the paired physical executor")
	}
	var expected jobs.JobView
	for _, node := range snapshot.Nodes {
		if node.ID == settings.configurationID && node.Runner != nil {
			runner := node.Runner
			expected.Executor = jobs.ExecutorView{NodeID: runner.NodeID, InstallationID: runner.InstallationID,
				Version: runner.Version, BuildID: runner.BuildID, OS: runner.OS, Arch: runner.Arch}
		}
	}
	audit, err := openLiveK3Audit(ctx, liveK3AuditSettings{ConfigurationRoot: settings.configurationRoot,
		Credentials: settings.store, ConfigurationID: settings.configurationID, Expected: expected})
	if err != nil {
		t.Fatal("cannot open the read-only physical runner observation connection")
	}
	probe, err := audit.Probe(ctx)
	if err != nil {
		_ = audit.Close()
		t.Fatal("cannot verify the isolated runner baseline before submitting")
	}
	result := sharedRunnerLiveResult{Executor: expected.Executor}
	applySharedRunnerProbe(t, ctx, probe, &result)
	baseline := result.Healthy && probe.Active == 0 && probe.Queued == 0 && probe.Pending == 0
	if settings.input.Role == "b" {
		peer, readErr := audit.GetJob(ctx, settings.input.PeerJobID)
		baseline = result.Healthy && probe.Active == 1 && probe.Queued == 0 && probe.Pending == 1 &&
			readErr == nil && peer.Receipt.Status == domain.RunRunning &&
			liveK3OriginMatches(&peer.Receipt.Origin, expected.Executor)
	}
	if !baseline {
		_ = audit.Close()
		t.Fatal("runner baseline contains unexpected work; no job was submitted")
	}
	return audit
}

func sharedRunnerPerformanceCommand(location jobs.Location, baseURL string, apiKey []byte) jobs.LaunchCommand {
	return jobs.LaunchCommand{Location: location, Kind: jobs.LaunchPerformance,
		Performance: &quicktest.PerformanceCommand{Protocol: domain.ProtocolOpenAIChat,
			AddressMode: quicktest.AddressModeBaseURL, URL: baseURL, APIKey: string(apiKey), ModelID: "r15-mock",
			LoadMode: domain.LoadFixedConcurrency, WorkloadMode: quicktest.PerformanceWorkloadFixed,
			RequestCount: 1, Concurrency: 1, TimeoutMS: 120_000, InputTokens: 32, OutputTokens: 8}}
}

func waitSharedRunnerAcceptance(t *testing.T, ctx context.Context, app *bridge.DesktopApp, id string) jobs.JobView {
	t.Helper()
	for {
		view, err := app.GetJob(id)
		if err == nil {
			if view.Submission == nil {
				t.Fatal("the original launched job lost its own durable submission")
			}
			if view.Submission.Delivery == jobs.SubmissionAccepted && view.Submission.Confirmation != nil {
				return view
			}
		}
		liveK3Pause(t, ctx, 250*time.Millisecond)
	}
}

func observeSharedRunnerTerminal(
	t *testing.T,
	ctx context.Context,
	audit bootstrap.RunnerConnection,
	input sharedRunnerLiveInput,
	view jobs.JobView,
	result *sharedRunnerLiveResult,
) time.Time {
	t.Helper()
	queueVerified := false
	last := ""
	for {
		snapshot, err := audit.GetJob(ctx, view.JobID)
		if err != nil {
			failSharedLive(t, ctx, "job_read_unavailable")
		}
		if !liveK3AuditIdentity(view, snapshot) {
			failSharedLive(t, ctx, "job_identity_changed")
		}
		probe, err := audit.Probe(ctx)
		if err != nil {
			failSharedLive(t, ctx, "runner_probe_unavailable")
		}
		applySharedRunnerProbe(t, ctx, probe, result)
		state := string(snapshot.Receipt.Status)
		if state != last {
			emitSharedRunnerLive(sharedRunnerLiveEvent{Stage: "observed", JobID: view.JobID, State: state, Result: result})
			last = state
		}
		if input.Role == "b" && !queueVerified {
			peer, err := audit.GetJob(ctx, input.PeerJobID)
			result.PeerVisible = err == nil && peer.Receipt.JobID == input.PeerJobID &&
				liveK3OriginMatches(&peer.Receipt.Origin, view.Executor)
			sharedQueue := probe.Active == 1 && probe.Queued == 1 && result.PeerVisible &&
				snapshot.Receipt.Status == domain.RunQueued && peer.Receipt.Status == domain.RunRunning
			if sharedQueue {
				emitSharedRunnerLive(sharedRunnerLiveEvent{Stage: "queue_verified", JobID: view.JobID,
					State: state, Result: result})
				queueVerified = true
			}
		}
		if jobcontract.Terminal(snapshot.Receipt.Status) {
			if snapshot.Receipt.Status != domain.RunCompleted {
				failSharedLive(t, ctx, "execution_not_completed")
			}
			if input.Role == "b" && !queueVerified {
				failSharedLive(t, ctx, "queue_not_observed")
			}
			return time.Now()
		}
		liveK3Pause(t, ctx, 250*time.Millisecond)
	}
}

func applySharedRunnerProbe(
	t *testing.T,
	ctx context.Context,
	probe jobcontract.RunnerInfo,
	result *sharedRunnerLiveResult,
) {
	t.Helper()
	if err := sharedRunnerProbeError(probe, result.Executor); err != nil {
		reason := "runner_probe_invalid"
		if errors.Is(err, jobs.ErrTargetChanged) {
			reason = "runner_origin_changed"
		} else if errors.Is(err, jobs.ErrUnavailable) {
			reason = "runner_unhealthy"
		}
		failSharedLive(t, ctx, reason)
	}
	result.Budget, result.Active, result.Queued, result.Pending = probe.Budget, probe.Active, probe.Queued, probe.Pending
	result.Healthy = probe.Ready && probe.Healthy
}

func sharedRunnerProbeError(probe jobcontract.RunnerInfo, executor jobs.ExecutorView) error {
	budget := probe.Budget
	validBudget := budget.MaxActiveJobs == 1 && budget.MaxQueuedJobs == 16 && budget.MaxConcurrency == 2000
	if probe.Validate() != nil || !validBudget {
		return jobs.ErrInvalid
	}
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: probe.NodeID, InstallationID: probe.NodeInstallationID,
		EngineBuildID: probe.EngineBuildID, RunnerVersion: probe.RunnerVersion, OS: probe.OS, Arch: probe.Arch}
	if !liveK3OriginMatches(&origin, executor) {
		return jobs.ErrTargetChanged
	}
	if !probe.Ready || !probe.Healthy {
		return jobs.ErrUnavailable
	}
	return nil
}

func sharedRunnerSettlementState(
	probe jobcontract.RunnerInfo,
	executor jobs.ExecutorView,
	elapsed time.Duration,
) (bool, error) {
	if err := sharedRunnerProbeError(probe, executor); err != nil {
		return false, err
	}
	if elapsed < 0 || elapsed >= sharedRunnerSettlementWindow {
		return false, context.DeadlineExceeded
	}
	return probe.Active == 0 && probe.Queued == 0 && probe.Pending == 0, nil
}

func waitSharedRunnerSettled(
	t *testing.T,
	ctx context.Context,
	audit bootstrap.RunnerConnection,
	terminalAt time.Time,
	result *sharedRunnerLiveResult,
) {
	t.Helper()
	// One 230-case suite is one prepared child: 140 seconds of settlement
	// workflow, plus at most 20 seconds of outcome/marker I/O and probe slack.
	// This observer window never changes any runner operation's own deadline.
	observation, cancel := context.WithDeadline(ctx, terminalAt.Add(sharedRunnerSettlementWindow))
	defer cancel()
	for {
		probeCtx, cancelProbe := context.WithTimeout(observation, 5*time.Second)
		probe, err := audit.Probe(probeCtx)
		cancelProbe()
		if err != nil {
			failSharedLive(t, ctx, "runner_probe_unavailable")
		}
		applySharedRunnerProbe(t, ctx, probe, result)
		result.TerminalToSettledMS = time.Since(terminalAt).Milliseconds()
		settled, err := sharedRunnerSettlementState(probe, result.Executor, time.Since(terminalAt))
		if err != nil {
			failSharedLive(t, ctx, "settlement_window_expired")
		}
		if settled {
			return
		}
		liveK3Pause(t, observation, 500*time.Millisecond)
	}
}

func waitSharedRunnerMirror(t *testing.T, ctx context.Context, app *bridge.DesktopApp, id string) jobs.JobView {
	t.Helper()
	for {
		view, err := app.GetJob(id)
		if err == nil && view.Execution != nil && view.Execution.Status == domain.RunCompleted {
			return view
		}
		liveK3Pause(t, ctx, 250*time.Millisecond)
	}
}

func sharedRunnerMinimumPerformance(result jobs.PerformanceResultView) bool {
	hasObservation := result.ExecutionStarted && result.ObservationState == jobcontract.PerformanceAvailable &&
		result.Observation != nil
	if !hasObservation {
		return false
	}
	report := result.Observation.Report
	profile := report.Profile
	minimal := profile.RequestCount == 1 && profile.Concurrency == 1 && profile.TimeoutMS == 120_000 &&
		profile.InputTokens == 32 && profile.OutputTokens == 8
	extra := profile.WarmupRequests != 0 || profile.RampDurationMS != 0 || profile.CapacityEnabled ||
		report.Warmup != nil || report.Ramp != nil || report.CapacityResult != nil
	completeCounts := report.Progress.Planned == 1 && report.Progress.Completed == 1 && report.Progress.Launched == 1
	complete := completeCounts && len(report.Samples) == 1
	return minimal && !extra && complete && report.Success
}

func emitSharedRunnerLive(event sharedRunnerLiveEvent) {
	if event.Result != nil && !event.Result.started.IsZero() {
		event.Result.RoleElapsedMS = time.Since(event.Result.started).Milliseconds()
	}
	_ = json.NewEncoder(os.Stdout).Encode(event)
}

func TestSharedRunnerLiveInputIsBoundedAndLoopbackOnly(t *testing.T) {
	valid := map[string]any{
		"role": "a", "endpoint": map[string]any{"Host": "example.test", "Port": 22, "User": "runner",
			"Fingerprint": "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)), "HostKeyType": "ssh-ed25519"},
		"password":       base64.StdEncoding.EncodeToString([]byte("synthetic-unit-password")),
		"base_url":       "http://127.0.0.1:12345/r15/" + strings.Repeat("a", 32) + "/v1",
		"workspace_root": t.TempDir(), "expected_node_id": "83000000-0000-4000-8000-000000000001",
		"expected_installation_id": "83000000-0000-4000-8000-000000000002",
		"expected_version":         "actual-runner-test", "expected_build": strings.Repeat("b", 64), "peer_job_id": "",
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{name: "current role", valid: true},
		{name: "provider URL", change: func(value map[string]any) { value["base_url"] = "https://provider.example/v1" }},
		{name: "nonloopback host", change: func(value map[string]any) {
			value["base_url"] = "http://192.0.2.1:123/r15/" + strings.Repeat("a", 32) + "/v1"
		}},
		{name: "unknown credential field", change: func(value map[string]any) { value["api_key"] = "forbidden" }},
		{name: "missing peer for b", change: func(value map[string]any) { value["role"] = "b" }},
		{name: "missing expected release", change: func(value map[string]any) { value["expected_version"] = "" }},
		{name: "oversized password", change: func(value map[string]any) {
			value["password"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 8193))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := make(map[string]any, len(valid))
			for key, item := range valid {
				value[key] = item
			}
			if test.change != nil {
				test.change(value)
			}
			document, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			input, err := decodeSharedRunnerLiveInput(bytes.NewReader(document))
			defer clear(input.Password)
			if (err == nil) != test.valid {
				t.Fatal("role input acceptance differs from the explicit current boundary")
			}
		})
	}
	if _, err := decodeSharedRunnerLiveInput(bytes.NewReader(bytes.Repeat([]byte(" "), (64<<10)+1))); err == nil {
		t.Fatal("oversized stdin document was accepted")
	}
}

func TestSharedRunnerMinimumCommandHasOneSyntheticRequest(t *testing.T) {
	baseURL := "http://127.0.0.1:12345/r15/" + strings.Repeat("a", 32) + "/v1"
	command := sharedRunnerPerformanceCommand(jobs.Location{Kind: jobcontract.TargetRemote,
		ConfigurationID: "83000000-0000-4000-8000-000000000003"}, baseURL, []byte("r15-mock-synthetic"))
	profile := command.Performance
	extra := profile.WarmupRequests != 0 || profile.RampDurationMS != 0 || profile.CapacityEnabled
	minimal := profile.RequestCount == 1 && profile.Concurrency == 1 && !extra
	if command.Validate() != nil || !minimal || profile.URL != baseURL {
		t.Fatal("shared runner role unexpectedly adds traffic, concurrency, or another endpoint")
	}
}

func TestSharedRunnerSettlementHasIndependentSingleChildWindow(t *testing.T) {
	view, _ := liveK3AuditFixture()
	valid := jobcontract.RunnerInfo{Contract: jobcontract.CurrentContract,
		NodeID: view.Executor.NodeID, NodeInstallationID: view.Executor.InstallationID,
		EngineBuildID: view.Executor.BuildID, RunnerVersion: view.Executor.Version,
		OS: view.Executor.OS, Arch: view.Executor.Arch, Ready: true, Healthy: true,
		Capabilities: jobcontract.RunnerCapabilities{JobKinds: []jobcontract.Kind{jobcontract.KindRun},
			Protocols: []string{"openai_chat"}, Features: []string{}},
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 2000,
			QueueTimeoutNS: int64(time.Minute), ExecutionTimeoutNS: int64(time.Hour)}}
	for _, test := range []struct {
		name    string
		elapsed time.Duration
		change  func(*jobcontract.RunnerInfo)
		settled bool
		failure bool
	}{
		{name: "work may exceed a single storage allowance", elapsed: 10 * time.Second, settled: true},
		{name: "single child worst workflow with remaining observation slack", elapsed: 160 * time.Second, settled: true},
		{name: "last instant within observation", elapsed: sharedRunnerSettlementWindow - time.Nanosecond, settled: true},
		{name: "observation boundary is finite", elapsed: sharedRunnerSettlementWindow, failure: true},
		{name: "still pending is not settled", elapsed: time.Minute, change: func(info *jobcontract.RunnerInfo) {
			info.Pending = 1
		}},
		{name: "unhealthy fails without retry through recovery", elapsed: time.Second,
			change: func(info *jobcontract.RunnerInfo) { info.Ready, info.Healthy = false, false }, failure: true},
		{name: "changed build fails", elapsed: time.Second,
			change: func(info *jobcontract.RunnerInfo) { info.EngineBuildID = strings.Repeat("a", 64) }, failure: true},
		{name: "changed installation fails", elapsed: time.Second,
			change: func(info *jobcontract.RunnerInfo) {
				info.NodeInstallationID = "83000000-0000-4000-8000-000000000009"
			}, failure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			probe := valid.Clone()
			if test.change != nil {
				test.change(&probe)
			}
			settled, err := sharedRunnerSettlementState(probe, view.Executor, test.elapsed)
			if settled != test.settled || (err != nil) != test.failure {
				t.Fatal("single-child settlement observation ignored its identity, health, or work bound")
			}
		})
	}
	stages := sharedRunnerExecutionWindow + sharedRunnerSettlementWindow + sharedRunnerMirrorWindow
	if sharedRunnerSettlementWindow != 3*time.Minute || stages > sharedRunnerRoleWindow {
		t.Fatal("the independent phase windows are not covered by the finite role lifetime")
	}
}
