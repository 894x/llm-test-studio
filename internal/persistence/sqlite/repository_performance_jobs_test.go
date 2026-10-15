package sqlite_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type performanceStorageFixture struct {
	repository  *persistence.Repository
	db          *sql.DB
	path        string
	spec        jobcontract.JobSpec
	checkpoints []quicktest.PerformanceCheckpoint
	requests    atomic.Int64
}

func newPerformanceStorageFixture(t *testing.T) *performanceStorageFixture {
	t.Helper()
	fixture := &performanceStorageFixture{checkpoints: []quicktest.PerformanceCheckpoint{}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.requests.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n")
		fmt.Fprint(writer, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		t.Context(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: server.URL, ModelID: "performance-model", APIKey: "performance-storage-test-secret",
			RequestCount: 2, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
			WarmupRequests: 1, SliceDurationMS: 10,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	host := quicktest.NewHost(quicktest.HostDependencies{Transport: server.Client().Transport})
	report, err := host.ExecutePrepared(
		t.Context(),
		prepared,
		lease,
		quicktest.ExecutionOptions{
			OnCheckpoint: func(ctx context.Context, checkpoint quicktest.PerformanceCheckpoint) error {
				fixture.checkpoints = append(fixture.checkpoints, checkpoint)
				return nil
			},
		},
	)
	if err != nil || !report.Success || len(fixture.checkpoints) != 7 {
		t.Fatalf(
			"real Core fixture: success=%v checkpoints=%d error=%v",
			report.Success,
			len(fixture.checkpoints),
			err,
		)
	}
	fixture.path = filepath.Join(t.TempDir(), "performance-jobs.db")
	if err := persistence.Migrate(
		t.Context(),
		fixture.path,
		persistence.MigrateOptions{AppVersion: "performance-job-test"},
	); err != nil {
		t.Fatal(err)
	}
	fixture.repository = reopenHardeningRepository(t, fixture.path)
	t.Cleanup(func() { _ = fixture.repository.Close() })
	fixture.db = openDatabase(t, fixture.path)
	t.Cleanup(func() { _ = fixture.db.Close() })
	fixture.spec = jobSpecForRun(t, runWithCompleteSnapshot(t))
	fixture.spec.Kind = jobcontract.KindPerformance
	fixture.spec.Spec = jobcontract.Payload{Performance: &prepared}
	if _, _, err := fixture.repository.AcceptJob(t.Context(), jobAcceptance(fixture.spec)); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *performanceStorageFixture) change(t *testing.T, status domain.RunStatus) jobcontract.Snapshot {
	t.Helper()
	previous, err := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := fixture.repository.ChangeJob(t.Context(), jobhost.Change{
		JobID: fixture.spec.JobID, ExpectedRevision: previous.Receipt.Revision,
		Status: status, At: previous.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return next
}

func (fixture *performanceStorageFixture) start(t *testing.T) {
	t.Helper()
	fixture.change(t, domain.RunStarting)
	fixture.change(t, domain.RunRunning)
}

func (fixture *performanceStorageFixture) input(index int) jobhost.PerformanceObservationWrite {
	return jobhost.PerformanceObservationWrite{
		JobID: fixture.spec.JobID, ExpectedRevision: uint64(index),
		At:         jobAcceptance(fixture.spec).AcceptedAt.Add(time.Duration(index+10) * time.Second),
		Checkpoint: fixture.checkpoints[index],
	}
}

func (fixture *performanceStorageFixture) recordThrough(t *testing.T, index int) jobcontract.PerformanceObservation {
	t.Helper()
	var latest jobcontract.PerformanceObservation
	for next := 0; next <= index; next++ {
		var err error
		latest, err = fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(next))
		if err != nil {
			t.Fatalf("record checkpoint %d: %v", next, err)
		}
	}
	return latest
}

func TestPerformanceObservationsPersistRealCoreFactsWithIdempotentCAS(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	first, err := fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(0))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	retry := fixture.input(0)
	retry.At = retry.At.Add(time.Hour)
	confirmed, err := fixture.repository.RecordPerformanceObservation(t.Context(), retry)
	if err != nil || !reflect.DeepEqual(first, confirmed) {
		t.Fatalf("lost-ack retry changed the observation: %v", err)
	}
	for _, input := range []jobhost.PerformanceObservationWrite{
		{JobID: fixture.spec.JobID, ExpectedRevision: 0, At: retry.At, Checkpoint: fixture.checkpoints[1]},
		{JobID: fixture.spec.JobID, ExpectedRevision: 9, At: retry.At, Checkpoint: fixture.checkpoints[1]},
		{JobID: fixture.spec.JobID, ExpectedRevision: 1, At: retry.At, Checkpoint: fixture.checkpoints[3]},
	} {
		if _, err := fixture.repository.RecordPerformanceObservation(
			t.Context(),
			input,
		); !errors.Is(err, jobhost.ErrConflict) {
			t.Fatalf("conflicting or skipped observation accepted: %v", err)
		}
	}
	after, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("retry or rejected observation advanced the job journal")
	}
	for index := 1; index < len(fixture.checkpoints); index++ {
		if _, err := fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(index)); err != nil {
			t.Fatalf("checkpoint %d: %v", index, err)
		}
	}
	latest, err := fixture.repository.GetPerformanceObservation(t.Context(), fixture.spec.JobID)
	if err != nil || latest.Revision != 7 || len(latest.Checkpoint.Report.Samples) != 2 {
		t.Fatalf("actual final observation was lost: revision=%d error=%v", latest.Revision, err)
	}
	if _, err := fixture.repository.RecordPerformanceObservation(
		t.Context(),
		fixture.input(0),
	); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatal("an old revision overwrote the latest observation")
	}
	page, err := fixture.repository.JobEvents(
		t.Context(),
		fixture.spec.JobID,
		0,
		200,
	)
	if err != nil || len(page.Events) != 10 {
		t.Fatalf("checkpoint journal: events=%d error=%v", len(page.Events), err)
	}
	for _, event := range page.Events[3:] {
		if event.Kind != jobcontract.EventPerformanceCheckpoint || bytes.Contains(event.Payload, []byte("samples")) {
			t.Fatal("checkpoint event contains a full report or promises historical document access")
		}
	}
}

