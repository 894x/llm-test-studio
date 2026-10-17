package sqlite_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func submissionFixture(t *testing.T, spec jobcontract.JobSpec) jobs.SubmissionRecord {
	t.Helper()
	info := jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: remoteOrigin.NodeID,
		NodeInstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
		RunnerVersion: "independent-runner-build", OS: "linux", Arch: "amd64", Ready: true, Healthy: true,
		Capabilities: jobcontract.RunnerCapabilities{
			JobKinds:  []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindPerformance, jobcontract.KindComparison},
			Protocols: []string{"openai-chat"}, Features: []string{jobcontract.FeatureExecutionOrigin},
		},
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 2000,
			QueueTimeoutNS: int64(15 * time.Minute), ExecutionTimeoutNS: int64(time.Hour)},
	}
	configuration := ""
	if spec.Target.Kind == jobcontract.TargetRemote {
		configuration = remoteOrigin.ConfigurationID
	}
	record, err := jobs.NewSubmission(jobs.PreparedJob{ConfigurationID: configuration, Spec: spec, Runner: info},
		repositoryEpoch)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func pendingSubmission(record jobs.SubmissionRecord) jobs.SubmissionRecord {
	record.Revision++
	record.State, record.Observation = jobs.SubmissionPending, jobs.ObservationNone
	record.UpdatedAt = record.UpdatedAt.Add(time.Second)
	return record
}

func acceptedSubmission(t *testing.T, record jobs.SubmissionRecord) jobs.SubmissionRecord {
	t.Helper()
	snapshot, err := record.Prepared.Spec.InitialSnapshot(repositoryEpoch.Add(time.Second), executionOrigin(record.Prepared.Spec))
	if err != nil {
		t.Fatal(err)
	}
	record.Revision++
	record.State, record.Observation = jobs.SubmissionAccepted, jobs.ObservationNone
	record.Receipt, record.UpdatedAt = &snapshot.Receipt, record.UpdatedAt.Add(time.Second)
	return record
}

func TestSubmissionRecordsReopenAllKindsWithoutCreatingExecutionOwnership(t *testing.T) {
	performance, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(),
		quicktest.PerformanceCommand{Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test/v1", ModelID: "fixture", APIKey: "outbox-transient-fixture-secret",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 8, OutputTokens: 4})
	if code != "" {
		t.Fatal(code)
	}
	defer lease.Close()
	run := jobSpecForRun(t, runWithCompleteSnapshot(t))
	performanceSpec := run
	performanceSpec.Kind, performanceSpec.Spec = jobcontract.KindPerformance, jobcontract.Payload{Performance: &performance}
	for _, spec := range []jobcontract.JobSpec{run, remoteSpec(performanceSpec), remoteSpec(comparisonJobSpec(t))} {
		t.Run(string(spec.Kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "outbox.db")
			if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "outbox-test"}); err != nil {
				t.Fatal(err)
			}
			repository := reopenHardeningRepository(t, path)
			record := submissionFixture(t, spec)
			stored, err := repository.CreateSubmission(t.Context(), record)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(
				t,
				"prepared outbox",
				record,
				stored,
			)
			record = pendingSubmission(stored)
			if err := repository.UpdateSubmission(t.Context(), record, stored.Revision); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.GetJob(t.Context(), spec.JobID); !errors.Is(err, jobhost.ErrNotFound) {
				t.Fatal("outbox became an execution owner")
			}
			if err := repository.Close(); err != nil {
				t.Fatal(err)
			}
			repository = reopenHardeningRepository(t, path)
			defer repository.Close()
			page, err := repository.ListPendingSubmissions(t.Context(), "", 25)
			if err != nil || page.HasMore || len(page.Records) != 1 {
				t.Fatalf("pending discovery: %v", err)
			}
			assertRoundTrip(
				t,
				"pending outbox",
				record,
				page.Records[0],
			)
			accepted := acceptedSubmission(t, page.Records[0])
			if err := repository.UpdateSubmission(t.Context(), accepted, record.Revision); err != nil {
				t.Fatal(err)
			}
			record, err = repository.GetSubmission(t.Context(), spec.JobID)
			if err != nil {
				t.Fatal(err)
			}
			assertRoundTrip(
				t,
				"accepted outbox",
				accepted,
				record,
			)
			page, err = repository.ListPendingSubmissions(t.Context(), "", 25)
			if err != nil || page.Records == nil || len(page.Records) != 0 || page.HasMore {
				t.Fatal("accepted record remained pending")
			}
			db := openDatabase(t, path)
			defer db.Close()
			var document []byte
			if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&document); err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(document, []byte("outbox-transient-fixture-secret")) {
				t.Fatal("outbox persisted transport credentials")
			}
		})
	}
}

