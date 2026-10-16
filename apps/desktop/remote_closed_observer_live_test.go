package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

const closedObserverLiveEnable = "LLM_TEST_LIVE_REMOTE_CLOSED"

// The one-request performance job targets only the temporary R15 loopback mock.
// The dispatcher releases that mock only after receiving closed_baseline.
type closedObserverLiveResult struct {
	started                     time.Time
	Executor                    jobs.ExecutorView `json:"executor"`
	ClientVersion               string            `json:"client_version"`
	ReportID                    string            `json:"report_id"`
	LaunchCalls                 uint32            `json:"launch_calls"`
	ObservedMS                  int64             `json:"observed_ms"`
	BeforeCloseSequence         uint64            `json:"before_close_sequence"`
	PreCloseObserved            bool              `json:"pre_close_observed"`
	ClosedBaselineSequence      uint64            `json:"closed_baseline_sequence"`
	AdvancedSequence            uint64            `json:"advanced_sequence"`
	ClosedTerminalSequence      uint64            `json:"closed_terminal_sequence"`
	ShutdownReturned            bool              `json:"shutdown_returned"`
	BaselineNonterminal         bool              `json:"baseline_nonterminal"`
	ContinuedWhileClosed        bool              `json:"continued_while_closed"`
	CompletedWhileClosed        bool              `json:"completed_while_closed"`
	ReopenedSameWorkspace       bool              `json:"reopened_same_workspace"`
	SameJobID                   bool              `json:"same_job_id"`
	Sealed                      bool              `json:"sealed"`
	OriginVerified              bool              `json:"origin_verified"`
	GalleryReportVerified       bool              `json:"gallery_report_verified"`
	Samples                     int               `json:"samples"`
	Succeeded                   uint64            `json:"succeeded"`
	MultisuiteTested            bool              `json:"multisuite_tested"`
	ProviderCompatibilityTested bool              `json:"provider_compatibility_tested"`
}

type closedObserverLiveEvent struct {
	Stage  string                    `json:"stage"`
	JobID  string                    `json:"job_id"`
	State  string                    `json:"state"`
	Result *closedObserverLiveResult `json:"result,omitempty"`
}

