package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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

func documentWorkQuery(class remoteresults.DocumentClass) remoteresults.DocumentQuery {
	return remoteresults.DocumentQuery{Origin: remoteOrigin, Class: class, Limit: 1}
}

func addDocumentWorkReference(t *testing.T, db *sql.DB, spec jobcontract.JobSpec, index int) string {
	t.Helper()
	id := fmt.Sprintf("86000000-0000-4000-8000-%012d", index)
	reference := jobcontract.ReportReference{ID: id, Kind: jobcontract.KindRun,
		RunID: spec.Spec.Run.Meta.ID, SHA256: strings.Repeat("e", 64), Size: 128}
	document, _ := json.Marshal(reference)
	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO remote_job_report_refs(job_id,reference_type,report_id,document_json) VALUES(?,'report',?,?)`,
		spec.JobID,
		id,
		document,
	); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRemoteDocumentWorkCompositeCursorAndDurableFocus(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	db := openDatabase(t, path)
	defer db.Close()
	foreign := priorityMirror(t, repository, 1)
	first := addDocumentWorkReference(
		t,
		db,
		foreign,
		1,
	)
	second := addDocumentWorkReference(
		t,
		db,
		foreign,
		2,
	)
	own := priorityMirror(t, repository, 2)
	storePrioritySubmission(
		t,
		repository,
		submissionFixture(t, own),
		"accepted",
	)
	addDocumentWorkReference(
		t,
		db,
		own,
		3,
	)
	query := documentWorkQuery(remoteresults.DocumentHistory)
	page, err := repository.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || len(page.Items) != 1 || !page.HasMore || page.NextCursor.ReportID != first {
		t.Fatalf("first foreign reference: %+v, %v", page, err)
	}
	query.After = page.NextCursor
	page, err = repository.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || len(page.Items) != 1 || page.HasMore || page.NextCursor.JobID != foreign.JobID ||
		page.NextCursor.ReportID != second {
		t.Fatalf("same-job reference was skipped: %+v, %v", page, err)
	}
	query.After = page.NextCursor
	empty, err := repository.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.HasMore || empty.NextCursor != query.After {
		t.Fatalf("empty document tail changed cursor: %+v, %v", empty, err)
	}
	focus, err := repository.ListRemoteDocumentWork(t.Context(), documentWorkQuery(remoteresults.DocumentFocus))
	if err != nil || len(focus.Items) != 1 || focus.Items[0].JobID != own.JobID {
		t.Fatalf("own submission did not reserve focus: %+v, %v", focus, err)
	}
	// A real durable control created while history is in flight changes class.
	control := controlFixture(
		t,
		repository,
		foreign.JobID,
		1,
	)
	if _, err := repository.CreateControl(t.Context(), control); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateControl(t.Context(), controlFixture(
		t,
		repository,
		foreign.JobID,
		2,
	)); err != nil {
		t.Fatal(err)
	}
	history, err := repository.ListRemoteDocumentWork(t.Context(), documentWorkQuery(remoteresults.DocumentHistory))
	if err != nil || len(history.Items) != 0 {
		t.Fatalf("controlled foreign reference remained history: %+v, %v", history, err)
	}
	focusQuery := documentWorkQuery(remoteresults.DocumentFocus)
	focusQuery.Limit = 25
	focus, err = repository.ListRemoteDocumentWork(t.Context(), focusQuery)
	if err != nil || len(focus.Items) != 3 {
		t.Fatalf("control anchors duplicated references or did not grant focus: %+v, %v", focus, err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	after, err := reopened.ListRemoteDocumentWork(t.Context(), focusQuery)
	if err != nil || !slices.EqualFunc(focus.Items, after.Items, func(a, b remoteresults.DocumentWork) bool {
		return a.JobID == b.JobID && a.Document == b.Document
	}) {
		t.Fatal("document work did not survive reopening")
	}
}

func TestRemoteDocumentWorkControlRecordsValidateEveryPersistedBinding(t *testing.T) {
	for _, mutation := range []string{"valid_confirmed", "valid_rejected", "target_origin", "target_digest", "target_null",
		"record_state", "record_command", "source", "outbox_job", "confirmation"} {
		t.Run(mutation, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			spec := priorityMirror(t, repository, 1)
			record := controlFixture(
				t,
				repository,
				spec.JobID,
				1,
			)
			if _, err := repository.CreateControl(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			db := openDatabase(t, path)
			defer db.Close()
			addDocumentWorkReference(
				t,
				db,
				spec,
				1,
			)
			if mutation == "valid_confirmed" || mutation == "confirmation" {
				terminalPriorityMirror(t, repository, spec)
				mirror, err := repository.GetRemoteMirror(t.Context(), spec.JobID)
				if err != nil {
					t.Fatal(err)
				}
				record.Revision, record.State = 2, jobs.ControlConfirmed
				record.UpdatedAt = record.UpdatedAt.Add(time.Second)
				record.Confirmation = &jobcontract.CommandConfirmation{
					Command: record.Command, AcceptedAt: mirror.Snapshot.UpdatedAt, Snapshot: mirror.Snapshot,
				}
				if err := repository.UpdateControl(t.Context(), record, 1); err != nil {
					t.Fatal(err)
				}
			} else if mutation == "valid_rejected" {
				record.Revision, record.State, record.Observation = 2, jobs.ControlRejected, jobs.ObservationRejected
				record.UpdatedAt = record.UpdatedAt.Add(time.Second)
				if err := repository.UpdateControl(t.Context(), record, 1); err != nil {
					t.Fatal(err)
				}
			}
			var query string
			switch mutation {
			case "target_origin":
				query = `UPDATE job_control_targets SET document_json=json_set(document_json,
					'$.location.configuration_id','81000000-0000-4000-8000-000000000999')`
			case "target_digest":
				query = `UPDATE job_control_targets SET document_json=json_set(document_json,
					'$.spec_sha256','` + strings.Repeat("f", 64) + `')`
			case "target_null":
				query = `UPDATE job_control_targets SET document_json=json_set(document_json,'$.acceptance',NULL)`
			case "record_state":
				query = `UPDATE job_control_outbox SET document_json=json_set(document_json,'$.state','rejected')`
			case "record_command":
				query = `UPDATE job_control_outbox SET document_json=json_set(document_json,
					'$.command.job_id','84000000-0000-4000-8000-000000000999')`
			case "source":
				_, _ = db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints=ON`)
				query = `UPDATE job_control_targets SET mirror_job_id=NULL`
			case "outbox_job":
				query = `UPDATE job_control_outbox SET job_id='84000000-0000-4000-8000-000000000999'`
			case "confirmation":
				query = `UPDATE job_control_outbox SET document_json=json_set(document_json,
					'$.confirmation.snapshot.receipt.origin.runner_version','conflicting')`
			}
			if query != "" {
				if _, err := db.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			page, err := repository.ListRemoteDocumentWork(t.Context(), documentWorkQuery(remoteresults.DocumentFocus))
			if strings.HasPrefix(mutation, "valid_") {
				if err != nil || len(page.Items) != 1 {
					t.Fatalf("valid durable control lost interest: %+v, %v", page, err)
				}
			} else if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt interest was trusted: %+v, %v", page, err)
			}
		})
	}
}