func TestSubmissionAdmissionAndCASUseOneFixedDestinationAcrossRepositories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "outbox-test"}); err != nil {
		t.Fatal(err)
	}
	first, second := reopenHardeningRepository(t, path), reopenHardeningRepository(t, path)
	defer first.Close()
	defer second.Close()
	record := submissionFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
	var workers sync.WaitGroup
	errorsFound := make(chan error, 2)
	for _, repository := range []*persistence.Repository{first, second} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := repository.CreateSubmission(t.Context(), record)
			errorsFound <- err
		}()
	}
	workers.Wait()
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
	pending := pendingSubmission(record)
	for _, repository := range []*persistence.Repository{first, second} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsFound <- repository.UpdateSubmission(t.Context(), pending, record.Revision)
		}()
	}
	workers.Wait()
	var won, conflicts int
	for range 2 {
		err := <-errorsFound
		if err == nil {
			won++
			continue
		}
		if errors.Is(err, jobs.ErrConflict) {
			conflicts++
			continue
		}
		t.Fatal(err)
	}
	if won != 1 || conflicts != 1 {
		t.Fatal("concurrent send intent did not have one winner")
	}
	duplicate := record
	duplicate.CreatedAt, duplicate.UpdatedAt = record.CreatedAt.Add(time.Hour), record.UpdatedAt.Add(time.Hour)
	actual, err := second.CreateSubmission(t.Context(), duplicate)
	if err != nil || actual.State != jobs.SubmissionPending || actual.Revision != pending.Revision {
		t.Fatal("repeated preparation reset the existing pending record")
	}
	duplicate.Prepared.ConfigurationID = "81000000-0000-4000-8000-000000000099"
	if _, err := second.CreateSubmission(t.Context(), duplicate); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("same task accepted a different configured destination")
	}
	duplicate = record
	duplicate.Prepared.Runner.RunnerVersion = "other-runner"
	if _, err := second.CreateSubmission(t.Context(), duplicate); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("same task accepted changed executor metadata")
	}
	for _, observation := range []jobs.Observation{jobs.ObservationUnreachable, jobs.ObservationNotFound,
		jobs.ObservationIdentityChanged, jobs.ObservationRejected} {
		next := pending
		next.Revision++
		next.Observation, next.UpdatedAt = observation, next.UpdatedAt.Add(time.Second)
		if err := first.UpdateSubmission(t.Context(), next, pending.Revision); err != nil {
			t.Fatal(err)
		}
		pending = next
		if pending.State != jobs.SubmissionPending || pending.Receipt != nil {
			t.Fatal("delivery observation became executor state or permission to resubmit")
		}
	}
	accepted := acceptedSubmission(t, pending)
	if err := first.UpdateSubmission(t.Context(), accepted, pending.Revision); err != nil {
		t.Fatal(err)
	}
	reopened := pendingSubmission(accepted)
	reopened.Receipt = nil
	if err := first.UpdateSubmission(t.Context(), reopened, accepted.Revision); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("accepted record returned to pending")
	}
}

func TestSubmissionPageBoundsCompleteFrozenDocumentsByBytes(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
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
		spec.JobID = fmt.Sprintf("72000000-0000-4000-8000-%012d", index+1)
		if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repository.ListPendingSubmissions(t.Context(), "", 25)
	if err != nil || len(page.Records) != 5 || !page.HasMore {
		t.Fatalf(
			"large document page count=%d more=%v error=%v",
			len(page.Records),
			page.HasMore,
			err,
		)
	}
	last, err := repository.ListPendingSubmissions(t.Context(), page.NextCursor, 25)
	if err != nil || len(last.Records) != 1 || last.HasMore {
		t.Fatal("byte-limited page lost the deferred record")
	}
}

