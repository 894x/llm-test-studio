package sqlite_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type submittedReadFixture struct {
	repository *persistence.Repository
	executor   *persistence.Repository
	db         *sql.DB
	spec       jobcontract.JobSpec
	record     jobs.SubmissionRecord
	snapshot   jobcontract.Snapshot
}

func newSubmittedReadFixture(t *testing.T, remote bool) submittedReadFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "submitted-read.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "query-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	t.Cleanup(func() { _ = repository.Close() })
	db := openDatabase(t, path)
	t.Cleanup(func() { _ = db.Close() })
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	if remote {
		spec = remoteSpec(spec)
	}
	record := submissionFixture(t, spec)
	if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	view, err := repository.Job(t.Context(), spec.JobID)
	if err != nil || view.Submission.Delivery != jobs.SubmissionPrepared || view.Execution != nil {
		t.Fatalf("preparation invented execution state: %v", err)
	}
	record = pendingSubmission(record)
	if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
		t.Fatal(err)
	}
	view, err = repository.Job(t.Context(), spec.JobID)
	if err != nil || view.Submission.Delivery != jobs.SubmissionPending || view.Execution != nil {
		t.Fatalf("pending delivery invented execution state: %v", err)
	}
	executor := repository
	if remote {
		executor = openOperationalRepository(t)
		t.Cleanup(func() { _ = executor.Close() })
	}
	snapshot, _, err := executor.AcceptJob(t.Context(), jobAcceptance(spec))
	if err != nil {
		t.Fatal(err)
	}
	if remote {
		applyRemoteEvents(t, repository, remoteEvents(t, executor, spec))
	}
	return submittedReadFixture{repository: repository, executor: executor, db: db, spec: spec,
		record: record, snapshot: snapshot}
}

func (fixture *submittedReadFixture) confirm(t *testing.T) {
	t.Helper()
	next := fixture.record
	next.Revision++
	next.State, next.Receipt = jobs.SubmissionAccepted, &fixture.snapshot.Receipt
	next.UpdatedAt = fixture.snapshot.UpdatedAt.Add(time.Second)
	if err := fixture.repository.UpdateSubmission(t.Context(), next, fixture.record.Revision); err != nil {
		t.Fatal(err)
	}
	fixture.record = next
}

func TestSubmittedQueryPreservesPendingAndFixedExecutorFacts(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			fixture := newSubmittedReadFixture(t, remote)
			for _, accepted := range []bool{false, true} {
				if accepted {
					fixture.confirm(t)
				}
				view, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
				if err != nil || view.Submission.Delivery != fixture.record.State || view.Submission.DeliveryRevision != fixture.record.Revision {
					t.Fatalf("delivery was rewritten by a query: %v", err)
				}
				if view.Execution == nil || view.Execution.Status != domain.RunQueued || view.Execution.Revision != 1 {
					t.Fatal("actual execution receipt was lost")
				}
				if view.Executor.Version != fixture.snapshot.Receipt.Origin.RunnerVersion || view.Executor.BuildID != fixture.spec.EngineBuildID {
					t.Fatal("query replaced the frozen executor with the desktop version")
				}
				if view.Progress != nil || view.PerformanceResultReady || view.RunIDs == nil || view.ReportIDs == nil {
					t.Fatal("run query manufactured performance data or omitted collection fields")
				}
			}
			stored, err := fixture.repository.GetSubmission(t.Context(), fixture.spec.JobID)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(
				t,
				"read-only outbox",
				fixture.record,
				stored,
			)
		})
	}
}

func TestSubmittedQueryDistinguishesUnsynchronizedRemoteAndMissingLocalExecution(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			fixture := newSubmittedReadFixture(t, remote)
			fixture.confirm(t)
			// Remove the projection through a separate connection to simulate missing storage.
			if _, err := fixture.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
				t.Fatal(err)
			}
			statement := `DELETE FROM execution_jobs WHERE id = ?`
			if remote {
				statement = `DELETE FROM remote_job_mirrors WHERE job_id = ?`
			}
			if _, err := fixture.db.ExecContext(t.Context(), statement, fixture.spec.JobID); err != nil {
				t.Fatal(err)
			}
			view, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
			if remote {
				if err != nil || view.Submission.Delivery != jobs.SubmissionAccepted || view.Execution != nil {
					t.Fatalf("unsynchronized remote state invented a receipt: %v", err)
				}
			} else if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("missing accepted local execution was hidden: %v", err)
			}
		})
	}
}