func TestRemoteDocumentWorkRejectsInvalidQueryAndCancelsWithoutConnectionLeak(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	db := openDatabase(t, path)
	defer db.Close()
	spec := priorityMirror(t, repository, 1)
	addDocumentWorkReference(
		t,
		db,
		spec,
		1,
	)
	valid := documentWorkQuery(remoteresults.DocumentHistory)
	for _, change := range []func(*remoteresults.DocumentQuery){
		func(q *remoteresults.DocumentQuery) { q.Class = "other" },
		func(q *remoteresults.DocumentQuery) { q.Limit = 0 },
		func(q *remoteresults.DocumentQuery) { q.Limit = 26 },
		func(q *remoteresults.DocumentQuery) { q.Origin = remoteresults.Origin{} },
		func(q *remoteresults.DocumentQuery) { q.After.JobID = spec.JobID },
	} {
		query := valid
		change(&query)
		if _, err := repository.ListRemoteDocumentWork(t.Context(), query); !errors.Is(err, remoteresults.ErrInvalid) {
			t.Fatal("invalid document query accepted")
		}
	}
	if _, err := repository.ListRemoteDocumentWork(nil, valid); !errors.Is(err, remoteresults.ErrInvalid) {
		t.Fatal("nil document context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListRemoteDocumentWork(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("document cancellation lost its classification: %v", err)
	}
	for range 3 {
		ctx, done := context.WithTimeout(t.Context(), time.Second)
		page, err := repository.ListRemoteDocumentWork(ctx, valid)
		done()
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("document query leaked its sole connection: %v", err)
		}
	}
}