func TestSubmissionRejectsInvalidChangesAndPreservesRowsOnWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "outbox-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	record, err := repository.CreateSubmission(t.Context(), submissionFixture(t,
		remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))))
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*jobs.SubmissionRecord){
		func(value *jobs.SubmissionRecord) { value.CreatedAt = value.CreatedAt.Add(time.Second) },
		func(value *jobs.SubmissionRecord) { value.Prepared.ConfigurationID = remoteOrigin.NodeID },
		func(value *jobs.SubmissionRecord) { value.Prepared.Runner.RunnerVersion = "changed" },
		func(value *jobs.SubmissionRecord) { value.UpdatedAt = value.CreatedAt.Add(-time.Second) },
		func(value *jobs.SubmissionRecord) { value.SpecSHA256 = strings.Repeat("b", 64) },
		func(value *jobs.SubmissionRecord) { value.Observation = "arbitrary-server-message" },
		func(value *jobs.SubmissionRecord) { value.Revision++ },
	} {
		next := pendingSubmission(record)
		change(&next)
		if err := repository.UpdateSubmission(t.Context(), next, record.Revision); err == nil {
			t.Fatal("changed frozen destination, content or invalid revision accepted")
		}
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER fail_submission_update
		BEFORE UPDATE ON job_submissions BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateSubmission(t.Context(), pendingSubmission(record), record.Revision); err == nil {
		t.Fatal("failed durable send intent was acknowledged")
	}
	actual, err := repository.GetSubmission(t.Context(), record.Prepared.Spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"failed update retained prepared record",
		record,
		actual,
	)
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER fail_submission_update`); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateSubmission(t.Context(), pendingSubmission(record), record.Revision); err != nil {
		t.Fatal(err)
	}
	actual, err = repository.GetSubmission(t.Context(), record.Prepared.Spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*jobcontract.Receipt){
		func(value *jobcontract.Receipt) { value.Origin.EngineBuildID = strings.Repeat("f", 64) },
		func(value *jobcontract.Receipt) { value.Origin.InstallationID = remoteOrigin.NodeID },
		func(value *jobcontract.Receipt) { value.RunIDs = []string{remoteOrigin.NodeID} },
	} {
		next := acceptedSubmission(t, actual)
		change(next.Receipt)
		if err := repository.UpdateSubmission(t.Context(), next, actual.Revision); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("mismatched confirmation accepted")
		}
	}
}

func TestSubmissionPaginationBoundsAndCorruptionDoNotRewriteRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "outbox.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "outbox-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
	for index := range 4 {
		spec.JobID = fmt.Sprintf("72000000-0000-4000-8000-%012d", index+1)
		if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
			t.Fatal(err)
		}
	}
	first, err := repository.ListPendingSubmissions(t.Context(), "", 2)
	if err != nil || !first.HasMore || len(first.Records) != 2 {
		t.Fatalf("first page: %v", err)
	}
	second, err := repository.ListPendingSubmissions(t.Context(), first.NextCursor, 2)
	if err != nil || second.HasMore || len(second.Records) != 2 || second.NextCursor <= first.NextCursor {
		t.Fatalf("second page: %v", err)
	}
	for _, input := range []struct {
		cursor string
		limit  uint32
	}{
		{cursor: "", limit: 0}, {cursor: "", limit: 26}, {cursor: "bad", limit: 1},
	} {
		if _, err := repository.ListPendingSubmissions(t.Context(), input.cursor, input.limit); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("unbounded or invalid submission query accepted")
		}
	}
	id := first.Records[0].Prepared.Spec.JobID
	var original []byte
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions WHERE job_id = ?`, id).
		Scan(&original); err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(original, &value); err != nil {
		t.Fatal(err)
	}
	value["obsolete_format"] = json.RawMessage(`1`)
	rejected, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE job_submissions SET document_json = ? WHERE job_id = ?`,
		rejected,
		id,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetSubmission(t.Context(), id); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("unknown stored field was accepted")
	}
	var retained []byte
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions WHERE job_id = ?`, id).
		Scan(&retained); err != nil || !bytes.Equal(retained, rejected) {
		t.Fatal("reading unsupported outbox changed its bytes")
	}
}
