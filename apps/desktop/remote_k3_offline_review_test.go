package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

type liveK3OfflineInput struct {
	Workspace string                     `json:"workspace"`
	Executor  jobs.ExecutorView          `json:"executor"`
	Jobs      []liveK3OfflineExpectedJob `json:"jobs"`
}

type liveK3OfflineExpectedJob struct {
	JobID         string `json:"job_id"`
	RunID         string `json:"run_id"`
	ReportID      string `json:"report_id"`
	SuiteKey      string `json:"suite_key"`
	ExpectedCases int    `json:"expected_cases"`
	Passed        uint64 `json:"passed"`
	Failed        uint64 `json:"failed"`
	Observed      uint64 `json:"observed"`
	Indeterminate uint64 `json:"indeterminate"`
}

type liveK3OfflineReview struct {
	SchemaVersion                  int                `json:"schema_version"`
	ReviewedAt                     string             `json:"reviewed_at"`
	UsedConsistentPrivateClone     bool               `json:"used_consistent_private_clone"`
	CloneRoot                      string             `json:"clone_root"`
	SourceExecutionTablesUnchanged bool               `json:"source_execution_tables_unchanged"`
	SourceSummaryUnchanged         bool               `json:"source_summary_unchanged"`
	OriginalSummaryPassed          bool               `json:"original_summary_passed"`
	OriginalContinuedWhileClosed   bool               `json:"original_continued_while_closed"`
	OfflineQueriesPassed           bool               `json:"offline_queries_passed"`
	GalleryReportsVerified         bool               `json:"gallery_reports_verified"`
	ProviderAssertionsPassed       bool               `json:"provider_assertions_passed"`
	OverallAcceptancePassed        bool               `json:"overall_acceptance_passed"`
	DeniedManagementConnections    uint64             `json:"denied_management_connections"`
	Jobs                           []liveK3JobSummary `json:"jobs"`
	Failures                       []string           `json:"failures"`
}