func TestRemoteDocumentWorkArchiveWaitsForActualSealedResultImport(t *testing.T) {
	core := newPerformanceStorageFixture(t)
	node := openOperationalRepository(t)
	defer node.Close()
	spec := remoteSpec(core.spec)
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		previous, err := node.GetJob(t.Context(), spec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := node.ChangeJob(t.Context(), jobhost.Change{
			JobID: spec.JobID, ExpectedRevision: previous.Receipt.Revision,
			Status: status, At: previous.UpdatedAt.Add(time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for index := range core.checkpoints {
		if _, err := node.RecordPerformanceObservation(t.Context(), core.input(index)); err != nil {
			t.Fatal(err)
		}
	}
	report := core.checkpoints[len(core.checkpoints)-1].Report
	previous, err := node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.RecordJobProgress(
		t.Context(),
		spec.JobID,
		report.Progress,
		previous.UpdatedAt.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	previous, err = node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := node.ChangeJob(t.Context(), jobhost.Change{
		JobID: spec.JobID, ExpectedRevision: previous.Receipt.Revision,
		Status: domain.RunCompleted, At: previous.UpdatedAt.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := node.SealPerformanceResult(t.Context(), spec.JobID, terminal.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	report, eligible, err := sealed.Result.StatisticalArchive()
	if err != nil || !eligible {
		t.Fatal("fixture did not produce an eligible sealed archive")
	}
	if err := node.SaveQuickPerformanceReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	archive, err := node.ReadPerformanceArchive(t.Context(), spec.JobID, report.ReportID)
	if err != nil {
		t.Fatal(err)
	}
	desktop, path := newSubmittedPagesRepository(t)
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	query := documentWorkQuery(remoteresults.DocumentHistory)
	query.Limit = 25
	page, err := desktop.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || len(page.Items) != 1 || page.Items[0].Document.Kind != jobcontract.EventPerformanceResult {
		t.Fatalf("archive ran before sealed result import: %+v, %v", page, err)
	}
	sealedCursor := page.NextCursor
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET imported=1
		WHERE reference_type='performance_result'`); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.ListRemoteDocumentWork(t.Context(), query); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("a flag without the actual imported result unlocked archive")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET imported=0`); err != nil {
		t.Fatal(err)
	}
	if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
		Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventPerformanceResult, Document: sealed.Document,
	}); err != nil {
		t.Fatal(err)
	}
	query.After = sealedCursor
	page, err = desktop.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || len(page.Items) != 1 || page.Items[0].Document.Kind != jobcontract.EventReport || page.HasMore {
		t.Fatalf("same-job archive did not follow durable sealed import: %+v, %v", page, err)
	}
	var sealedReference []byte
	err = db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_job_report_refs
		WHERE reference_type='performance_result'`).Scan(&sealedReference)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"$.job_id", "$.report_id"} {
		if _, err := db.ExecContext(
			t.Context(),
			`UPDATE remote_job_report_refs SET document_json=json_set(document_json,?,?)
			WHERE reference_type='performance_result'`,
			field,
			"85000000-0000-4000-8000-000000000999",
		); err != nil {
			t.Fatal(err)
		}
		if _, err := desktop.ListRemoteDocumentWork(t.Context(), query); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("archive trusted a sealed result with a different %s: %v", field, err)
		}
		if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET document_json=?
			WHERE reference_type='performance_result'`, sealedReference); err != nil {
			t.Fatal(err)
		}
	}
	if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
		Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventReport, Document: archive.Document,
	}); err != nil {
		t.Fatal(err)
	}
	query.After = remoteresults.DocumentCursor{}
	page, err = desktop.ListRemoteDocumentWork(t.Context(), query)
	if err != nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("imported performance documents remained work: %+v, %v", page, err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET imported=0,
		document_json=json_set(document_json,'$.size',?) WHERE reference_type='report'`, 40<<20); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.ListRemoteDocumentWork(t.Context(), query); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("archive used a larger unrelated entity budget")
	}
}

func TestRemoteDocumentWorkRejectsWrongRunReferenceWithoutChangingStorage(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	spec := priorityMirror(t, repository, 1)
	db := openDatabase(t, path)
	defer db.Close()
	addDocumentWorkReference(
		t,
		db,
		spec,
		1,
	)
	if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_report_refs SET document_json=
		json_set(document_json,'$.run_id','85000000-0000-4000-8000-000000000999')`); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := db.QueryRowContext(t.Context(),
		`SELECT document_json FROM remote_job_report_refs`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	query := documentWorkQuery(remoteresults.DocumentHistory)
	if _, err := repository.ListRemoteDocumentWork(t.Context(), query); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("foreign run reference entered document work: %v", err)
	}
	err := db.QueryRowContext(t.Context(), `SELECT document_json FROM remote_job_report_refs`).Scan(&after)
	if err != nil || before != after {
		t.Fatal("rejected reference was rewritten")
	}
}
