package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func submittedPagesContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func newSubmittedPagesRepository(t *testing.T) (*persistence.Repository, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "submitted-pages.db")
	options := persistence.MigrateOptions{AppVersion: "submitted-pages"}
	if err := persistence.Migrate(t.Context(), path, options); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	t.Cleanup(func() { _ = repository.Close() })
	return repository, path
}

func submittedPageJobID(index int) string {
	return fmt.Sprintf("84000000-0000-4000-8000-%012d", index)
}

func TestSubmittedJobsPagesKeepDeliveryOriginAndExecutorAcrossReopen(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	empty, err := repository.Jobs(submittedPagesContext(t), "", 25)
	if err != nil || empty.SchemaVersion != 1 || empty.Jobs == nil || len(empty.Jobs) != 0 || empty.HasMore {
		t.Fatalf("empty submitted page is not a current non-null collection: %v", err)
	}
	records := map[string]jobs.SubmissionRecord{}
	for index := 27; index > 0; index-- {
		spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
		spec.JobID = submittedPageJobID(index)
		if index%3 == 0 {
			spec = remoteSpec(spec)
		}
		record := submissionFixture(t, spec)
		record.Prepared.Runner.RunnerVersion = fmt.Sprintf("frozen-runner-%02d", index)
		if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		if index%3 != 1 {
			record = pendingSubmission(record)
			if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
		}
		if index%3 == 0 {
			record = acceptedSubmission(t, record)
			if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
		}
		records[spec.JobID] = record
	}
	ctx := submittedPagesContext(t)
	first, err := repository.Jobs(ctx, "", 25)
	if err != nil || len(first.Jobs) != 25 || !first.HasMore || first.NextCursor != submittedPageJobID(25) {
		t.Fatalf("first submitted page lost its keyset bound: %v", err)
	}
	last, err := repository.Jobs(ctx, first.NextCursor, 25)
	if err != nil || len(last.Jobs) != 2 || last.HasMore || last.NextCursor != submittedPageJobID(27) {
		t.Fatalf("submitted tail omitted or repeated a record: %v", err)
	}
	all := append(first.Jobs, last.Jobs...)
	for index, view := range all {
		id := submittedPageJobID(index + 1)
		record := records[id]
		runner := record.Prepared.Runner
		expectedNode, expectedInstallation := runner.NodeID, runner.NodeInstallationID
		expectedVersion, expectedBuild := runner.RunnerVersion, runner.EngineBuildID
		if record.Receipt != nil {
			origin := record.Receipt.Origin
			expectedNode, expectedInstallation = origin.NodeID, origin.InstallationID
			expectedVersion, expectedBuild = origin.RunnerVersion, origin.EngineBuildID
		}
		matchingDelivery := view.JobID == id && view.Submission.Delivery == record.State && view.Submission.DeliveryRevision == record.Revision
		matchingLocation := view.Location.Kind == record.Prepared.Spec.Target.Kind &&
			view.Location.ConfigurationID == record.Prepared.ConfigurationID
		matchingExecutor := view.Executor.NodeID == expectedNode &&
			view.Executor.InstallationID == expectedInstallation
		matchingBuild := view.Executor.Version == expectedVersion && view.Executor.BuildID == expectedBuild
		if !matchingDelivery || !matchingLocation || !matchingExecutor || !matchingBuild {
			t.Fatalf("page changed frozen delivery, location or actual runner for %s", id)
		}
		if view.Execution != nil || view.Progress != nil || view.PerformanceResultReady {
			t.Fatalf("unsynchronized delivery %s manufactured execution", id)
		}
		if view.RunIDs == nil || view.ReportIDs == nil || len(view.RunIDs) != 1 {
			t.Fatalf("submitted %s lost its frozen child identity or collection shape", id)
		}
		if view.Submission.PreparedAt != record.CreatedAt.Format(time.RFC3339Nano) ||
			view.Submission.DeliveryUpdatedAt != record.UpdatedAt.Format(time.RFC3339Nano) {
			t.Fatalf("submitted %s lost delivery timestamps", id)
		}
		if record.Receipt == nil {
			if view.Submission.Confirmation != nil {
				t.Fatalf("unconfirmed delivery %s acquired an acknowledgement", id)
			}
			continue
		}
		if view.Submission.Confirmation == nil || view.Submission.Confirmation.Status != record.Receipt.Status ||
			view.Submission.Confirmation.AcceptedAt != record.Receipt.AcceptedAt.Format(time.RFC3339Nano) {
			t.Fatalf("remote acknowledgement for %s was lost before synchronization", id)
		}
	}
	tail, err := repository.Jobs(ctx, last.NextCursor, 25)
	if err != nil || tail.Jobs == nil || len(tail.Jobs) != 0 || tail.HasMore || tail.NextCursor != last.NextCursor {
		t.Fatalf("exhausted submitted cursor is not a stable empty page: %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	read, err := reopened.Jobs(ctx, "", 25)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"reopened submitted page",
		first,
		read,
	)
}

func TestSubmittedJobsPagesBudgetCompleteFrozenInputBeforeProjection(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	definition, err := json.Marshal(map[string]any{
		"request": map[string]any{"body": map[string]any{"messages": []map[string]any{
			{"role": "user", "content": strings.Repeat("p", 3<<20)},
		}}}, "inputs": map[string]any{}, "assertions": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec.Spec.Run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = definition
	for index := range 6 {
		spec.JobID = submittedPageJobID(index + 1)
		if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := submittedPagesContext(t)
	first, err := repository.Jobs(ctx, "", 25)
	if err != nil || len(first.Jobs) != 5 || !first.HasMore || first.NextCursor != submittedPageJobID(5) {
		t.Fatalf("16 MiB frozen-input budget did not stop before the sixth small DTO: %v", err)
	}
	last, err := repository.Jobs(ctx, first.NextCursor, 25)
	if err != nil || len(last.Jobs) != 1 || last.HasMore || last.Jobs[0].JobID != submittedPageJobID(6) {
		t.Fatalf("byte-limited submitted page lost its deferred record: %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	var pageBytes, totalBytes int64
	if err := db.QueryRowContext(ctx, `SELECT
		SUM(CASE WHEN job_id <= ? THEN length(CAST(document_json AS BLOB)) ELSE 0 END),
		SUM(length(CAST(document_json AS BLOB))) FROM job_submissions`, first.NextCursor).
		Scan(&pageBytes, &totalBytes); err != nil {
		t.Fatal(err)
	}
	if pageBytes > jobs.MaxSubmissionPageBytes || totalBytes <= jobs.MaxSubmissionPageBytes {
		t.Fatal("large-input fixture did not straddle the full document budget")
	}
}

func TestSubmittedJobControlsPagesKeepAllHistoryAndIndependentExecution(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	fixture.confirm(t)
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		next, err := fixture.executor.ChangeJob(t.Context(), jobhost.Change{
			JobID: fixture.spec.JobID, ExpectedRevision: fixture.snapshot.Receipt.Revision,
			Status: status, At: fixture.snapshot.UpdatedAt.Add(time.Second),
		})
		if err != nil {
			t.Fatal(err)
		}
		fixture.snapshot = next
	}
	otherSpec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	otherSpec.JobID = submittedPageJobID(99)
	other := submissionFixture(t, otherSpec)
	if _, err := fixture.repository.CreateSubmission(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	other = pendingSubmission(other)
	if err := fixture.repository.UpdateSubmission(t.Context(), other, other.Revision-1); err != nil {
		t.Fatal(err)
	}
	expected := map[string]jobs.ControlRecord{}
	for index := 1; index <= 27; index++ {
		foreign := controlFixture(
			t,
			fixture.repository,
			otherSpec.JobID,
			index*2-1,
		)
		if _, err := fixture.repository.CreateControl(t.Context(), foreign); err != nil {
			t.Fatal(err)
		}
		record := controlFixture(
			t,
			fixture.repository,
			fixture.spec.JobID,
			index*2,
		)
		if index%3 == 1 {
			record.Command.Kind = jobcontract.CommandStopSending
			record.Command.ExpectedRevision = fixture.snapshot.Receipt.Revision
		}
		if _, err := fixture.repository.CreateControl(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		switch index % 3 {
		case 1:
			confirmation, _, err := fixture.executor.ApplyJobCommand(
				t.Context(),
				record.Command,
				record.CreatedAt.Add(time.Duration(index)*time.Second),
			)
			if err != nil {
				t.Fatal(err)
			}
			fixture.snapshot = confirmation.Snapshot
			record.Revision++
			record.State, record.Confirmation = jobs.ControlConfirmed, &confirmation
			record.UpdatedAt = confirmation.AcceptedAt.Add(time.Second)
		case 2:
			_, _, err := fixture.executor.ApplyJobCommand(t.Context(), record.Command, record.CreatedAt)
			if !errors.Is(err, jobhost.ErrConflict) {
				t.Fatalf("rejected fixture did not represent an actual revision conflict: %v", err)
			}
			record.Revision++
			record.State, record.Observation = jobs.ControlRejected, jobs.ObservationRejected
			record.UpdatedAt = record.UpdatedAt.Add(time.Second)
		}
		if record.Revision > 1 {
			if err := fixture.repository.UpdateControl(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
		}
		expected[record.Command.CommandID] = record
	}
	completed, err := fixture.executor.ChangeJob(t.Context(), jobhost.Change{
		JobID: fixture.spec.JobID, ExpectedRevision: fixture.snapshot.Receipt.Revision,
		Status: domain.RunCompleted, At: fixture.snapshot.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := submittedPagesContext(t)
	first, err := fixture.repository.JobControls(ctx, fixture.spec.JobID, "", 25)
	if err != nil || len(first.Commands) != 25 || !first.HasMore || first.JobID != fixture.spec.JobID {
		t.Fatalf("control history lost its per-job bound: %v", err)
	}
	last, err := fixture.repository.JobControls(ctx, fixture.spec.JobID, first.NextCursor, 25)
	if err != nil || len(last.Commands) != 2 || last.HasMore {
		t.Fatalf("control history tail omitted final or pending instructions: %v", err)
	}
	for index, view := range append(first.Commands, last.Commands...) {
		id := controlFixture(
			t,
			fixture.repository,
			fixture.spec.JobID,
			(index+1)*2,
		).Command.CommandID
		record := expected[id]
		matchingIdentity := view.SchemaVersion == 1 && view.CommandID == id && view.JobID == fixture.spec.JobID
		matchingDelivery := view.State == record.State && view.Observation == record.Observation &&
			view.DeliveryRevision == record.Revision && view.ExpectedRevision == record.Command.ExpectedRevision
		if !matchingIdentity || !matchingDelivery || view.Kind != record.Command.Kind {
			t.Fatalf("history mixed jobs, skipped an instruction or replaced its state at %s", id)
		}
		if view.CreatedAt != record.CreatedAt.Format(time.RFC3339Nano) ||
			view.UpdatedAt != record.UpdatedAt.Format(time.RFC3339Nano) {
			t.Fatalf("history lost public string timestamps at %s", id)
		}
		if record.Confirmation == nil {
			if view.ConfirmedAt != nil || view.Execution != nil {
				t.Fatalf("unconfirmed instruction %s acquired execution facts", id)
			}
			continue
		}
		confirmation := record.Confirmation
		if view.ConfirmedAt == nil || *view.ConfirmedAt != confirmation.AcceptedAt.Format(time.RFC3339Nano) {
			t.Fatalf("history lost exact confirmation time at %s", id)
		}
		if view.Execution == nil || view.Execution.Status != domain.RunDraining ||
			view.Execution.Revision != confirmation.Snapshot.Receipt.Revision ||
			view.Execution.LatestSequence != confirmation.Snapshot.LatestSequence {
			t.Fatalf("historical confirmation %s was overwritten by current completion", id)
		}
	}
	if first.NextCursor != controlFixture(
		t,
		fixture.repository,
		fixture.spec.JobID,
		50,
	).Command.CommandID ||
		last.NextCursor != controlFixture(
			t,
			fixture.repository,
			fixture.spec.JobID,
			54,
		).Command.CommandID {
		t.Fatal("control history cursor did not retain the last returned command")
	}
	tail, err := fixture.repository.JobControls(ctx, fixture.spec.JobID, last.NextCursor, 25)
	emptyTail := tail.Commands != nil && len(tail.Commands) == 0 && !tail.HasMore
	if err != nil || !emptyTail || tail.NextCursor != last.NextCursor {
		t.Fatalf("exhausted control history did not return a stable non-null collection: %v", err)
	}
	foreignPage, err := fixture.repository.JobControls(ctx, otherSpec.JobID, "", 25)
	if err != nil || len(foreignPage.Commands) != 25 || !foreignPage.HasMore {
		t.Fatalf("other job's history was lost: %v", err)
	}
	for _, view := range foreignPage.Commands {
		if view.JobID != otherSpec.JobID || view.State != jobs.ControlPending {
			t.Fatal("control history leaked another job's instructions")
		}
	}
	current, err := fixture.repository.Job(ctx, fixture.spec.JobID)
	if err != nil || current.Execution == nil || current.Execution.Status != domain.RunCompleted {
		t.Fatalf("reading historical controls replaced current job execution: %v", err)
	}
	stored, err := fixture.repository.GetJob(ctx, fixture.spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"history read preserves execution",
		completed,
		stored,
	)
	var path string
	row := fixture.db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name = 'main'`)
	if err := row.Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	read, err := reopened.JobControls(ctx, fixture.spec.JobID, "", 25)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"reopened control history",
		first,
		read,
	)
}

func TestSubmittedPagesValidateInputsAndPreserveCancellation(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
		t.Fatal(err)
	}
	ctx := submittedPagesContext(t)
	empty, err := repository.JobControls(ctx, spec.JobID, "", 25)
	if err != nil || empty.SchemaVersion != 1 || empty.JobID != spec.JobID || empty.Commands == nil ||
		len(empty.Commands) != 0 || empty.HasMore {
		t.Fatalf("job without controls did not return a current non-null collection: %v", err)
	}
	for _, input := range []struct {
		name  string
		ctx   context.Context
		after string
		limit uint32
	}{
		{name: "nil context", ctx: nil, limit: 1},
		{name: "invalid cursor", ctx: ctx, after: "invalid", limit: 1},
		{name: "zero limit", ctx: ctx, limit: 0},
		{name: "excess limit", ctx: ctx, limit: 26},
	} {
		t.Run(input.name, func(t *testing.T) {
			if _, err := repository.Jobs(input.ctx, input.after, input.limit); !errors.Is(err, jobs.ErrInvalid) {
				t.Fatalf("invalid submitted page query accepted: %v", err)
			}
			_, err := repository.JobControls(input.ctx, spec.JobID, input.after, input.limit)
			if !errors.Is(err, jobs.ErrInvalid) {
				t.Fatalf("invalid control history query accepted: %v", err)
			}
		})
	}
	for _, id := range []string{"", "invalid"} {
		if _, err := repository.JobControls(ctx, id, "", 25); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatalf("invalid history owner accepted: %v", err)
		}
	}
	if _, err := repository.JobControls(ctx, submittedPageJobID(99), "", 25); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("missing history owner was confused with an empty history: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := repository.Jobs(cancelled, "", 25); !errors.Is(err, context.Canceled) {
		t.Fatalf("submitted page lost cancellation: %v", err)
	}
	if _, err := repository.JobControls(cancelled, spec.JobID, "", 25); !errors.Is(err, context.Canceled) {
		t.Fatalf("control history lost cancellation: %v", err)
	}
}

func TestSubmittedPagesRejectOversizedDocumentsWithoutMutation(t *testing.T) {
	for _, target := range []string{"first submission", "lookahead submission", "history owner", "control"} {
		t.Run(target, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
			spec.JobID = submittedPageJobID(1)
			record := submissionFixture(t, spec)
			if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			record = pendingSubmission(record)
			if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
				t.Fatal(err)
			}
			id := spec.JobID
			if target == "lookahead submission" {
				spec.JobID = submittedPageJobID(2)
				if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
					t.Fatal(err)
				}
				id = spec.JobID
			}
			write := `UPDATE job_submissions SET document_json = ? WHERE job_id = ?`
			read := `SELECT document_json FROM job_submissions WHERE job_id = ?`
			size := jobcontract.MaxSubmissionBytes + jobcontract.MaxRunnerInfoBytes + jobcontract.MaxStateBytes + 4097
			if target == "control" {
				control := controlFixture(
					t,
					repository,
					spec.JobID,
					1,
				)
				if _, err := repository.CreateControl(t.Context(), control); err != nil {
					t.Fatal(err)
				}
				id, size = control.Command.CommandID, jobs.MaxControlRecordBytes+1
				write = `UPDATE job_control_outbox SET document_json = ? WHERE command_id = ?`
				read = `SELECT document_json FROM job_control_outbox WHERE command_id = ?`
			}
			document := []byte(`{"padding":"` + strings.Repeat("x", size) + `"}`)
			db := openDatabase(t, path)
			defer db.Close()
			ctx := submittedPagesContext(t)
			if _, err := db.ExecContext(ctx, write, document, id); err != nil {
				t.Fatal(err)
			}
			var err error
			if target == "control" || target == "history owner" {
				_, err = repository.JobControls(ctx, record.Prepared.Spec.JobID, "", 1)
			} else {
				_, err = repository.Jobs(ctx, "", 1)
				if err != nil && !strings.Contains(err.Error(), "job page identity or byte limit") {
					t.Fatalf("oversized input reached projection instead of the bounded ID scan: %v", err)
				}
			}
			if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("oversized stored document was projected: %v", err)
			}
			var retained []byte
			if err := db.QueryRowContext(ctx, read, id).Scan(&retained); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(retained, document) {
				t.Fatal("rejecting an oversized page changed stored bytes")
			}
		})
	}
}
