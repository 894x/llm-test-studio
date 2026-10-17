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

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func priorityMirror(t *testing.T, repository *persistence.Repository, index int) jobcontract.JobSpec {
	return priorityMirrorForOrigin(
		t,
		repository,
		index,
		remoteOrigin,
	)
}

func priorityMirrorForOrigin(
	t *testing.T,
	repository *persistence.Repository,
	index int,
	origin remoteresults.Origin,
) jobcontract.JobSpec {
	t.Helper()
	spec := remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	spec.JobID, spec.Spec.Run.Meta.ID = submittedPageJobID(index), fmt.Sprintf("85000000-0000-4000-8000-%012d", index)
	spec.Target.NodeID, spec.NodeInstallationID = origin.NodeID, origin.InstallationID
	snapshot, err := spec.InitialSnapshot(repositoryEpoch.Add(time.Second), executionOrigin(spec))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(jobcontract.AcceptedEvent{Spec: spec, Snapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{Origin: origin, JobID: spec.JobID,
		Events: []jobcontract.Event{{
			Contract: jobcontract.CurrentContract, JobID: spec.JobID, Sequence: 1,
			Kind: jobcontract.EventAccepted, CreatedAt: snapshot.UpdatedAt, Payload: payload,
		}}})
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func terminalPriorityMirror(t *testing.T, repository *persistence.Repository, spec jobcontract.JobSpec) {
	t.Helper()
	mirror, err := repository.GetRemoteMirror(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := mirror.Snapshot
	snapshot.Receipt.Status, snapshot.Receipt.Revision = domain.RunCancelled, 2
	snapshot.LatestSequence, snapshot.UpdatedAt = 2, snapshot.UpdatedAt.Add(time.Second)
	payload, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	applyRemoteEvents(t, repository, []jobcontract.Event{{
		Contract: jobcontract.CurrentContract, JobID: spec.JobID, Sequence: 2,
		Kind: jobcontract.EventState, CreatedAt: snapshot.UpdatedAt, Payload: payload,
	}})
}

func TestRemoteSyncPriorityIncludesSharedActiveAndDeduplicatesOwnMirror(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	wanted := []string{}
	for index := 29; index > 0; index-- {
		spec := priorityMirror(t, repository, index)
		if index%2 == 0 {
			state := "pending"
			if index%4 == 0 {
				state = "accepted"
			}
			storePrioritySubmission(
				t,
				repository,
				submissionFixture(t, spec),
				state,
			)
		}
		wanted = append(wanted, spec.JobID)
	}
	slices.Sort(wanted)
	got, cursor := []string{}, ""
	for {
		page, err := repository.ListRemoteFactPriorityIDs(
			t.Context(),
			remoteOrigin,
			cursor,
			5,
		)
		if err != nil || page.JobIDs == nil || len(page.JobIDs) > 5 {
			t.Fatalf("shared priority page: %+v, %v", page, err)
		}
		got = append(got, page.JobIDs...)
		if len(page.JobIDs) > 0 && page.NextCursor != page.JobIDs[len(page.JobIDs)-1] {
			t.Fatal("keyset cursor differs from last returned job")
		}
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
	}
	if !slices.Equal(got, wanted) {
		t.Fatalf("foreign or deduplicated own jobs missing: %v", got)
	}
}

func TestRemoteFactPriorityTerminalDocumentsUseSeparateQueue(t *testing.T) {
	node, desktop := openOperationalRepository(t), openOperationalRepository(t)
	defer node.Close()
	defer desktop.Close()
	fixture := newRepositoryFixture(t)
	spec := populateRemoteRun(t, node, fixture)
	events := remoteEvents(t, node, spec)
	reportAt := slices.IndexFunc(events, func(event jobcontract.Event) bool {
		return event.Kind == jobcontract.EventReport
	})
	if reportAt < 1 {
		t.Fatal("fixture lacks a late report")
	}
	applyRemoteEvents(t, desktop, events[:reportAt])
	page, err := desktop.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		25,
	)
	if err != nil || len(page.JobIDs) != 0 || page.HasMore {
		t.Fatalf("terminal without known refs remains priority: %+v, %v", page, err)
	}
	applyRemoteEvents(t, desktop, events[reportAt:])
	page, err = desktop.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		25,
	)
	if err != nil || len(page.JobIDs) != 0 {
		t.Fatalf("late document consumed fact priority: %+v, %v", page, err)
	}
	documents, err := desktop.ListRemoteDocumentWork(t.Context(), remoteresults.DocumentQuery{
		Origin: remoteOrigin, Class: remoteresults.DocumentHistory, Limit: 2,
	})
	if err != nil || len(documents.Items) != 1 || documents.Items[0].JobID != spec.JobID {
		t.Fatalf("late document missing from separate work: %+v, %v", documents, err)
	}
	origin := executionOrigin(spec)
	fixture.report.ExecutionOrigin = &origin
	document, err := jobcontract.EncodeRunReport(fixture.report)
	if err != nil {
		t.Fatal(err)
	}
	if err := desktop.ImportRemoteRunReport(t.Context(), remoteresults.RunReportImport{
		Origin: remoteOrigin, JobID: spec.JobID, Document: document,
	}); err != nil {
		t.Fatal(err)
	}
	page, err = desktop.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		25,
	)
	if err != nil || len(page.JobIDs) != 0 || page.HasMore {
		t.Fatalf("fully imported terminal remains priority: %+v, %v", page, err)
	}
	documents, err = desktop.ListRemoteDocumentWork(t.Context(), remoteresults.DocumentQuery{
		Origin: remoteOrigin, Class: remoteresults.DocumentHistory, Limit: 2,
	})
	if err != nil || len(documents.Items) != 0 || documents.HasMore {
		t.Fatalf("imported document remained work: %+v, %v", documents, err)
	}
}