// This role emits only an allow-listed JSONL protocol, then exits after deferred
// shutdown. No physical connection is made by an ordinary test invocation.
func TestLiveRemoteClosedObserverRole(t *testing.T) {
	if os.Getenv(closedObserverLiveEnable) != "1" {
		t.Skip("explicit one-request loopback-only closed-observer acceptance; bounded stdin only")
	}
	final := closedObserverLiveEvent{Stage: "failed", State: "closed_observer_incomplete"}
	defer func() {
		emitClosedObserverLive(final)
		code := 0
		if t.Failed() || final.Stage != "completed" {
			code = 1
		}
		os.Exit(code)
	}()
	input, err := decodeClosedObserverLiveInput(os.Stdin)
	if err != nil {
		t.Fatal("invalid bounded pinned loopback-only closed-observer configuration")
	}
	defer clear(input.Password)
	ctx, cancel := context.WithTimeout(t.Context(), 170*time.Second)
	defer cancel()
	started := time.Now()
	result := closedObserverLiveResult{ClientVersion: "closed-observer-client", started: started}
	final.Result = &result
	emit := func(stage, id, state string) {
		result.ObservedMS = time.Since(started).Milliseconds()
		emitClosedObserverLive(closedObserverLiveEvent{Stage: stage, JobID: id, State: state, Result: &result})
	}
	app, configurationRoot, store := startSharedRunnerLiveApp(t, ctx, input, result.ClientVersion)
	emit("app_ready", "", "ready")
	defer func() {
		if app != nil {
			_ = app.shutdown()
		}
	}()
	pairing := liveK3Input{Endpoint: input.Endpoint, Password: input.Password,
		ExpectedNodeID: input.ExpectedNodeID, ExpectedInstallation: input.ExpectedInstallation,
		ExpectedVersion: input.ExpectedVersion, ExpectedBuild: input.ExpectedBuild}
	configurationID := pairLiveK3Node(t, ctx, app, pairing)
	location := liveK3RemoteDefault(t, app, configurationID, pairing)
	emit("pair_ready", "", "ready")
	baseline := openSharedRunnerBaseline(t, ctx, sharedRunnerBaselineSettings{
		app: app, input: input, configurationRoot: configurationRoot, configurationID: configurationID, store: store})
	if baseline.Close() != nil {
		t.Fatal("cannot close the pre-submission idle-runner observation")
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal("cannot create the synthetic loopback credential")
	}
	defer clear(secret)
	key := []byte("closed-observer-mock-" + base64.RawURLEncoding.EncodeToString(secret))
	defer clear(key)
	result.LaunchCalls = 1
	emit("launch_started", "", "attempt_started")
	view, launchErr := app.LaunchJob(sharedRunnerPerformanceCommand(location, input.BaseURL, key))
	if launchErr != nil {
		if ownJobs, readErr := readLiveK3OwnJobs(app.GetJobs); readErr == nil && len(ownJobs) == 1 {
			view = ownJobs[0]
		}
	}
	final.JobID = view.JobID
	if !domain.IsUUID(view.JobID) {
		final.State = "launch_identity_unknown_no_resubmission"
		t.Fatal("one launch has no confirmed local identity; retained outbox requires query-only review")
	}
	if view.Submission == nil {
		t.Fatal("the launched job lost its own durable submission")
	}
	emit("launch_returned", view.JobID, string(view.Submission.Delivery))
	// Pending delivery is confirmed only by queries of this exact durable ID.
	// Acceptance does not wait for the unrelated historical mirror backlog.
	if view.Submission.Delivery != jobs.SubmissionAccepted || view.Submission.Confirmation == nil {
		view = waitSharedRunnerAcceptance(t, ctx, app, view.JobID)
	}
	assertLiveK3Executor(t, view, pairing)
	if view.Kind != jobcontract.KindPerformance || len(view.ReportIDs) != 1 ||
		view.Executor.Version == result.ClientVersion {
		t.Fatal("accepted job is missing its stable report or actual runner identity")
	}
	result.Executor, result.ReportID = view.Executor, view.ReportIDs[0]
	emit("accepted", view.JobID, string(view.Submission.Confirmation.Status))
	settings := liveK3AuditSettings{ConfigurationRoot: configurationRoot,
		Credentials: store, ConfigurationID: configurationID, Expected: view}
	preClose := waitClosedObserverRunning(t, ctx, settings)
	result.BeforeCloseSequence, result.PreCloseObserved = preClose.LatestSequence, true
	emit("pre_close_running", view.JobID, string(preClose.Receipt.Status))
	if app.shutdown() != nil {
		t.Fatal("desktop shutdown did not return successfully")
	}
	app = nil
	result.ShutdownReturned = true
	emit("shutdown_returned", view.JobID, "closed")
	observeClosedObserverCompletion(t, ctx, settings, &result)

	// Reopening uses the same DB, catalog, saved pairing and memory store. This
	// creates no replacement launch and does not change the original receipt.
	app = reopenClosedObserverApp(t, ctx, input.WorkspaceRoot, configurationRoot, store, result.ClientVersion)
	result.ReopenedSameWorkspace = true
	if reopenedLocation := liveK3RemoteDefault(t, app, configurationID, pairing); reopenedLocation != location {
		t.Fatal("the same workspace did not retain its explicit remote default")
	}
	reopened := waitSharedRunnerMirror(t, ctx, app, view.JobID)
	assertLiveK3Executor(t, reopened, pairing)
	if reopened.JobID != view.JobID || reopened.Executor != view.Executor ||
		len(reopened.ReportIDs) != 1 || reopened.ReportIDs[0] != result.ReportID {
		t.Fatal("reopening replaced the original job, report, or accepted executor")
	}
	result.SameJobID = true
	emit("reopened", view.JobID, string(reopened.Execution.Status))
	outcome := waitLiveK3Performance(t, ctx, app, view.JobID)
	archive := waitLiveK3Archive(t, ctx, app, result.ReportID)
	result.Sealed = outcome.SealedAt != ""
	result.OriginVerified = liveK3PerformanceOriginMatches(outcome, reopened) && liveK3ArchiveOriginMatches(archive, outcome)
	if outcome.ReportID != result.ReportID || !sharedRunnerMinimumPerformance(outcome) || !result.OriginVerified {
		t.Fatal("the original one-request report or actual terminal and archive origins are incomplete")
	}
	result.Samples = len(outcome.Observation.Report.Samples)
	result.Succeeded = outcome.Observation.Report.Progress.Succeeded
	assertLiveK3Outbox(t, app, []liveK3JobSummary{{JobID: view.JobID}})
	gallery, galleryErr := app.GetReports()
	if galleryErr != nil || !closedObserverGalleryMatches(gallery, result.ReportID) {
		t.Fatal("the original report is missing from the reopened gallery")
	}
	result.GalleryReportVerified = true
	result.ObservedMS = time.Since(started).Milliseconds()
	final = closedObserverLiveEvent{Stage: "completed", JobID: view.JobID, State: "completed", Result: &result}
}

