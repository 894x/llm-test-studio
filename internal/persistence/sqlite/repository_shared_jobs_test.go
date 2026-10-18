package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func newForeignJobFixture(t *testing.T, spec jobcontract.JobSpec) submittedReadFixture {
	t.Helper()
	desktop, path := newSubmittedPagesRepository(t)
	node := openOperationalRepository(t)
	t.Cleanup(func() { _ = node.Close() })
	db := openDatabase(t, path)
	t.Cleanup(func() { _ = db.Close() })
	accepted, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec))
	if err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	return submittedReadFixture{repository: desktop, executor: node, db: db, spec: spec, snapshot: accepted}
}

func TestSharedJobQueriesExposeRealForeignRunAndComparisonWithoutSubmission(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison} {
		t.Run(string(kind), func(t *testing.T) {
			spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
			if kind == jobcontract.KindComparison {
				spec = remoteSpec(comparisonJobSpec(t))
			}
			fixture := newForeignJobFixture(t, spec)
			read, err := fixture.repository.ReadJob(t.Context(), spec.JobID)
			if err != nil || read.Submission != nil || read.Execution == nil || read.Validate() != nil {
				t.Fatalf("foreign source lacks actual execution or invents a submission: %v", err)
			}
			view, err := fixture.repository.Job(t.Context(), spec.JobID)
			if err != nil || view.Submission != nil || view.Execution == nil || view.Kind != kind {
				t.Fatalf("foreign job was hidden or converted into a local delivery: %v", err)
			}
			if len(view.RunIDs) != len(spec.PreparedRuns()) || view.Location.ConfigurationID != remoteOrigin.ConfigurationID ||
				view.Executor.BuildID != fixture.snapshot.Receipt.Origin.EngineBuildID {
				t.Fatal("foreign projection lost its children, binding or actual executor")
			}
			document, err := json.Marshal(view)
			if err != nil || !bytes.Contains(document, []byte(`"submission":null`)) {
				t.Fatal("foreign submission must be explicitly null")
			}
			controls, err := fixture.repository.JobControls(t.Context(), spec.JobID, submittedPageJobID(9), 25)
			if err != nil || controls.Commands == nil || len(controls.Commands) != 0 || controls.HasMore ||
				controls.NextCursor != submittedPageJobID(9) {
				t.Fatalf("foreign control history must be a real empty current page: %v", err)
			}
			if _, err := fixture.repository.GetSubmission(t.Context(), spec.JobID); !errors.Is(err, jobs.ErrNotFound) {
				t.Fatalf("read fabricated an outbox submission: %v", err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := fixture.repository.ReadJob(ctx, spec.JobID); !errors.Is(err, context.Canceled) {
				t.Fatalf("foreign source read ignored cancellation: %v", err)
			}
		})
	}
}

func TestSharedJobPagesUnionDeduplicatesAndResumesAcrossSources(t *testing.T) {
	desktop, path := newSubmittedPagesRepository(t)
	node := openOperationalRepository(t)
	defer node.Close()
	for index := 27; index > 0; index-- {
		spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
		spec.JobID, spec.Spec.Run.Meta.ID = submittedPageJobID(index), fmt.Sprintf("85000000-0000-4000-8000-%012d", index)
		if index%3 != 0 {
			record := submissionFixture(t, spec)
			if _, err := desktop.CreateSubmission(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			if index%3 == 2 {
				record = pendingSubmission(record)
				if err := desktop.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
					t.Fatal(err)
				}
			}
		}
		if index%3 != 1 {
			if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
				t.Fatal(err)
			}
			applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
		}
	}
	first, err := desktop.Jobs(t.Context(), "", 25)
	if err != nil || len(first.Jobs) != 25 || !first.HasMore || first.NextCursor != submittedPageJobID(25) {
		t.Fatalf("union did not apply a shared UUID keyset bound: %v", err)
	}
	last, err := desktop.Jobs(t.Context(), first.NextCursor, 25)
	if err != nil || len(last.Jobs) != 2 || last.HasMore || last.NextCursor != submittedPageJobID(27) {
		t.Fatalf("shared tail skipped or repeated a job: %v", err)
	}
	for index, view := range append(first.Jobs, last.Jobs...) {
		if view.JobID != submittedPageJobID(index+1) || (view.Submission == nil) != ((index+1)%3 == 0) {
			t.Fatal("union deduplication changed ownership or ordering")
		}
	}
	if err := desktop.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	read, err := reopened.Jobs(t.Context(), "", 25)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"shared union survives reopen",
		first,
		read,
	)
}