func TestPerformanceObservationAndResultWritesRollBackWithTheirEvents(t *testing.T) {
	for _, target := range []string{"observation", "observation-event", "result", "result-event"} {
		t.Run(target, func(t *testing.T) {
			fixture := newPerformanceStorageFixture(t)
			fixture.start(t)
			isResult := target == "result" || target == "result-event"
			if isResult {
				fixture.recordThrough(t, 6)
				fixture.change(t, domain.RunCompleted)
			}
			before, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
			ddl := `CREATE TRIGGER performance_write_fault BEFORE INSERT ON execution_performance_observations
				BEGIN SELECT RAISE(ABORT, 'controlled observation write failure'); END`
			switch target {
			case "observation-event":
				ddl = `CREATE TRIGGER performance_write_fault BEFORE INSERT ON execution_job_events
					WHEN NEW.kind = 'performance_checkpoint' BEGIN SELECT RAISE(ABORT, 'controlled event failure'); END`
			case "result":
				ddl = `CREATE TRIGGER performance_write_fault BEFORE INSERT ON execution_performance_results
					BEGIN SELECT RAISE(ABORT, 'controlled result failure'); END`
			case "result-event":
				ddl = `CREATE TRIGGER performance_write_fault BEFORE INSERT ON execution_job_events
					WHEN NEW.kind = 'performance_result' BEGIN SELECT RAISE(ABORT, 'controlled event failure'); END`
			}
			if _, err := fixture.db.ExecContext(t.Context(), ddl); err != nil {
				t.Fatal(err)
			}
			var writeErr error
			if isResult {
				_, writeErr = fixture.repository.SealPerformanceResult(
					t.Context(),
					fixture.spec.JobID,
					before.UpdatedAt.Add(time.Second),
				)
			} else {
				_, writeErr = fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(0))
			}
			if writeErr == nil {
				t.Fatal("injected persistence fault was ignored")
			}
			after, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rolled-back fact advanced sequence, time or lifecycle revision")
			}
			if isResult {
				_, err := fixture.repository.ReadPerformanceResult(t.Context(), fixture.spec.JobID)
				if !errors.Is(err, jobhost.ErrNotFound) {
					t.Fatalf("rolled-back result remains readable: %v", err)
				}
			} else if _, err := fixture.repository.GetPerformanceObservation(
				t.Context(),
				fixture.spec.JobID,
			); !errors.Is(err, jobhost.ErrNotFound) {
				t.Fatalf("rolled-back observation remains readable: %v", err)
			}
			if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER performance_write_fault`); err != nil {
				t.Fatal(err)
			}
			if isResult {
				_, writeErr = fixture.repository.SealPerformanceResult(
					t.Context(),
					fixture.spec.JobID,
					before.UpdatedAt.Add(time.Second),
				)
			} else {
				_, writeErr = fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(0))
			}
			if writeErr != nil {
				t.Fatalf("same operation could not retry after rollback: %v", writeErr)
			}
		})
	}
}

func TestPerformanceResultRecoveryPreservesActualObservationAndDoesNotExecute(t *testing.T) {
	for _, test := range []struct {
		name    string
		started bool
		through int
		state   jobcontract.PerformanceObservationState
	}{
		{name: "queued cancel", through: -1, state: jobcontract.PerformanceNotStarted},
		{name: "started without observation", started: true, through: -1, state: jobcontract.PerformanceInterrupted},
		{name: "preparation interrupted", started: true, through: 0, state: jobcontract.PerformanceInterrupted},
		{name: "measured phase entered", started: true, through: 4, state: jobcontract.PerformanceInterrupted},
		{name: "measured phase completed", started: true, through: 5, state: jobcontract.PerformanceInterrupted},
		{name: "final observation returned", started: true, through: 6, state: jobcontract.PerformanceAvailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPerformanceStorageFixture(t)
			if test.started {
				fixture.start(t)
			}
			if test.through >= 0 {
				fixture.recordThrough(t, test.through)
			}
			terminal := fixture.change(t, domain.RunCancelled)
			requestsBefore := fixture.requests.Load()
			if err := fixture.repository.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.repository = reopenHardeningRepository(t, fixture.path)
			sealed, err := fixture.repository.SealPerformanceResult(
				t.Context(),
				fixture.spec.JobID,
				terminal.UpdatedAt.Add(time.Second),
			)
			if err != nil || sealed.Result.ObservationState != test.state || sealed.Result.ExecutionStarted != test.started {
				t.Fatalf(
					"recovered observation state=%s started=%v error=%v",
					sealed.Result.ObservationState,
					sealed.Result.ExecutionStarted,
					err,
				)
			}
			if sealed.Result.Terminal.Receipt.Status != domain.RunCancelled || fixture.requests.Load() != requestsBefore {
				t.Fatal("recovery changed the terminal winner or sent another HTTP request")
			}
			if test.through >= 0 && !reflect.DeepEqual(sealed.Result.Observation.Checkpoint, fixture.checkpoints[test.through]) {
				t.Fatal("recovery replaced the actual stage observation")
			}
			digest := sha256.Sum256(sealed.Document)
			if sealed.Reference.SHA256 != hex.EncodeToString(digest[:]) || sealed.Reference.Size != int64(len(sealed.Document)) {
				t.Fatal("result reference does not describe the stored download bytes")
			}
			retried, err := fixture.repository.SealPerformanceResult(
				t.Context(),
				fixture.spec.JobID,
				terminal.UpdatedAt.Add(time.Hour),
			)
			if err != nil || !bytes.Equal(sealed.Document, retried.Document) || sealed.Reference != retried.Reference {
				t.Fatalf("result retry changed its timestamp, bytes or digest: %v", err)
			}
			if err := fixture.repository.MarkJobSettled(t.Context(), fixture.spec.JobID); err != nil {
				t.Fatal(err)
			}
			read, err := fixture.repository.ReadPerformanceResult(t.Context(), fixture.spec.JobID)
			if err != nil || !bytes.Equal(read.Document, sealed.Document) {
				t.Fatalf("settled result cannot be read: %v", err)
			}
			page, err := fixture.repository.JobEvents(
				t.Context(),
				fixture.spec.JobID,
				0,
				200,
			)
			if err != nil {
				t.Fatal(err)
			}
			resultEvents := 0
			for _, event := range page.Events {
				if event.Kind == jobcontract.EventPerformanceResult {
					resultEvents++
				}
			}
			if resultEvents != 1 {
				t.Fatal("result recovery appended duplicate seal events")
			}
		})
	}
}

func TestPerformanceLateObservationRespectsTerminalWinnerAndResultSeal(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	fixture.recordThrough(t, 4)
	fixture.change(t, domain.RunCancelled)
	if _, err := fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(5)); err != nil {
		t.Fatalf("terminal command prevented the final in-flight observation: %v", err)
	}
	current, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if _, err := fixture.repository.SealPerformanceResult(
		t.Context(),
		fixture.spec.JobID,
		current.UpdatedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.RecordPerformanceObservation(
		t.Context(),
		fixture.input(6),
	); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("sealed result accepted a later observation: %v", err)
	}
	current, _ = fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if current.Receipt.Status != domain.RunCancelled {
		t.Fatal("late facts changed the winning cancellation")
	}
}

func TestPerformanceObservationAndResultReadsRejectCorruption(t *testing.T) {
	for _, target := range []string{"observation revision", "observation format", "result digest", "result identity"} {
		t.Run(target, func(t *testing.T) {
			fixture := newPerformanceStorageFixture(t)
			fixture.start(t)
			fixture.recordThrough(t, 6)
			terminal := fixture.change(t, domain.RunCompleted)
			if _, err := fixture.repository.SealPerformanceResult(
				t.Context(),
				fixture.spec.JobID,
				terminal.UpdatedAt.Add(time.Second),
			); err != nil {
				t.Fatal(err)
			}
			query := `UPDATE execution_performance_observations SET revision = revision + 1 WHERE job_id = ?`
			switch target {
			case "observation format":
				query = `UPDATE execution_performance_observations
					SET document_json = json_set(document_json, '$.contract', 0) WHERE job_id = ?`
			case "result digest":
				query = `UPDATE execution_performance_results SET sha256 = 'wrong' WHERE job_id = ?`
			case "result identity":
				query = `UPDATE execution_performance_results
					SET report_id = '73000000-0000-4000-8000-000000000001' WHERE job_id = ?`
			}
			if _, err := fixture.db.ExecContext(t.Context(), query, fixture.spec.JobID); err != nil {
				t.Fatal(err)
			}
			var err error
			if target == "observation revision" || target == "observation format" {
				_, err = fixture.repository.GetPerformanceObservation(t.Context(), fixture.spec.JobID)
			} else {
				_, err = fixture.repository.ReadPerformanceResult(t.Context(), fixture.spec.JobID)
			}
			if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt row accepted: %v", err)
			}
		})
	}
}

func TestPerformanceObservationCASAcrossTwoRepositories(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	other := reopenHardeningRepository(t, fixture.path)
	defer other.Close()
	for _, conflicting := range []bool{false, true} {
		first := fixture.input(0)
		second := first
		if conflicting {
			first = fixture.input(1)
			second = first
			second.Checkpoint.Boundary = quicktest.PerformanceBoundaryReturned
			second.Checkpoint.Report.ErrorCode = "request_failed"
		}
		inputs := []jobhost.PerformanceObservationWrite{first, second}
		repositories := []*persistence.Repository{fixture.repository, other}
		ready := make(chan struct{})
		results := make(chan error, 2)
		var group sync.WaitGroup
		for index, repository := range repositories {
			group.Add(1)
			go func() {
				defer group.Done()
				<-ready
				_, err := repository.RecordPerformanceObservation(t.Context(), inputs[index])
				results <- err
			}()
		}
		close(ready)
		group.Wait()
		close(results)
		conflicts := 0
		for err := range results {
			if errors.Is(err, jobhost.ErrConflict) {
				conflicts++
			} else if err != nil {
				t.Fatal(err)
			}
		}
		if conflicting != (conflicts == 1) {
			t.Fatalf("concurrent observation decisions: conflicting=%v conflicts=%d", conflicting, conflicts)
		}
		latest, err := fixture.repository.GetPerformanceObservation(t.Context(), fixture.spec.JobID)
		if err != nil || latest.Revision != first.ExpectedRevision+1 {
			t.Fatalf("CAS revision advanced twice: %v", err)
		}
	}
	page, err := fixture.repository.JobEvents(
		t.Context(),
		fixture.spec.JobID,
		0,
		200,
	)
	if err != nil || len(page.Events) != 5 {
		t.Fatalf("concurrent retries duplicated checkpoint events: count=%d error=%v", len(page.Events), err)
	}
}

func TestPerformanceObservationUpdateAndEventFailuresRollBackTogether(t *testing.T) {
	for _, eventFault := range []bool{false, true} {
		fixture := newPerformanceStorageFixture(t)
		fixture.start(t)
		first := fixture.recordThrough(t, 0)
		before, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
		ddl := `CREATE TRIGGER performance_update_fault BEFORE UPDATE ON execution_performance_observations
			BEGIN SELECT RAISE(ABORT, 'controlled observation update failure'); END`
		if eventFault {
			ddl = `CREATE TRIGGER performance_update_fault BEFORE INSERT ON execution_job_events
				WHEN NEW.kind = 'performance_checkpoint' BEGIN SELECT RAISE(ABORT, 'controlled event failure'); END`
		}
		if _, err := fixture.db.ExecContext(t.Context(), ddl); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(1)); err == nil {
			t.Fatal("update fault was ignored")
		}
		after, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
		observation, err := fixture.repository.GetPerformanceObservation(t.Context(), fixture.spec.JobID)
		if err != nil || !reflect.DeepEqual(first, observation) || !reflect.DeepEqual(before, after) {
			t.Fatalf("failed update changed facts or journal: %v", err)
		}
		if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER performance_update_fault`); err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.repository.RecordPerformanceObservation(t.Context(), fixture.input(1)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPerformanceObservationOwnsInputAndReturnedMutableValues(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	fixture.recordThrough(t, 4)
	input := fixture.input(5)
	stored, err := fixture.repository.RecordPerformanceObservation(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	input.Checkpoint.Report.Samples[0].PromptTokens = 999
	input.Checkpoint.Report.Warmup.Completed = 999
	stored.Checkpoint.Report.Samples[0].PromptTokens = 888
	read, err := fixture.repository.GetPerformanceObservation(t.Context(), fixture.spec.JobID)
	if err != nil || read.Checkpoint.Report.Samples[0].PromptTokens != 10 || read.Checkpoint.Report.Warmup.Completed != 1 {
		t.Fatalf("caller mutation changed durable facts: %v", err)
	}
}

func TestPerformanceObservationCorruptTimeIsRejectedWithoutMutationOnEveryPath(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	fixture.recordThrough(t, 0)
	fixture.change(t, domain.RunCancelled)
	corruptTime := jobAcceptance(fixture.spec).AcceptedAt.Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := fixture.db.ExecContext(
		t.Context(),
		`UPDATE execution_performance_observations
		SET recorded_at = ?, document_json = json_set(document_json, '$.recorded_at', ?) WHERE job_id = ?`,
		corruptTime,
		corruptTime,
		fixture.spec.JobID,
	); err != nil {
		t.Fatal(err)
	}
	var beforeDocument, afterDocument []byte
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM execution_performance_observations
		WHERE job_id = ?`, fixture.spec.JobID).Scan(&beforeDocument); err != nil {
		t.Fatal(err)
	}
	before, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if _, err := fixture.repository.GetPerformanceObservation(
		t.Context(),
		fixture.spec.JobID,
	); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("getter accepted corrupt observation time: %v", err)
	}
	for _, input := range []jobhost.PerformanceObservationWrite{fixture.input(0), fixture.input(1)} {
		if _, err := fixture.repository.RecordPerformanceObservation(
			t.Context(),
			input,
		); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("retry or update accepted corrupt observation time: %v", err)
		}
	}
	if _, err := fixture.repository.SealPerformanceResult(
		t.Context(),
		fixture.spec.JobID,
		before.UpdatedAt.Add(time.Second),
	); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("seal misclassified corrupt observation time: %v", err)
	}
	after, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM execution_performance_observations
		WHERE job_id = ?`, fixture.spec.JobID).Scan(&afterDocument); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeDocument, afterDocument) || !reflect.DeepEqual(before, after) {
		t.Fatal("rejected corruption was silently repaired or advanced the job journal")
	}
}

func TestPerformanceResultDoesNotClaimUnstartedWhenStartJournalIsMissing(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	terminal := fixture.change(t, domain.RunCancelled)
	if _, err := fixture.db.ExecContext(t.Context(), `DELETE FROM execution_job_events
		WHERE job_id = ? AND kind = 'state' AND json_extract(document_json, '$.payload.receipt.status') = 'starting'`,
		fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.SealPerformanceResult(
		t.Context(),
		fixture.spec.JobID,
		terminal.UpdatedAt.Add(time.Second),
	); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("missing start journal was interpreted as no traffic: %v", err)
	}
}

func TestPerformanceResultCannotSealAnActiveOrUnobservedCompletedJob(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	fixture.start(t)
	current, _ := fixture.repository.GetJob(t.Context(), fixture.spec.JobID)
	if _, err := fixture.repository.SealPerformanceResult(
		t.Context(),
		fixture.spec.JobID,
		current.UpdatedAt.Add(time.Second),
	); !errors.Is(err, jobhost.ErrConflict) {
		t.Fatalf("active execution was sealed: %v", err)
	}
	terminal := fixture.change(t, domain.RunCompleted)
	if _, err := fixture.repository.SealPerformanceResult(
		t.Context(),
		fixture.spec.JobID,
		terminal.UpdatedAt.Add(time.Second),
	); !errors.Is(err, jobhost.ErrInvalid) {
		t.Fatalf("completion without a final observation was sealed: %v", err)
	}
}