func decodeClosedObserverLiveInput(reader io.Reader) (sharedRunnerLiveInput, error) {
	input, err := decodeSharedRunnerLiveInput(reader)
	if err != nil {
		return sharedRunnerLiveInput{}, err
	}
	if input.Role != "a" || input.PeerJobID != "" {
		clear(input.Password)
		return sharedRunnerLiveInput{}, errors.New("one independent closed observer role is required")
	}
	return input, nil
}

func waitClosedObserverRunning(
	t *testing.T,
	ctx context.Context,
	settings liveK3AuditSettings,
) jobcontract.Snapshot {
	t.Helper()
	connection, err := openLiveK3Audit(ctx, settings)
	if err != nil {
		t.Fatal("cannot open the independent read-only pre-shutdown observation")
	}
	defer connection.Close()
	for {
		snapshot, err := connection.GetJob(ctx, settings.Expected.JobID)
		if err == nil {
			if !closedObserverSnapshotMatches(settings.Expected, snapshot) {
				t.Fatal("the pre-shutdown observation changed the original job identity")
			}
			if jobcontract.Terminal(snapshot.Receipt.Status) {
				t.Fatal("the gated mock job became terminal before observer shutdown")
			}
			if closedObserverRunningSnapshot(settings.Expected, snapshot) {
				if connection.Close() != nil {
					t.Fatal("cannot close the independent pre-shutdown observation")
				}
				return snapshot
			}
		}
		liveK3Pause(t, ctx, 250*time.Millisecond)
	}
}

func closedObserverRunningSnapshot(expected jobs.JobView, snapshot jobcontract.Snapshot) bool {
	return closedObserverSnapshotMatches(expected, snapshot) && snapshot.Receipt.Status == domain.RunRunning
}

func closedObserverSnapshotMatches(expected jobs.JobView, snapshot jobcontract.Snapshot) bool {
	if expected.Submission == nil {
		return false
	}
	confirmation := expected.Submission.Confirmation
	if confirmation == nil || !liveK3AuditIdentity(expected, snapshot) {
		return false
	}
	return snapshot.Receipt.Revision >= confirmation.Revision &&
		snapshot.Receipt.AcceptedAt.Format(time.RFC3339Nano) == confirmation.AcceptedAt
}

func closedObserverGalleryMatches(gallery reporting.Snapshot, reportID string) bool {
	matches := 0
	for _, report := range gallery.Reports {
		if report.ID != reportID {
			continue
		}
		if report.Source != reporting.SourceQuickPerformance {
			return false
		}
		matches++
	}
	return matches == 1
}