func TestSubmittedRemoteConfirmationCanLeadJournalSynchronization(t *testing.T) {
	fixture := newSubmittedReadFixture(t, true)
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
		next, err := fixture.executor.ChangeJob(t.Context(), jobhost.Change{
			JobID: fixture.spec.JobID, ExpectedRevision: fixture.snapshot.Receipt.Revision,
			Status: status, At: fixture.snapshot.UpdatedAt.Add(time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		fixture.snapshot = next
	}
	fixture.confirm(t)
	view, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
	if err != nil || view.Submission.Confirmation == nil || view.Submission.Confirmation.Status != domain.RunCompleted {
		t.Fatalf("newer confirmed receipt was lost: %v", err)
	}
	if view.Execution != nil || view.Progress != nil || view.PerformanceResultReady {
		t.Fatal("lagging mirror was published ahead of journal synchronization")
	}
	applyRemoteEvents(t, fixture.repository, remoteEvents(t, fixture.executor, fixture.spec))
	view, err = fixture.repository.Job(t.Context(), fixture.spec.JobID)
	if err != nil || view.Execution == nil || view.Execution.Status != domain.RunCompleted ||
		view.Execution.Revision != view.Submission.Confirmation.Revision {
		t.Fatalf("caught-up mirror was not published: %v", err)
	}
	stored, err := fixture.repository.GetSubmission(t.Context(), fixture.spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"confirmation unchanged by read/sync",
		fixture.record,
		stored,
	)
}

func TestSubmittedQueryRejectsOversizedAndChangedExecutionWithoutMutation(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, target := range []string{"spec size", "snapshot size", "spec digest", "origin", "namespace"} {
			if !remote && target == "origin" || remote && target == "namespace" {
				continue
			}
			t.Run(map[bool]string{false: "local/", true: "remote/"}[remote]+target, func(t *testing.T) {
				fixture := newSubmittedReadFixture(t, remote)
				fixture.confirm(t)
				table, key := "execution_jobs", "id"
				if remote {
					table, key = "remote_job_mirrors", "job_id"
				}
				var mutation string
				var value any
				switch target {
				case "spec size":
					mutation, value = "spec_json = ?", `{"padding":"`+strings.Repeat("x", jobcontract.MaxSubmissionBytes)+`"}`
				case "snapshot size":
					mutation, value = "snapshot_json = ?", `{"padding":"`+strings.Repeat("x", jobcontract.MaxStateBytes)+`"}`
				case "spec digest":
					mutation, value = `spec_json = json_set(spec_json, '$.engine_build_id', ?)`, strings.Repeat("c", 64)
				case "origin":
					mutation, value = "configuration_id = ?", remoteOrigin.NodeID
				case "namespace":
					_, err := fixture.db.ExecContext(
						t.Context(),
						`INSERT INTO remote_job_mirrors
						(job_id, configuration_id, node_id, installation_id, spec_sha256, spec_json, snapshot_json, applied_sequence)
						SELECT id, ?, ?, installation_id, spec_sha256, spec_json, snapshot_json, 1 FROM execution_jobs`,
						remoteOrigin.ConfigurationID,
						remoteOrigin.NodeID,
					)
					if err != nil {
						t.Fatal(err)
					}
				}
				if mutation != "" {
					if _, err := fixture.db.ExecContext(
						t.Context(),
						"UPDATE "+table+" SET "+mutation+" WHERE "+key+" = ?",
						value,
						fixture.spec.JobID,
					); err != nil {
						t.Fatal(err)
					}
				}
				read := func() []byte {
					var document []byte
					if err := fixture.db.QueryRowContext(t.Context(), "SELECT spec_json || snapshot_json FROM "+table+
						" WHERE "+key+" = ?", fixture.spec.JobID).Scan(&document); err != nil {
						t.Fatal(err)
					}
					return document
				}
				before := read()
				_, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
				if !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("corrupt execution was projected: %v", err)
				}
				if !bytes.Equal(before, read()) {
					t.Fatal("rejected data was mutated")
				}
			})
		}
	}
}