// This explicit follow-up reads a completed workspace without any old credential
// store. The source DB is opened mode=ro and backed up by SQLite, not copied with
// a potentially inconsistent WAL. Production startup operates only on the clone.
// No test code in this entry point calls Launch, Control, Retry or Reconcile.
func TestLiveRemoteK3ExistingWorkspaceOfflineReview(t *testing.T) {
	if os.Getenv("LLM_TEST_LIVE_REMOTE_K3_OFFLINE_REVIEW") != "1" {
		t.Skip("explicit read-only completed-workspace review; non-secret expectations arrive on stdin")
	}
	input := readLiveK3OfflineInput(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	review := liveK3OfflineReview{SchemaVersion: 1, ReviewedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Jobs: []liveK3JobSummary{}, Failures: []string{}}
	sourceSummaryPath := filepath.Join(input.Workspace, "acceptance-summary.json")
	sourceSummary, err := os.ReadFile(sourceSummaryPath)
	if err != nil || len(sourceSummary) > 4<<20 {
		t.Fatal("cannot read the original bounded safe summary")
	}
	originalHash := sha256.Sum256(sourceSummary)
	var original liveK3Summary
	if json.Unmarshal(sourceSummary, &original) != nil {
		t.Fatal("original safe summary is invalid")
	}
	review.OriginalSummaryPassed, review.OriginalContinuedWhileClosed = original.Passed, original.ContinuedWhileClosed
	sourceDB := filepath.Join(input.Workspace, "configuration", "llm-test-studio", "llm-test-studio.db")
	sourceDigest, err := liveK3ReadOnlyDigest(ctx, sourceDB)
	if err != nil {
		t.Fatal("source database is not an accepted, settled remote-only workspace")
	}
	clone, err := os.MkdirTemp("", "llm-remote-k3-offline-review-")
	if err != nil {
		t.Fatal("cannot create a private offline review clone")
	}
	review.CloneRoot = clone
	reviewPath := filepath.Join(clone, "acceptance-offline-review.safe.json")
	defer func() {
		currentDigest, digestErr := liveK3ReadOnlyDigest(ctx, sourceDB)
		review.SourceExecutionTablesUnchanged = digestErr == nil && sourceDigest == currentDigest
		currentSummary, readErr := os.ReadFile(sourceSummaryPath)
		review.SourceSummaryUnchanged = readErr == nil && sha256.Sum256(currentSummary) == originalHash
		if !review.SourceExecutionTablesUnchanged || !review.SourceSummaryUnchanged {
			review.Failures = append(review.Failures, "source_changed_during_review")
			t.Error("source facts changed during offline review")
		}
		if t.Failed() && len(review.Failures) == 0 {
			review.Failures = append(review.Failures, "offline_review_incomplete")
		}
		review.OfflineQueriesPassed = !t.Failed() && len(review.Jobs) == 3
		// A clone read cannot retroactively prove the old observer-disconnect
		// interval or turn provider assertion failures into successful cases.
		review.OverallAcceptancePassed = review.OfflineQueriesPassed && review.ProviderAssertionsPassed &&
			review.OriginalContinuedWhileClosed && review.OriginalSummaryPassed
		document, encodeErr := json.MarshalIndent(review, "", "  ")
		if encodeErr != nil {
			t.Error("cannot persist the independent safe offline review")
			return
		}
		file, openErr := os.OpenFile(reviewPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if openErr != nil {
			t.Error("cannot exclusively create the independent safe offline review")
			return
		}
		_, writeErr := file.Write(document)
		if errors.Join(writeErr, file.Close()) != nil {
			t.Error("cannot persist the independent safe offline review")
			return
		}
		t.Logf("offline review report=%s", reviewPath)
	}()
	configurationRoot, executableRoot := filepath.Join(clone, "configuration"), filepath.Join(clone, "catalog")
	cloneDB := filepath.Join(configurationRoot, "llm-test-studio", "llm-test-studio.db")
	if os.MkdirAll(filepath.Dir(cloneDB), 0o700) != nil {
		t.Fatal("cannot prepare offline clone storage")
	}
	if liveK3SQLiteBackup(ctx, sourceDB, cloneDB) != nil {
		t.Fatal("cannot create the consistent SQLite review backup")
	}
	if copyLiveK3AuthoredCatalog(filepath.Join(input.Workspace, "catalog"), executableRoot) != nil {
		t.Fatal("cannot copy the bounded current authored definitions")
	}
	artifactRoot := filepath.Join(clone, "no-runner-artifacts")
	if os.MkdirAll(artifactRoot, 0o700) != nil {
		t.Fatal("cannot disable offline runner installation")
	}
	source, err := runnerartifacts.NewDirectorySource(artifactRoot)
	if err != nil {
		t.Fatal("cannot create offline artifact policy")
	}
	var denied atomic.Uint64
	options := productionOptions{userConfigDir: func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(executableRoot, "offline-review.exe"), nil },
		appVersion:     "live-remote-k3-offline-review", credentialStore: credentials.NewMemoryStore(), runnerSource: source,
		remoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
			denied.Add(1)
			return nil, remotessh.ErrConnection
		},
		runnerConnector: func(context.Context, *remotenodes.Service, remotesync.Node, jobs.Access) (runnerConnection, error) {
			denied.Add(1)
			return nil, jobs.ErrUnavailable
		}}
	app := startLiveK3App(t, ctx, options)
	defer func() { _ = app.shutdown(); review.DeniedManagementConnections = denied.Load() }()
	review.UsedConsistentPrivateClone = true
	suites := liveK3FullSuites(t, app)
	page, err := app.GetJobs("", 25)
	if err != nil || page.HasMore || len(page.Jobs) != 3 {
		t.Fatal("offline clone does not contain exactly the expected three submissions")
	}
	galleryStarted := time.Now()
	gallery, galleryErr := app.GetReports()
	if galleryErr != nil || len(gallery.Reports) != len(input.Jobs) {
		t.Fatal("offline gallery does not contain exactly the three expected reports")
	}
	for index, expected := range input.Jobs {
		expectedSource := reporting.SourceRun
		if index == 2 {
			expectedSource = reporting.SourceQuickPerformance
		}
		matches := 0
		for _, report := range gallery.Reports {
			if report.ID == expected.ReportID {
				if report.Source != expectedSource || report.RunID != expected.RunID {
					t.Fatal("offline gallery report source or run identity differs from recorded facts")
				}
				matches++
			}
		}
		if matches != 1 {
			t.Fatal("offline gallery is missing an expected report or contains duplicate identities")
		}
	}
	review.GalleryReportsVerified = true
	t.Logf("gallery reports=%d elapsed_ms=%d", len(gallery.Reports), time.Since(galleryStarted).Milliseconds())
	review.ProviderAssertionsPassed = true
	for index, expected := range input.Jobs {
		view, readErr := app.GetJob(expected.JobID)
		if readErr != nil || view.Executor != input.Executor || view.Delivery != jobs.SubmissionAccepted ||
			view.Execution == nil || view.Execution.Status != "completed" {
			t.Fatal("offline authoritative job identity or terminal state differs from expectations")
		}
		controls, controlsErr := app.GetJobControls(expected.JobID, "", 25)
		if controlsErr != nil || controls.HasMore || len(controls.Commands) != 0 {
			t.Fatal("offline job has unexpected control instructions")
		}
		item := liveK3JobSummary{JobID: view.JobID, Executor: view.Executor, Delivery: string(view.Delivery),
			Status: string(view.Execution.Status), ReportID: expected.ReportID, FailureCases: []liveK3CaseFailure{}}
		if index < 2 {
			if len(view.RunIDs) != 1 || view.RunIDs[0] != expected.RunID {
				t.Fatal("offline suite report does not belong to its submitted job")
			}
			detail, detailErr := app.GetReportDetail(expected.ReportID)
			if detailErr != nil || detail.Report.ID != expected.ReportID || detail.Report.RunID != expected.RunID || detail.Report.Validate() != nil {
				t.Fatal("offline full-suite report does not match its fixed identities")
			}
			item.SuiteKey, item.ExpectedCases = expected.SuiteKey, expected.ExpectedCases
			collectLiveK3Suite(&item, detail, suites[index], view.Executor)
			matchingCounts := item.Passed == expected.Passed && item.Failed == expected.Failed &&
				item.Observed == expected.Observed && item.Indeterminate == expected.Indeterminate
			if item.ActualCases != expected.ExpectedCases || !item.CaseIdentitiesMatch || !item.RunnerReportOrigin || !matchingCounts {
				t.Fatal("offline suite counts, case identities, or actual runner origin differ from recorded facts")
			}
			if !detail.Report.Conclusion.Passed {
				review.ProviderAssertionsPassed = false
			}
		} else {
			if len(view.ReportIDs) != 1 || view.ReportIDs[0] != expected.ReportID {
				t.Fatal("offline performance report does not belong to its submitted job")
			}
			result, resultErr := app.GetJobPerformanceResult(expected.JobID)
			if resultErr != nil || result.ReportID != expected.ReportID || !liveK3PerformanceOriginMatches(result, view) ||
				!liveK3MinimumPerformance(result) {
				t.Fatal("offline complete performance result or authoritative terminal origin is inconsistent")
			}
			archive, archiveErr := app.GetReportDetail(expected.ReportID)
			if archiveErr != nil || archive.Performance == nil || !liveK3ArchiveOriginMatches(*archive.Performance, result) {
				t.Fatal("offline statistical archive is missing its actual non-null runner origin")
			}
			item.Performance = summarizeLiveK3Performance(result)
			item.Performance.TerminalOriginVerified = true
			item.Performance.CheckpointOriginPresent = result.Observation.Report.ExecutionOrigin != nil
			item.Performance.StatisticalArchiveOriginVerified, item.RunnerReportOrigin = true, true
		}
		review.Jobs = append(review.Jobs, item)
		t.Logf("job=%s status=%s cases=%d/%d", item.JobID, item.Status, item.ActualCases, item.ExpectedCases)
	}
}