func TestSharedControlFreezesForeignTargetAndRetainsRealMirrorFK(t *testing.T) {
	fixture := newForeignJobFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
	record := controlFixture(
		t,
		fixture.repository,
		fixture.spec.JobID,
		1,
	)
	if record.Target.Acceptance == nil || record.Target.Acceptance.Origin != fixture.snapshot.Receipt.Origin {
		t.Fatal("foreign control did not freeze actual acceptance")
	}
	stored, err := fixture.repository.CreateControl(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"foreign pending command",
		record,
		stored,
	)
	var submissionID, mirrorID *string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT submission_job_id, mirror_job_id
		FROM job_control_targets WHERE command_id = ?`, record.Command.CommandID).Scan(&submissionID, &mirrorID); err != nil {
		t.Fatal(err)
	}
	if submissionID != nil || mirrorID == nil || *mirrorID != fixture.spec.JobID {
		t.Fatal("foreign control anchor fabricated a submission source")
	}
	assertRemoteNodeUsage(t, fixture.repository, true)
	connection, err := fixture.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(t.Context(), `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := connection.ExecContext(t.Context(), `DELETE FROM job_control_targets WHERE command_id = ?`,
		record.Command.CommandID); err == nil {
		t.Fatal("outbox FK allowed deleting its immutable target anchor")
	}
	if _, err := connection.ExecContext(t.Context(), `DELETE FROM remote_job_mirrors WHERE job_id = ?`, fixture.spec.JobID); err == nil {
		t.Fatal("real mirror FK allowed deleting a controlled execution")
	}
	confirmation, _, err := fixture.executor.ApplyJobCommand(t.Context(), record.Command, record.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	next := record
	next.Revision++
	next.State, next.Confirmation, next.UpdatedAt = jobs.ControlConfirmed, &confirmation, record.UpdatedAt.Add(time.Second)
	if err := fixture.repository.UpdateControl(t.Context(), next, record.Revision); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, fixture.repository, remoteEvents(t, fixture.executor, fixture.spec))
	replayed, err := fixture.repository.CreateControl(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"original confirmed foreign command",
		next,
		replayed,
	)
	page, err := fixture.repository.JobControls(t.Context(), fixture.spec.JobID, "", 25)
	if err != nil || len(page.Commands) != 1 || page.Commands[0].State != jobs.ControlConfirmed {
		t.Fatalf("shared control history did not retain this client's durable record: %v", err)
	}
}

func TestSharedControlCreationRevalidatesSourcesAndRollsBackAnchor(t *testing.T) {
	for _, mutation := range []string{"scope", "digest", "acceptance", "null acceptance", "source changed",
		"source removed", "outbox failure"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newForeignJobFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			record := controlFixture(
				t,
				fixture.repository,
				fixture.spec.JobID,
				1,
			)
			switch mutation {
			case "scope":
				record.Target.Location.ConfigurationID = remoteOrigin.NodeID
			case "digest":
				record.Target.SpecSHA256 = strings.Repeat("c", 64)
			case "acceptance":
				copy := *record.Target.Acceptance
				copy.AcceptedAt = copy.AcceptedAt.Add(time.Second)
				record.Target.Acceptance = &copy
			case "null acceptance":
				record.Target.Acceptance = nil
			case "source changed":
				if _, err := fixture.db.ExecContext(t.Context(), `UPDATE remote_job_mirrors SET configuration_id = ?`,
					remoteOrigin.NodeID); err != nil {
					t.Fatal(err)
				}
			case "source removed":
				if _, err := fixture.db.ExecContext(t.Context(), `DELETE FROM remote_job_mirrors`); err != nil {
					t.Fatal(err)
				}
			case "outbox failure":
				// A deterministic late SQL failure proves the first write rolls back.
				if _, err := fixture.db.ExecContext(t.Context(), `CREATE TRIGGER test_reject_control
					BEFORE INSERT ON job_control_outbox BEGIN SELECT RAISE(ABORT, 'test failure'); END`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.repository.CreateControl(t.Context(), record); err == nil {
				t.Fatal("control was committed without an unchanged real source and full atomic write")
			}
			for _, table := range []string{"job_control_targets", "job_control_outbox", "job_submissions"} {
				if got := queryInt(t, fixture.db, "SELECT COUNT(*) FROM "+table); got != 0 {
					t.Fatalf("failed control leaked %s rows: %d", table, got)
				}
			}
		})
	}
}

