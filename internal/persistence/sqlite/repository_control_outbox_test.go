package sqlite_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func controlFixture(jobID string, index int) jobs.ControlRecord {
	at := repositoryEpoch.Add(2 * time.Minute)
	return jobs.ControlRecord{SchemaVersion: 1, Revision: 1, State: jobs.ControlPending,
		CreatedAt: at, UpdatedAt: at, Observation: jobs.ObservationNone,
		Command: jobcontract.Command{Contract: jobcontract.CurrentContract,
			CommandID: fmt.Sprintf("83000000-0000-4000-8000-%012d", index), JobID: jobID,
			Kind: jobcontract.CommandCancel, ExpectedRevision: 1}}
}

func TestControlOutboxReopensOriginalInstructionsAndStableConfirmations(t *testing.T) {
	fixture := newSubmittedReadFixture(t, true)
	fixture.confirm(t)
	record := controlFixture(fixture.spec.JobID, 1)
	stored, err := fixture.repository.CreateControl(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"pending control",
		record,
		stored,
	)
	confirmation, _, err := fixture.executor.ApplyJobCommand(t.Context(), record.Command, record.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	next := record
	next.Revision++
	next.State, next.Confirmation = jobs.ControlConfirmed, &confirmation
	next.UpdatedAt = next.UpdatedAt.Add(time.Second)
	if err := fixture.repository.UpdateControl(t.Context(), next, record.Revision); err != nil {
		t.Fatal(err)
	}
	confirmation.Snapshot.Receipt.RunIDs[0] = "caller mutation"
	var databasePath string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(
		&databasePath,
	); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, databasePath)
	defer reopened.Close()
	read, err := reopened.GetControl(t.Context(), record.Command.CommandID)
	validRead := err == nil && read.State == jobs.ControlConfirmed && read.Confirmation != nil
	if !validRead || read.Confirmation.Snapshot.Receipt.RunIDs[0] == "caller mutation" {
		t.Fatalf("confirmed original command was lost or aliased: %v", err)
	}
	record.CreatedAt, record.UpdatedAt = record.CreatedAt.Add(time.Hour), record.UpdatedAt.Add(time.Hour)
	retried, err := reopened.CreateControl(t.Context(), record)
	if err != nil {
		t.Fatal(err)
	}
	assertRoundTrip(
		t,
		"same command returns original confirmation",
		read,
		retried,
	)
	for _, mutation := range []func(*jobs.ControlRecord){
		func(value *jobs.ControlRecord) { value.Command.Kind = jobcontract.CommandStopSending },
		func(value *jobs.ControlRecord) { value.Command.ExpectedRevision++ },
		func(value *jobs.ControlRecord) { value.Command.JobID = remoteOrigin.NodeID },
	} {
		changed := record
		mutation(&changed)
		if _, err := reopened.CreateControl(t.Context(), changed); !errors.Is(err, jobs.ErrConflict) {
			t.Fatalf("same command ID accepted changed instructions: %v", err)
		}
	}
	read.Revision++
	read.State, read.Confirmation = jobs.ControlPending, nil
	if err := reopened.UpdateControl(t.Context(), read, read.Revision-1); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("confirmed control returned to pending")
	}
}