func readLiveK3OfflineInput(t *testing.T) liveK3OfflineInput {
	t.Helper()
	document, err := io.ReadAll(io.LimitReader(os.Stdin, (32<<10)+1))
	if err != nil || len(document) > 32<<10 {
		t.Fatal("invalid bounded non-secret offline expectations")
	}
	var input liveK3OfflineInput
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		t.Fatal("invalid offline expectations")
	}
	validRoot := filepath.IsAbs(input.Workspace) && !strings.ContainsAny(input.Workspace, "?#")
	if !validRoot || len(input.Jobs) != 3 {
		t.Fatal("offline review requires an exact workspace and three ordered job identities")
	}
	input.Workspace = filepath.Clean(input.Workspace)
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: input.Executor.NodeID,
		InstallationID: input.Executor.InstallationID, RunnerVersion: input.Executor.Version,
		EngineBuildID: input.Executor.BuildID, OS: input.Executor.OS, Arch: input.Executor.Arch}
	if origin.Validate() != nil {
		t.Fatal("offline expectations require the complete actual runner identity")
	}
	seen := map[string]bool{}
	for index, item := range input.Jobs {
		if !domain.IsUUID(item.JobID) || !domain.IsUUID(item.ReportID) || seen[item.JobID] {
			t.Fatal("offline expectations require three distinct fixed job and report identities")
		}
		seen[item.JobID] = true
		if index < 2 && !domain.IsUUID(item.RunID) {
			t.Fatal("offline suite expectations require a fixed run identity")
		}
	}
	if input.Jobs[0].SuiteKey != "kimi-k3.official-foundation" || input.Jobs[0].ExpectedCases != 84 ||
		input.Jobs[1].SuiteKey != "openai-responses.kimi-k3.official-foundation" || input.Jobs[1].ExpectedCases != 230 {
		t.Fatal("offline review requires both full authored suites")
	}
	return input
}