func TestRemoteSyncPrioritySkipsTerminalHistoryBeforeLimit(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	for index := 1; index <= 128; index++ {
		spec := priorityMirror(t, repository, index)
		terminalPriorityMirror(t, repository, spec)
	}
	active := priorityMirror(t, repository, 129)
	started := time.Now()
	page, err := repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		1,
	)
	t.Logf("priority prefix: terminal=128 active=1 returned=%d elapsed=%s", len(page.JobIDs), time.Since(started))
	if err != nil || !slices.Equal(page.JobIDs, []string{active.JobID}) || page.HasMore {
		t.Fatalf("terminal history consumed a priority page: %+v, %v", page, err)
	}
}

func TestRemoteSyncPriorityTerminalCorruptionIsNotFilteredOrMutated(t *testing.T) {
	for _, mutation := range []string{"status", "contract", "owner", "sequence", "root", "receipt", "origin",
		"failure", "oversized", "malformed", "duplicate", "version_label", "empty_children", "invalid_children", "time"} {
		t.Run(mutation, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			spec := priorityMirror(t, repository, 1)
			terminalPriorityMirror(t, repository, spec)
			db := openDatabase(t, path)
			defer db.Close()
			jsonPath, value := "$.receipt.status", any("unknown")
			switch mutation {
			case "empty_children", "invalid_children", "duplicate":
				value = "cancelled"
			case "contract":
				jsonPath, value = "$.receipt.contract", 0
			case "owner":
				jsonPath, value = "$.receipt.origin.node_id", submittedPageJobID(100)
			case "sequence":
				jsonPath, value = "$.latest_sequence", 1
			case "root":
				jsonPath, value = "$.schema_version", 0
			case "receipt":
				jsonPath, value = "$.receipt.schema_version", 0
			case "origin":
				jsonPath, value = "$.receipt.origin.schema_version", 0
			case "failure":
				jsonPath, value = "$.failure", "unexpected"
			case "oversized":
				jsonPath, value = "$.padding", strings.Repeat("x", jobcontract.MaxStateBytes)
			case "version_label":
				jsonPath, value = "$.receipt.origin.runner_version", ""
			case "time":
				jsonPath, value = "$.updated_at", "2026-02-30T12:00:00Z"
			}
			if _, err := db.ExecContext(
				t.Context(),
				`UPDATE remote_job_mirrors SET snapshot_json = json_set(snapshot_json, ?, ?)`,
				jsonPath,
				value,
			); err != nil {
				t.Fatal(err)
			}
			if mutation == "malformed" {
				if _, err := db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(t.Context(), `UPDATE remote_job_mirrors SET snapshot_json = '{'`); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "duplicate" {
				if _, err := db.ExecContext(t.Context(),
					`UPDATE remote_job_mirrors SET snapshot_json =
					 substr(snapshot_json,1,length(snapshot_json)-1) || ',"latest_sequence":2}'`); err != nil {
					t.Fatal(err)
				}
			}
			if mutation == "empty_children" || mutation == "invalid_children" {
				children := "[]"
				if mutation == "invalid_children" {
					children = `["not-an-id"]`
				}
				if _, err := db.ExecContext(t.Context(),
					`UPDATE remote_job_mirrors SET snapshot_json = json_set(snapshot_json,'$.receipt.run_ids',json(?))`,
					children); err != nil {
					t.Fatal(err)
				}
			}
			assertPriorityCorruptUnchanged(
				t,
				repository,
				db,
				"snapshot_json",
			)
		})
	}
}