func TestSubmittedProgressRejectsInvalidFactsBeforeProjection(t *testing.T) {
	for _, target := range []string{"phase", "count", "time", "owner", "size"} {
		t.Run(target, func(t *testing.T) {
			fixture := newPerformanceStorageFixture(t)
			record := submissionFixture(t, fixture.spec)
			if _, err := fixture.repository.CreateSubmission(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			record = pendingSubmission(record)
			if err := fixture.repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
			fixture.start(t)
			snapshot, err := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
			if err != nil {
				t.Fatal(err)
			}
			progress := quicktest.PerformanceProgress{Phase: "sending", Planned: 2, Offered: 1, Launched: 1, InFlight: 1}
			if err := fixture.repository.RecordJobProgress(
				t.Context(),
				fixture.spec.JobID,
				progress,
				snapshot.UpdatedAt.Add(time.Second),
			); err != nil {
				t.Fatal(err)
			}
			view, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
			if err != nil || view.Progress == nil || view.Progress.Progress != progress {
				t.Fatalf("valid current progress was lost: %v", err)
			}
			mutation, value := `json_set(document_json, '$.payload.phase', ?)`, any("unknown")
			switch target {
			case "count":
				mutation, value = `json_set(document_json, '$.payload.planned', ?)`, uint64(1<<53)
			case "time":
				mutation, value = `json_set(document_json, '$.created_at', ?)`, repositoryEpoch.Format(time.RFC3339Nano)
			case "owner":
				mutation, value = `json_set(document_json, '$.job_id', ?)`, remoteOrigin.NodeID
			case "size":
				mutation, value = "?", `{"padding":"`+strings.Repeat("x", jobs.MaxProgressViewBytes)+`"}`
			}
			if _, err := fixture.db.ExecContext(
				t.Context(),
				"UPDATE execution_job_events SET document_json = "+mutation+
					" WHERE job_id = ? AND kind = 'progress'",
				value,
				fixture.spec.JobID,
			); err != nil {
				t.Fatal(err)
			}
			_, err = fixture.repository.Job(t.Context(), fixture.spec.JobID)
			if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt progress was projected: %v", err)
			}
		})
	}
}