func liveK3SQLiteBackup(ctx context.Context, source, destination string) error {
	if source == destination {
		return errors.New("source and clone must differ")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		return errors.New("clone destination must not exist")
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(source)+"?mode=ro")
	if err != nil {
		return err
	}
	defer database.Close()
	_, err = database.ExecContext(ctx, "VACUUM main INTO ?", destination)
	return err
}

// The digest contains only stable execution/report facts; no payload is logged.
// Preflight excludes every queue that could make production startup execute work.
func liveK3ReadOnlyDigest(ctx context.Context, path string) (string, error) {
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return "", err
	}
	defer database.Close()
	var pending, controls, local int
	err = database.QueryRowContext(ctx, `SELECT
		(SELECT count(*) FROM job_submissions WHERE state != 'accepted'),
		(SELECT count(*) FROM job_control_outbox), (SELECT count(*) FROM execution_jobs)`).Scan(&pending, &controls, &local)
	if err != nil || pending != 0 || controls != 0 || local != 0 {
		return "", errors.New("workspace is not settled remote-only data")
	}
	digest := sha256.New()
	queries := []string{
		"SELECT document_json FROM job_submissions ORDER BY job_id",
		"SELECT document_json FROM job_control_outbox ORDER BY command_id",
		"SELECT spec_json || char(0) || snapshot_json || char(0) || applied_sequence FROM remote_job_mirrors ORDER BY job_id",
		"SELECT document_json FROM remote_job_events ORDER BY job_id, sequence",
		"SELECT document_json || char(0) || imported FROM remote_job_report_refs ORDER BY job_id, reference_type, report_id",
		"SELECT document_json FROM remote_performance_results ORDER BY job_id",
		"SELECT document_json FROM reports ORDER BY id",
		"SELECT document_json FROM quick_performance_reports ORDER BY id",
		"SELECT document_json FROM case_results ORDER BY id",
	}
	for _, query := range queries {
		_, _ = io.WriteString(digest, query)
		rows, err := database.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return "", err
			}
			var length [8]byte
			binary.BigEndian.PutUint64(length[:], uint64(len(raw)))
			_, _ = digest.Write(length[:])
			_, _ = digest.Write(raw)
		}
		err = errors.Join(rows.Err(), rows.Close())
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyLiveK3AuthoredCatalog(source, destination string) error {
	var total int64
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("authored clone does not follow links")
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return errors.New("authored clone file is invalid")
		}
		total += info.Size()
		if total > 64<<20 {
			return errors.New("authored clone exceeds its byte limit")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	})
}