func TestSharedJobPageAdvancesWhenValidSourcesTogetherExceedPageBudget(t *testing.T) {
	desktop, path := newSubmittedPagesRepository(t)
	node := openOperationalRepository(t)
	defer node.Close()
	spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	spec.JobID = submittedPageJobID(1)
	entry := &spec.Spec.Run.Snapshot.Entries[0]
	second := entry.CaseDefinitions[0]
	second.ID, second.Key = "85000000-0000-4000-8000-000000000002", "T-SNAPSHOT-SECOND"
	entry.CaseDefinitions = append(entry.CaseDefinitions, second)
	entry.Cases = append(entry.Cases, domain.CaseRevisionRef{CaseID: second.ID, Revision: second.Revision})
	entry.Suite.Cases = append(entry.Suite.Cases, domain.CaseRef{CaseID: second.ID})
	entry.CaseInputs[second.ID] = map[string]json.RawMessage{}
	setPadding := func(size int) {
		for index := range entry.CaseDefinitions {
			count := size / 2
			if index == 1 {
				count = size - count
			}
			entry.CaseDefinitions[index].Definitions = domain.ProtocolDefinitions{
				domain.ProtocolOpenAIChat: canonicalFixtureJSON(t, map[string]any{
					"request": map[string]any{"body": map[string]any{"messages": []map[string]any{
						{"role": "user", "content": strings.Repeat("p", count)},
					}}}, "inputs": map[string]any{}, "assertions": []any{},
				}),
			}
		}
	}
	setPadding(0)
	base, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	setPadding(jobcontract.MaxSubmissionBytes - len(base) - 128)
	if err := spec.Validate(); err != nil {
		t.Fatalf("maximum fixture spec: %v", err)
	}
	if _, err := spec.Digest(); err != nil {
		t.Fatalf("maximum fixture digest: %v", err)
	}
	record := submissionFixture(t, spec)
	if _, err := desktop.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	record = pendingSubmission(record)
	if err := desktop.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	next := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	next.JobID = submittedPageJobID(2)
	if _, err := desktop.CreateSubmission(t.Context(), submissionFixture(t, next)); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	var bytesRead int64
	if err := db.QueryRowContext(t.Context(), `SELECT length(CAST(submission.document_json AS BLOB)) +
		length(CAST(mirror.spec_json AS BLOB)) + length(CAST(mirror.snapshot_json AS BLOB))
		FROM job_submissions submission JOIN remote_job_mirrors mirror USING(job_id) WHERE job_id = ?`, spec.JobID).
		Scan(&bytesRead); err != nil {
		t.Fatal(err)
	}
	if bytesRead <= jobs.MaxSubmissionPageBytes {
		t.Fatal("fixture does not exceed the combined page budget")
	}
	first, err := desktop.Jobs(t.Context(), "", 25)
	if err != nil || len(first.Jobs) != 1 || !first.HasMore || first.NextCursor != spec.JobID {
		t.Fatalf("valid maximum job caused an empty/nonadvancing page: %v", err)
	}
	last, err := desktop.Jobs(t.Context(), first.NextCursor, 25)
	if err != nil || len(last.Jobs) != 1 || last.HasMore || last.NextCursor != next.JobID {
		t.Fatalf("maximum job prevented the following job from being reached: %v", err)
	}
}