func TestSubmittedQueriesRejectInvalidInputsAndPreserveCancellation(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	for _, ctx := range []context.Context{nil, t.Context()} {
		if _, err := fixture.repository.Job(ctx, "invalid"); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := fixture.repository.PerformanceResult(ctx, "invalid"); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := fixture.repository.Job(t.Context(), remoteOrigin.NodeID); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatal(err)
	}
	_, err := fixture.repository.PerformanceResult(t.Context(), fixture.spec.JobID)
	if !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := fixture.repository.Job(ctx, fixture.spec.JobID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = fixture.repository.PerformanceResult(ctx, fixture.spec.JobID)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSubmittedPerformanceQueryPreservesIncompleteAndReturnedObservations(t *testing.T) {
	for _, through := range []int{-1, 0, 6} {
		t.Run(map[int]string{-1: "not started", 0: "interrupted preparation", 6: "returned"}[through], func(t *testing.T) {
			fixture := newPerformanceStorageFixture(t)
			record := submissionFixture(t, fixture.spec)
			if _, err := fixture.repository.CreateSubmission(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			record = pendingSubmission(record)
			if err := fixture.repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
			_, err := fixture.repository.PerformanceResult(t.Context(), fixture.spec.JobID)
			if !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("unsealed result was readable: %v", err)
			}
			if through >= 0 {
				fixture.start(t)
				fixture.recordThrough(t, through)
			}
			terminal := fixture.change(t, domain.RunCancelled)
			sealed, err := fixture.repository.SealPerformanceResult(t.Context(), fixture.spec.JobID,
				terminal.UpdatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			before := fixture.requests.Load()
			view, err := fixture.repository.PerformanceResult(t.Context(), fixture.spec.JobID)
			if err != nil || view.ObservationState != sealed.Result.ObservationState ||
				view.ExecutionStarted != sealed.Result.ExecutionStarted || view.Terminal.Status != domain.RunCancelled {
				t.Fatalf("sealed execution facts were replaced: %v", err)
			}
			if through < 0 {
				if view.Observation != nil {
					t.Fatal("cancelled queued job acquired fabricated metrics")
				}
			} else {
				if view.Observation == nil || view.Observation.Stage != sealed.Result.Observation.Checkpoint.Stage ||
					view.Observation.Boundary != sealed.Result.Observation.Checkpoint.Boundary {
					t.Fatal("interrupted stage was presented as a completed measured report")
				}
				assertRoundTrip(
					t,
					"actual observation report",
					sealed.Result.Observation.Checkpoint.Report,
					view.Observation.Report,
				)
			}
			state, err := fixture.repository.Job(t.Context(), fixture.spec.JobID)
			if err != nil || !state.PerformanceResultReady || state.Submission.Delivery != jobs.SubmissionPending ||
				fixture.requests.Load() != before {
				t.Fatalf("query changed delivery or executed another request: %v", err)
			}
		})
	}
}

func TestSubmittedRemotePerformanceQueryRequiresVerifiedImport(t *testing.T) {
	for _, target := range []string{"valid", "marker", "missing document", "size", "digest",
		"foreign valid", "foreign marker", "foreign missing document", "foreign size", "foreign digest"} {
		t.Run(target, func(t *testing.T) {
			foreign := strings.HasPrefix(target, "foreign ")
			target = strings.TrimPrefix(target, "foreign ")
			fixture := newPerformanceStorageFixture(t)
			spec := remoteSpec(fixture.spec)
			node := openOperationalRepository(t)
			defer node.Close()
			initial, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec))
			if err != nil {
				t.Fatal(err)
			}
			terminal, err := node.ChangeJob(t.Context(), jobhost.Change{JobID: spec.JobID, ExpectedRevision: 1,
				Status: domain.RunCancelled, At: initial.UpdatedAt.Add(time.Second)})
			if err != nil {
				t.Fatal(err)
			}
			sealed, err := node.SealPerformanceResult(t.Context(), spec.JobID, terminal.UpdatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "remote-result-query.db")
			if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "query-test"}); err != nil {
				t.Fatal(err)
			}
			desktop := reopenHardeningRepository(t, path)
			defer desktop.Close()
			db := openDatabase(t, path)
			defer db.Close()
			if !foreign {
				record := submissionFixture(t, spec)
				if _, err := desktop.CreateSubmission(t.Context(), record); err != nil {
					t.Fatal(err)
				}
				record = pendingSubmission(record)
				if err := desktop.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
					t.Fatal(err)
				}
			}
			applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
			view, err := desktop.Job(t.Context(), spec.JobID)
			if err != nil || view.PerformanceResultReady || (view.Submission == nil) != foreign {
				t.Fatalf("reference alone became a downloaded result: %v", err)
			}
			if _, err := desktop.PerformanceResult(t.Context(), spec.JobID); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("not downloaded result was readable: %v", err)
			}
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
				Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventPerformanceResult, Document: sealed.Document,
			}); err != nil {
				t.Fatal(err)
			}
			if target == "valid" {
				result, err := desktop.PerformanceResult(t.Context(), spec.JobID)
				if err != nil || result.Observation != nil || result.ObservationState != jobcontract.PerformanceNotStarted {
					t.Fatalf("verified offline result not readable: %v", err)
				}
				view, err := desktop.Job(t.Context(), spec.JobID)
				if err != nil || !view.PerformanceResultReady || (view.Submission == nil) != foreign {
					t.Fatalf("verified result lost current shared ownership: %v", err)
				}
				return
			}
			statement := `UPDATE remote_job_report_refs SET imported = 0 WHERE job_id = ?`
			arguments := []any{spec.JobID}
			switch target {
			case "missing document":
				statement = `DELETE FROM remote_performance_results WHERE job_id = ?`
			case "size":
				statement = `UPDATE remote_performance_results SET document_json = '{}' WHERE job_id = ?`
			case "digest":
				changed := sealed.Result
				changed.SealedAt = changed.SealedAt.Add(time.Second)
				document, err := jobcontract.EncodePerformanceResult(changed)
				if err != nil || len(document) != len(sealed.Document) {
					t.Fatal("digest fixture must remain typed-valid with the same byte length")
				}
				statement = `UPDATE remote_performance_results SET document_json = ? WHERE job_id = ?`
				arguments = []any{document, spec.JobID}
			}
			if _, err := db.ExecContext(t.Context(), statement, arguments...); err != nil {
				t.Fatal(err)
			}
			if target == "digest" {
				_, err := desktop.Job(t.Context(), spec.JobID)
				if !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("unverified result was marked ready: %v", err)
				}
			}
			if _, err := desktop.PerformanceResult(t.Context(), spec.JobID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("unverified imported result was projected: %v", err)
			}
		})
	}
}