func TestRemoteSyncPriorityInvalidImportedMarkerCannotHideTerminal(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	spec := priorityMirror(t, repository, 1)
	terminalPriorityMirror(t, repository, spec)
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(
		t.Context(),
		`INSERT INTO remote_job_report_refs
	 (job_id,reference_type,report_id,document_json,imported) VALUES (?,'report',?,'{}',2)`,
		spec.JobID,
		submittedPageJobID(100),
	); err != nil {
		t.Fatal(err)
	}
	assertPriorityCorruptUnchanged(
		t,
		repository,
		db,
		"snapshot_json",
	)
}

func assertPriorityCorruptUnchanged(t *testing.T, repository *persistence.Repository, db *sql.DB, column string) {
	t.Helper()
	var before, after string
	// All callers use fixed test-owned column names.
	query := "SELECT " + column + " FROM remote_job_mirrors"
	if err := db.QueryRowContext(t.Context(), query).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		2,
	)
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("corrupt terminal hint disappeared or was accepted: %v", err)
	}
	if err := db.QueryRowContext(t.Context(), query).Scan(&after); err != nil || after != before {
		t.Fatal("rejected hint changed persisted bytes")
	}
}

func TestRemoteSyncPriorityConflictingTerminalSourcesCannotDisappear(t *testing.T) {
	mutations := []string{"configuration_id", "node_id", "installation_id", "spec_sha256", "receipt", "prepared"}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			spec := priorityMirror(t, repository, 1)
			terminalPriorityMirror(t, repository, spec)
			state := "accepted"
			if mutation == "prepared" {
				state = "prepared"
			}
			storePrioritySubmission(
				t,
				repository,
				submissionFixture(t, spec),
				state,
			)
			db := openDatabase(t, path)
			defer db.Close()
			switch mutation {
			case "receipt":
				if _, err := db.ExecContext(t.Context(), `UPDATE job_submissions SET document_json =
				 json_set(document_json,'$.receipt.origin.runner_version','conflicting-runner')`); err != nil {
					t.Fatal(err)
				}
			case "prepared":
			default:
				value := submittedPageJobID(100)
				if mutation == "spec_sha256" {
					value = strings.Repeat("c", 64)
				}
				// This fixed enum deliberately moves the mirror out of scope too.
				if _, err := db.ExecContext(t.Context(), "UPDATE remote_job_mirrors SET "+mutation+" = ?", value); err != nil {
					t.Fatal(err)
				}
			}
			assertPriorityCorruptUnchanged(
				t,
				repository,
				db,
				"snapshot_json",
			)
		})
	}
}