func TestControlOutboxConcurrentCreateAndCASHaveOneStateWinner(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	var databasePath string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(
		&databasePath,
	); err != nil {
		t.Fatal(err)
	}
	other := reopenHardeningRepository(t, databasePath)
	defer other.Close()
	repositories := []*persistence.Repository{fixture.repository, other}
	record := controlFixture(fixture.spec.JobID, 1)
	errorsFound := make(chan error, 2)
	var workers sync.WaitGroup
	for _, repository := range repositories {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := repository.CreateControl(t.Context(), record)
			errorsFound <- err
		}()
	}
	workers.Wait()
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatal(err)
		}
	}
	next := record
	next.Revision++
	next.Observation, next.UpdatedAt = jobs.ObservationUnreachable, next.UpdatedAt.Add(time.Second)
	for _, repository := range repositories {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsFound <- repository.UpdateControl(t.Context(), next, record.Revision)
		}()
	}
	workers.Wait()
	var successes, conflicts int
	for range 2 {
		switch err := <-errorsFound; {
		case err == nil:
			successes++
		case errors.Is(err, jobs.ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("control update did not use a revision CAS")
	}
}

func TestControlOutboxBoundsPendingPagesAndDoesNotReturnFinalInstructions(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	for index := 1; index <= 27; index++ {
		record := controlFixture(fixture.spec.JobID, index)
		if _, err := fixture.repository.CreateControl(t.Context(), record); err != nil {
			t.Fatal(err)
		}
		if index == 1 {
			record.Revision++
			record.State, record.Observation = jobs.ControlRejected, jobs.ObservationRejected
			if err := fixture.repository.UpdateControl(t.Context(), record, 1); err != nil {
				t.Fatal(err)
			}
			repeated, err := fixture.repository.CreateControl(t.Context(), controlFixture(fixture.spec.JobID, index))
			if err != nil || repeated.State != jobs.ControlRejected {
				t.Fatalf("repeated creation reset a rejected instruction: %v", err)
			}
			record.Revision++
			record.State, record.Observation = jobs.ControlPending, jobs.ObservationNone
			err = fixture.repository.UpdateControl(t.Context(), record, record.Revision-1)
			if !errors.Is(err, jobs.ErrConflict) {
				t.Fatal("rejected instruction returned to pending")
			}
		}
	}
	first, err := fixture.repository.ListPendingControls(t.Context(), "", 25)
	validFirst := err == nil && len(first.Records) == 25 && first.HasMore
	if !validFirst || first.Records[0].Command.CommandID !=
		controlFixture(fixture.spec.JobID, 2).Command.CommandID {
		t.Fatalf("pending control page is not bounded or contains a final instruction: %v", err)
	}
	last, err := fixture.repository.ListPendingControls(t.Context(), first.NextCursor, 25)
	validLast := !last.HasMore && len(last.Records) == 1 && last.NextCursor > first.NextCursor
	if err != nil || !validLast {
		t.Fatalf("pending control tail could not resume: %v", err)
	}
	for _, limit := range []uint32{0, 26} {
		if _, err := fixture.repository.ListPendingControls(t.Context(), "", limit); !errors.Is(err, jobs.ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = fixture.repository.GetControl(ctx, first.Records[0].Command.CommandID)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestControlOutboxRejectsCorruptDocumentsWithoutChangingStoredBytes(t *testing.T) {
	for _, target := range []string{"size", "duplicate", "unknown", "version", "missing", "null", "case",
		"columns", "confirmation owner"} {
		t.Run(target, func(t *testing.T) {
			fixture := newSubmittedReadFixture(t, false)
			record := controlFixture(fixture.spec.JobID, 1)
			if _, err := fixture.repository.CreateControl(t.Context(), record); err != nil {
				t.Fatal(err)
			}
			read := func() []byte {
				var document []byte
				if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM job_control_outbox
					WHERE command_id = ?`, record.Command.CommandID).Scan(&document); err != nil {
					t.Fatal(err)
				}
				return document
			}
			document := read()
			statement := `UPDATE job_control_outbox SET document_json = ? WHERE command_id = ?`
			switch target {
			case "size":
				document = []byte(`{"padding":"` + strings.Repeat("x", jobs.MaxControlRecordBytes) + `"}`)
			case "duplicate":
				document = bytes.Replace(
					document,
					[]byte(`"schema_version":1`),
					[]byte(`"schema_version":1,"schema_version":1`),
					1,
				)
			case "unknown":
				document = append([]byte(`{"unknown":1,`), document[1:]...)
			case "version":
				document = bytes.Replace(
					document,
					[]byte(`"schema_version":1`),
					[]byte(`"schema_version":0`),
					1,
				)
			case "missing":
				document = bytes.Replace(
					document,
					[]byte(`"observation":"",`),
					nil,
					1,
				)
			case "null":
				document = bytes.Replace(
					document,
					[]byte(`"observation":""`),
					[]byte(`"observation":null`),
					1,
				)
			case "case":
				document = bytes.Replace(
					document,
					[]byte(`"schema_version"`),
					[]byte(`"Schema_version"`),
					1,
				)
			case "columns":
				statement = `UPDATE job_control_outbox SET job_id = ?, document_json = ? WHERE command_id = ?`
				if _, err := fixture.db.ExecContext(t.Context(), `PRAGMA foreign_keys = OFF`); err != nil {
					t.Fatal(err)
				}
			case "confirmation owner":
				confirmation, _, err := fixture.executor.ApplyJobCommand(t.Context(), record.Command, record.CreatedAt)
				if err != nil {
					t.Fatal(err)
				}
				record.Revision++
				record.State, record.Confirmation = jobs.ControlConfirmed, &confirmation
				if err := fixture.repository.UpdateControl(t.Context(), record, 1); err != nil {
					t.Fatal(err)
				}
				document = bytes.Replace(
					read(),
					[]byte(fixture.spec.NodeInstallationID),
					[]byte(remoteOrigin.NodeID),
					1,
				)
			}
			arguments := []any{document, record.Command.CommandID}
			if target == "columns" {
				arguments = []any{remoteOrigin.NodeID, document, record.Command.CommandID}
			}
			if _, err := fixture.db.ExecContext(t.Context(), statement, arguments...); err != nil {
				t.Fatal(err)
			}
			before := read()
			_, err := fixture.repository.GetControl(t.Context(), record.Command.CommandID)
			if !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt control accepted: %v", err)
			}
			if target != "confirmation owner" {
				_, err := fixture.repository.ListPendingControls(t.Context(), "", 25)
				if !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("corrupt pending page accepted: %v", err)
				}
			}
			if !bytes.Equal(before, read()) {
				t.Fatal("rejected control data was repaired or changed")
			}
		})
	}
}