func observeClosedObserverCompletion(
	t *testing.T,
	ctx context.Context,
	settings liveK3AuditSettings,
	result *closedObserverLiveResult,
) {
	t.Helper()
	connection, err := openLiveK3Audit(ctx, settings)
	if err != nil {
		t.Fatal("cannot open the independent read-only connection after shutdown")
	}
	defer connection.Close()
	baseline, err := connection.GetJob(ctx, settings.Expected.JobID)
	if err != nil || !closedObserverSnapshotMatches(settings.Expected, baseline) ||
		baseline.LatestSequence < result.BeforeCloseSequence || jobcontract.Terminal(baseline.Receipt.Status) {
		t.Fatal("the post-shutdown baseline is unavailable, changed, regressed, or already terminal")
	}
	result.ClosedBaselineSequence, result.BaselineNonterminal = baseline.LatestSequence, true
	emitClosedObserverLive(closedObserverLiveEvent{Stage: "closed_baseline", JobID: settings.Expected.JobID,
		State: string(baseline.Receipt.Status), Result: result})
	for {
		current, readErr := connection.GetJob(ctx, settings.Expected.JobID)
		if readErr != nil {
			liveK3Pause(t, ctx, 250*time.Millisecond)
			continue
		}
		switch classifyLiveK3Audit(settings.Expected, baseline, current) {
		case "identity_changed", "sequence_regressed":
			t.Fatal("closed-window read changed the accepted identity or regressed its durable sequence")
		case "advanced":
			if !result.ContinuedWhileClosed {
				result.AdvancedSequence, result.ContinuedWhileClosed = current.LatestSequence, true
				emitClosedObserverLive(closedObserverLiveEvent{Stage: "closed_advanced", JobID: settings.Expected.JobID,
					State: string(current.Receipt.Status), Result: result})
			}
		}
		if jobcontract.Terminal(current.Receipt.Status) {
			if current.Receipt.Status != domain.RunCompleted || !result.ContinuedWhileClosed {
				t.Fatal("the same closed-window job did not advance to successful completion")
			}
			result.ClosedTerminalSequence, result.CompletedWhileClosed = current.LatestSequence, true
			emitClosedObserverLive(closedObserverLiveEvent{Stage: "closed_terminal", JobID: settings.Expected.JobID,
				State: string(current.Receipt.Status), Result: result})
			return
		}
		liveK3Pause(t, ctx, 250*time.Millisecond)
	}
}

func reopenClosedObserverApp(
	t *testing.T,
	ctx context.Context,
	workspace, configurationRoot string,
	store credentials.Store,
	version string,
) *DesktopApp {
	t.Helper()
	source, err := runnerartifacts.NewDirectorySource(filepath.Join(workspace, "no-runner-artifacts"))
	if err != nil {
		t.Fatal("cannot retain the no-install runner source during reopen")
	}
	options := productionOptions{userConfigDir: func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(workspace, "catalog", "shared-live.exe"), nil },
		appVersion:     version, credentialStore: store, runnerSource: source}
	return startLiveK3App(t, ctx, options)
}

func emitClosedObserverLive(event closedObserverLiveEvent) {
	if event.Result != nil && !event.Result.started.IsZero() {
		event.Result.ObservedMS = time.Since(event.Result.started).Milliseconds()
	}
	_ = json.NewEncoder(os.Stdout).Encode(event)
}

func TestClosedObserverLiveInputAllowsOnlyOneIndependentRole(t *testing.T) {
	input := map[string]any{
		"role": "a", "endpoint": map[string]any{"Host": "example.test", "Port": 22, "User": "runner",
			"Fingerprint": "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)), "HostKeyType": "ssh-ed25519"},
		"password":       base64.StdEncoding.EncodeToString([]byte("synthetic-unit-password")),
		"base_url":       "http://127.0.0.1:12345/r15/" + strings.Repeat("a", 32) + "/v1",
		"workspace_root": t.TempDir(), "expected_node_id": "83000000-0000-4000-8000-000000000001",
		"expected_installation_id": "83000000-0000-4000-8000-000000000002",
		"expected_version":         "actual-runner-unit", "expected_build": strings.Repeat("b", 64), "peer_job_id": "",
	}
	for _, test := range []struct {
		name, role, peer string
		valid            bool
	}{
		{name: "one role", role: "a", valid: true},
		{name: "second client", role: "b", peer: "83000000-0000-4000-8000-000000000003"},
		{name: "unexpected peer", role: "a", peer: "83000000-0000-4000-8000-000000000003"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input["role"], input["peer_job_id"] = test.role, test.peer
			document, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := decodeClosedObserverLiveInput(bytes.NewReader(document))
			defer clear(decoded.Password)
			if (err == nil) != test.valid {
				t.Fatal("closed-observer input accepted a second participant or rejected its only role")
			}
		})
	}
}