func TestSharedJobPageBudgetsPerformanceResultBeforeProjection(t *testing.T) {
	desktop, path := newSubmittedPagesRepository(t)
	first := jobSpecForRun(t, runWithCompleteSnapshot(t))
	first.JobID = submittedPageJobID(1)
	if _, err := desktop.CreateSubmission(t.Context(), submissionFixture(t, first)); err != nil {
		t.Fatal(err)
	}
	core := newPerformanceStorageFixture(t)
	spec := remoteSpec(core.spec)
	spec.JobID = submittedPageJobID(2)
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
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
		Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventPerformanceResult, Document: sealed.Document,
	}); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	// This corrupt lookahead remains individually byte-bounded. The first page
	// must account for its cost before attempting any typed result projection.
	large := `{"padding":"` + strings.Repeat("p", jobs.MaxSubmissionPageBytes) + `"}`
	if _, err := db.ExecContext(t.Context(), `UPDATE remote_performance_results SET document_json = ?`, large); err != nil {
		t.Fatal(err)
	}
	page, err := desktop.Jobs(t.Context(), "", 25)
	if err != nil || len(page.Jobs) != 1 || !page.HasMore || page.NextCursor != first.JobID {
		t.Fatalf("page decoded a large result outside its byte budget: %v", err)
	}
	if _, err := desktop.Jobs(t.Context(), page.NextCursor, 25); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("deferred corrupt result was accepted on its own page: %v", err)
	}
	var retained string
	if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_performance_results`).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if retained != large {
		t.Fatal("byte budgeting or rejection changed the deferred result")
	}
}

func TestSharedJobRejectsConflictingSubmissionAndMirrorWithoutMutation(t *testing.T) {
	for _, mutation := range []string{"scope", "spec", "acceptance", "namespace"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newSubmittedReadFixture(t, true)
			switch mutation {
			case "scope":
				record := fixture.record
				record.Prepared.ConfigurationID = remoteOrigin.NodeID
				if _, err := fixture.db.ExecContext(t.Context(), `UPDATE job_submissions
					SET configuration_id = ?, document_json = ?`, record.Prepared.ConfigurationID, canonicalFixtureJSON(t, record)); err != nil {
					t.Fatal(err)
				}
			case "spec":
				record := fixture.record
				record.Prepared.Spec.Spec.Run.Snapshot.Entries[0].CaseDefinitions[0].Name = "changed frozen input"
				var err error
				record.SpecSHA256, err = record.Prepared.Spec.Digest()
				if err != nil || record.Validate() != nil {
					t.Fatal("spec conflict fixture is not independently valid")
				}
				if _, err := fixture.db.ExecContext(t.Context(), `UPDATE job_submissions SET spec_sha256 = ?, document_json = ?`,
					record.SpecSHA256, canonicalFixtureJSON(t, record)); err != nil {
					t.Fatal(err)
				}
			case "acceptance":
				fixture.confirm(t)
				record := fixture.record
				copy := *record.Receipt
				copy.AcceptedAt = copy.AcceptedAt.Add(time.Second)
				record.Receipt = &copy
				if record.Validate() != nil {
					t.Fatal("acceptance conflict fixture is not independently valid")
				}
				if _, err := fixture.db.ExecContext(t.Context(), `UPDATE job_submissions SET document_json = ?`,
					canonicalFixtureJSON(t, record)); err != nil {
					t.Fatal(err)
				}
			case "namespace":
				if _, err := fixture.db.ExecContext(t.Context(), `INSERT INTO execution_jobs
					(id, installation_id, engine_build_id, kind, revision, status, accepted_at, spec_sha256,
					secret_commitment, spec_json, snapshot_json, last_sequence)
					SELECT job_id, installation_id, json_extract(spec_json, '$.engine_build_id'),
					json_extract(spec_json, '$.kind'), json_extract(snapshot_json, '$.receipt.revision'),
					json_extract(snapshot_json, '$.receipt.status'), json_extract(snapshot_json, '$.receipt.accepted_at'),
					spec_sha256, ?, spec_json, snapshot_json, applied_sequence FROM remote_job_mirrors`, strings.Repeat("b", 64)); err != nil {
					t.Fatal(err)
				}
			}
			var before string
			if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.repository.ReadJob(t.Context(), fixture.spec.JobID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("conflicting shared source was resolved: %v", err)
			}
			if _, err := fixture.repository.Job(t.Context(), fixture.spec.JobID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("conflicting shared source was projected: %v", err)
			}
			if _, err := fixture.repository.Jobs(t.Context(), "", 25); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("deduplication hid a conflicting source: %v", err)
			}
			if _, err := fixture.repository.JobControls(t.Context(), fixture.spec.JobID, "", 25); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("control history skipped real source validation: %v", err)
			}
			var after string
			if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("rejected source was rewritten")
			}
		})
	}
}

func TestSharedControlsKeepEarlierUnknownAcceptanceAndFreezeLaterCommands(t *testing.T) {
	desktop, _ := newSubmittedPagesRepository(t)
	spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	submission := submissionFixture(t, spec)
	if _, err := desktop.CreateSubmission(t.Context(), submission); err != nil {
		t.Fatal(err)
	}
	submission = pendingSubmission(submission)
	if err := desktop.UpdateSubmission(t.Context(), submission, submission.Revision-1); err != nil {
		t.Fatal(err)
	}
	first := controlFixture(
		t,
		desktop,
		spec.JobID,
		1,
	)
	if first.Target.Acceptance != nil {
		t.Fatal("unconfirmed own pending command fabricated acceptance")
	}
	if _, err := desktop.CreateControl(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	node := openOperationalRepository(t)
	defer node.Close()
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	second := controlFixture(
		t,
		desktop,
		spec.JobID,
		2,
	)
	if second.Target.Acceptance == nil {
		t.Fatal("later command omitted now-known actual acceptance")
	}
	if _, err := desktop.CreateControl(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	proposal := second
	proposal.Command = first.Command
	replayed, err := desktop.CreateControl(t.Context(), proposal)
	if err != nil || replayed.Target.Acceptance != nil {
		t.Fatalf("admission rewrote or invalidated the original immutable pending target: %v", err)
	}
	assertRoundTrip(
		t,
		"unknown acceptance stays frozen",
		first,
		replayed,
	)
	changed := first
	changed.Revision++
	changed.Target = second.Target
	if err := desktop.UpdateControl(t.Context(), changed, first.Revision); !errors.Is(err, jobs.ErrConflict) {
		t.Fatalf("CAS changed the original target: %v", err)
	}
}

func TestSharedControlRejectsMissingOrChangedAnchorWithoutMutation(t *testing.T) {
	for _, mutation := range []string{"missing", "digest", "acceptance", "source", "old record"} {
		t.Run(mutation, func(t *testing.T) {
			fixture := newForeignJobFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			record := controlFixture(
				t,
				fixture.repository,
				fixture.spec.JobID,
				1,
			)
			if _, err := fixture.repository.CreateControl(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			statement := `UPDATE job_control_targets SET document_json = json_set(document_json, '$.spec_sha256', ?)`
			arguments := []any{strings.Repeat("c", 64)}
			switch mutation {
			case "missing":
				if _, err := fixture.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
					t.Fatal(err)
				}
				statement, arguments = `DELETE FROM job_control_targets`, []any{}
			case "acceptance":
				statement, arguments = `UPDATE job_control_targets SET document_json = json_set(document_json, '$.acceptance', NULL)`, []any{}
			case "source":
				if _, err := fixture.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
					t.Fatal(err)
				}
				statement = `UPDATE job_control_targets SET mirror_job_id = NULL, submission_job_id = job_id`
				arguments = []any{}
			case "old record":
				statement = `UPDATE job_control_outbox SET document_json = json_remove(document_json, '$.target')`
				arguments = []any{}
			}
			if _, err := fixture.db.ExecContext(t.Context(), statement, arguments...); err != nil {
				t.Fatal(err)
			}
			var before string
			if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM job_control_outbox`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.repository.GetControl(t.Context(), record.Command.CommandID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("unanchored control accepted: %v", err)
			}
			if _, err := fixture.repository.ListPendingControls(t.Context(), "", 25); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("unanchored control replay accepted: %v", err)
			}
			if _, err := fixture.repository.JobControls(t.Context(), fixture.spec.JobID, "", 25); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("unanchored history accepted: %v", err)
			}
			var after string
			if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM job_control_outbox`).Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatal("rejected control was converted or repaired")
			}
		})
	}
}

func TestSharedJobCurrentSchemaRejectsTamperedLedgerWithoutMutation(t *testing.T) {
	version, name, checksum := persistence.CurrentSchemaIdentity()
	if version != 3 || name != "0003_query_indexes" || checksum != "ceca1e8965986f9722ba7d8ace907a98d316b849c311fa1066f260963055eafb" {
		t.Fatalf("unexpected shared job schema identity: %d %s %s", version, name, checksum)
	}
	for _, operation := range []string{"migrate", "inspect", "repository"} {
		t.Run(operation, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "previous-shared-control.db")
			if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "current"}); err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t, path)
			if _, err := db.ExecContext(t.Context(), `UPDATE schema_migrations SET checksum = ?`,
				"adc400effb974273c9d7d18dc8cc7f2deb7623cd958c7f80d61b8010dca4be6e"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			before := directoryHashes(t, directory)
			var err error
			switch operation {
			case "migrate":
				err = persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "new"})
			case "inspect":
				_, err = persistence.SchemaVersion(t.Context(), path)
			case "repository":
				_, err = persistence.OpenRepository(t.Context(), path, persistence.RepositoryOptions{})
			}
			if !errors.Is(err, persistence.ErrSchemaResetRequired) || !strings.Contains(err.Error(), "explicit upgrade") {
				t.Fatalf("previous schema was accepted without an actionable upgrade error: %v", err)
			}
			if !reflect.DeepEqual(before, directoryHashes(t, directory)) {
				t.Fatal("rejecting the previous checksum mutated database files")
			}
		})
	}
}