func TestRemoteSyncPriorityCancellationReleasesSingleConnection(t *testing.T) {
	repository, path := newSubmittedPagesRepository(t)
	priorityMirror(t, repository, 1)
	priorityMirror(t, repository, 2)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListRemoteFactPriorityIDs(
		ctx,
		remoteOrigin,
		"",
		1,
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	ctx, done := context.WithTimeout(t.Context(), time.Second)
	defer done()
	for range 3 {
		page, err := repository.ListRemoteFactPriorityIDs(
			ctx,
			remoteOrigin,
			"",
			1,
		)
		if err != nil || len(page.JobIDs) != 1 || !page.HasMore {
			t.Fatalf("priority rows leaked sole connection: %+v, %v", page, err)
		}
	}
}

func TestRemoteSyncPriorityForeignMirrorsUseExactOrigin(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	for index, field := range []string{"wanted", "configuration", "node", "installation"} {
		origin := remoteOrigin
		switch field {
		case "configuration":
			origin.ConfigurationID = submittedPageJobID(200)
		case "node":
			origin.NodeID = submittedPageJobID(201)
		case "installation":
			origin.InstallationID = submittedPageJobID(202)
		}
		priorityMirrorForOrigin(
			t,
			repository,
			index+1,
			origin,
		)
	}
	page, err := repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		25,
	)
	if err != nil || !slices.Equal(page.JobIDs, []string{submittedPageJobID(1)}) {
		t.Fatalf("foreign mirror scope widened: %+v, %v", page, err)
	}
}

func storePrioritySubmission(
	t *testing.T,
	repository *persistence.Repository,
	record jobs.SubmissionRecord,
	state string,
) {
	t.Helper()
	if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	if state == "prepared" {
		return
	}
	record = pendingSubmission(record)
	if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
		t.Fatal(err)
	}
	if state == "accepted" {
		record = acceptedSubmission(t, record)
		if err := repository.UpdateSubmission(t.Context(), record, record.Revision-1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoteSyncPriorityPagesUseExactScopeAndBothDeliveryStates(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	wanted := []string{}
	variants := []string{"pending", "accepted", "prepared", "configuration", "node", "installation", "local"}
	for index, variant := range variants {
		spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
		spec.JobID = submittedPageJobID(index + 1)
		if variant != "local" {
			spec = remoteSpec(spec)
		}
		record := submissionFixture(t, spec)
		switch variant {
		case "configuration":
			record.Prepared.ConfigurationID = submittedPageJobID(101)
		case "node":
			record.Prepared.Spec.Target.NodeID, record.Prepared.Runner.NodeID = submittedPageJobID(102), submittedPageJobID(102)
		case "installation":
			record.Prepared.Spec.NodeInstallationID = submittedPageJobID(103)
			record.Prepared.Runner.NodeInstallationID = submittedPageJobID(103)
		}
		var err error
		record, err = jobs.NewSubmission(record.Prepared, repositoryEpoch)
		if err != nil {
			t.Fatal(err)
		}
		state := "pending"
		if variant == "accepted" || variant == "prepared" {
			state = variant
		}
		storePrioritySubmission(
			t,
			repository,
			record,
			state,
		)
		if variant == "pending" || variant == "accepted" {
			wanted = append(wanted, spec.JobID)
		}
	}
	page, err := repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		"",
		1,
	)
	if err != nil || !slices.Equal(page.JobIDs, wanted[:1]) || !page.HasMore || page.NextCursor != wanted[0] {
		t.Fatalf("priority first page: %+v, %v", page, err)
	}
	page, err = repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		page.NextCursor,
		1,
	)
	if err != nil || !slices.Equal(page.JobIDs, wanted[1:]) || page.HasMore || page.NextCursor != wanted[1] {
		t.Fatalf("priority tail: %+v, %v", page, err)
	}
	page, err = repository.ListRemoteFactPriorityIDs(
		t.Context(),
		remoteOrigin,
		page.NextCursor,
		1,
	)
	if err != nil || page.JobIDs == nil || len(page.JobIDs) != 0 || page.HasMore || page.NextCursor != wanted[1] {
		t.Fatal("empty priority tail changed its keyset cursor")
	}
}