func TestClosedObserverRunningUsesIndependentSnapshotWithoutLocalExecution(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*jobs.JobView, *jobcontract.Snapshot)
		valid  bool
	}{
		{name: "matching remote running snapshot without a local mirror", valid: true},
		{name: "foreign job without own submission", change: func(view *jobs.JobView, _ *jobcontract.Snapshot) {
			view.Submission = nil
		}},
		{name: "unconfirmed delivery", change: func(view *jobs.JobView, _ *jobcontract.Snapshot) {
			view.Submission.Confirmation = nil
		}},
		{name: "different job", change: func(_ *jobs.JobView, snapshot *jobcontract.Snapshot) {
			snapshot.Receipt.JobID = "83000000-0000-4000-8000-000000000009"
		}},
		{name: "different origin", change: func(_ *jobs.JobView, snapshot *jobcontract.Snapshot) {
			snapshot.Receipt.Origin.RunnerVersion = "another-runner"
		}},
		{name: "changed acceptance", change: func(_ *jobs.JobView, snapshot *jobcontract.Snapshot) {
			snapshot.Receipt.AcceptedAt = snapshot.Receipt.AcceptedAt.Add(time.Second)
		}},
		{name: "terminal before closing", change: func(_ *jobs.JobView, snapshot *jobcontract.Snapshot) {
			snapshot.Receipt.Status, snapshot.Receipt.Revision = domain.RunCompleted, 4
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			view, snapshot := liveK3AuditFixture()
			view.Submission = &jobs.SubmissionView{Delivery: jobs.SubmissionAccepted,
				Confirmation: &jobs.ConfirmationView{Status: domain.RunQueued, Revision: 1,
					AcceptedAt: snapshot.Receipt.AcceptedAt.Format(time.RFC3339Nano)}}
			view.Execution = nil
			if test.change != nil {
				test.change(&view, &snapshot)
			}
			if got := closedObserverRunningSnapshot(view, snapshot); got != test.valid {
				t.Fatal("pre-close observation acceptance differs from the independent current snapshot")
			}
			if view.Execution != nil {
				t.Fatal("the helper fabricated a local mirror from the remote snapshot")
			}
		})
	}
}

func TestClosedObserverGalleryFindsOriginalReportAmongSharedHistory(t *testing.T) {
	id := "83000000-0000-4000-8000-000000000003"
	original := reporting.Summary{ID: id, Source: reporting.SourceQuickPerformance}
	foreign := reporting.Summary{ID: "83000000-0000-4000-8000-000000000004", Source: reporting.SourceRun}
	for _, test := range []struct {
		name    string
		reports []reporting.Summary
		valid   bool
	}{
		{name: "original alone", reports: []reporting.Summary{original}, valid: true},
		{name: "original among imported history", reports: []reporting.Summary{foreign, original}, valid: true},
		{name: "original absent", reports: []reporting.Summary{foreign}},
		{name: "duplicate original", reports: []reporting.Summary{original, foreign, original}},
		{name: "wrong original source", reports: []reporting.Summary{{ID: id, Source: reporting.SourceRun}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gallery := reporting.Snapshot{SchemaVersion: 1, Reports: test.reports}
			if got := closedObserverGalleryMatches(gallery, id); got != test.valid {
				t.Fatal("the original report was not matched uniquely within shared imported history")
			}
		})
	}
}