func TestRemoteSyncPriorityRejectsColumnDocumentMismatchWithoutMutation(t *testing.T) {
	for _, column := range []string{"configuration", "node", "installation", "job", "contract", "state", "digest",
		"revision", "oversized", "unsupported_root", "unsupported_prepared"} {
		t.Run(column, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			record := submissionFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			storePrioritySubmission(
				t,
				repository,
				record,
				"pending",
			)
			db := openDatabase(t, path)
			defer db.Close()
			jsonPath, value := "$.prepared.configuration_id", any(submittedPageJobID(200))
			switch column {
			case "node":
				jsonPath = "$.prepared.runner.node_id"
			case "installation":
				jsonPath = "$.prepared.spec.node_installation_id"
			case "job":
				jsonPath = "$.prepared.spec.job_id"
			case "contract":
				jsonPath, value = "$.prepared.spec.contract", 0
			case "state":
				jsonPath, value = "$.state", "prepared"
			case "digest":
				jsonPath, value = "$.spec_sha256", strings.Repeat("c", 64)
			case "revision":
				jsonPath, value = "$.revision", 100
			case "oversized":
				jsonPath, value = "$.unexpected", strings.Repeat("x", 9<<20)
			case "unsupported_root":
				jsonPath, value = "$.schema_version", 0
			case "unsupported_prepared":
				jsonPath, value = "$.prepared.schema_version", 0
			}
			if _, err := db.ExecContext(
				t.Context(),
				`UPDATE job_submissions SET document_json = json_set(document_json, ?, ?)`,
				jsonPath,
				value,
			); err != nil {
				t.Fatal(err)
			}
			var before, after string
			if err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&before); err != nil {
				t.Fatal(err)
			}
			if _, err := repository.ListRemoteFactPriorityIDs(
				t.Context(),
				remoteOrigin,
				"",
				2,
			); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt priority owner or format accepted: %v", err)
			}
			err := db.QueryRowContext(t.Context(), `SELECT document_json FROM job_submissions`).Scan(&after)
			if err != nil || before != after {
				t.Fatal("rejected priority hint mutated persisted input")
			}
		})
	}
}

func TestRemoteSyncPriorityValidatesPageAndCancellation(t *testing.T) {
	repository, _ := newSubmittedPagesRepository(t)
	for _, invalid := range []struct {
		origin remoteresults.Origin
		after  string
		limit  uint32
	}{
		{origin: remoteresults.Origin{}, limit: 2},
		{origin: remoteOrigin, after: "not-an-id", limit: 2},
		{origin: remoteOrigin, limit: 0},
		{origin: remoteOrigin, limit: 26},
	} {
		if _, err := repository.ListRemoteFactPriorityIDs(
			t.Context(),
			invalid.origin,
			invalid.after,
			invalid.limit,
		); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal("invalid priority page accepted")
		}
	}
	if _, err := repository.ListRemoteFactPriorityIDs(
		nil,
		remoteOrigin,
		"",
		2,
	); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListRemoteFactPriorityIDs(
		ctx,
		remoteOrigin,
		"",
		2,
	); !errors.Is(err, context.Canceled) {
		t.Fatal("priority query did not propagate cancellation")
	}
}

func TestRemoteSyncPriorityColumnCorruptionCannotPromoteAnotherScope(t *testing.T) {
	for _, field := range []string{"configuration_id", "node_id", "installation_id"} {
		t.Run(field, func(t *testing.T) {
			repository, path := newSubmittedPagesRepository(t)
			record := submissionFixture(t, remoteSpec(jobSpecForRun(t, runWithCompleteSnapshot(t))))
			storePrioritySubmission(
				t,
				repository,
				record,
				"pending",
			)
			db := openDatabase(t, path)
			defer db.Close()
			origin, replacement := remoteOrigin, submittedPageJobID(201)
			// The column is from this fixed test enum, never external input.
			if _, err := db.ExecContext(t.Context(), "UPDATE job_submissions SET "+field+" = ?", replacement); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "configuration_id":
				origin.ConfigurationID = replacement
			case "node_id":
				origin.NodeID = replacement
			case "installation_id":
				origin.InstallationID = replacement
			}
			if _, err := repository.ListRemoteFactPriorityIDs(
				t.Context(),
				origin,
				"",
				2,
			); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatal("corrupted owner columns promoted a foreign submission to priority")
			}
		})
	}
}
