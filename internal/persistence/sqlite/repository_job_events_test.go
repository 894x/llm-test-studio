package sqlite_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func openJournalFixture(t *testing.T) (*persistence.Repository, *sql.DB, jobcontract.Snapshot) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "journal-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	t.Cleanup(func() { _ = repository.Close() })
	db := openDatabase(t, path)
	t.Cleanup(func() { _ = db.Close() })
	input := jobAcceptance(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	snapshot, _, err := repository.AcceptJob(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	return repository, db, snapshot
}

func canonicalFixtureJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func insertJournalFixtureEvent(t *testing.T, db *sql.DB, event jobcontract.Event) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `
		INSERT INTO execution_job_events(job_id, sequence, kind, created_at, document_json)
		VALUES(?, ?, ?, ?, ?)
	`, event.JobID, event.Sequence, event.Kind, event.CreatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
		canonicalFixtureJSON(t, event))
	if err != nil {
		t.Fatal(err)
	}
}

func setJournalFixtureSnapshot(t *testing.T, db *sql.DB, snapshot jobcontract.Snapshot) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `UPDATE execution_jobs SET snapshot_json = ?, last_sequence = ? WHERE id = ?`,
		canonicalFixtureJSON(t, snapshot), snapshot.LatestSequence, snapshot.Receipt.JobID)
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureProgressEvent(snapshot jobcontract.Snapshot, sequence uint64) jobcontract.Event {
	return jobcontract.Event{
		Contract: jobcontract.CurrentContract, JobID: snapshot.Receipt.JobID, Sequence: sequence,
		Kind: jobcontract.EventProgress, CreatedAt: snapshot.UpdatedAt, Payload: json.RawMessage(`{}`),
	}
}

func TestJobEventPageByteLimitResumesWithoutDroppingEvents(t *testing.T) {
	repository, db, snapshot := openJournalFixture(t)
	for sequence := uint64(2); sequence <= 4; sequence++ {
		event := fixtureProgressEvent(snapshot, sequence)
		event.Payload = json.RawMessage(`{"padding":"` + strings.Repeat("x", 4<<20) + `"}`)
		insertJournalFixtureEvent(t, db, event)
	}
	snapshot.LatestSequence = 4
	setJournalFixtureSnapshot(t, db, snapshot)
	first, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, 1, 200)
	if err != nil || len(first.Events) != 2 || first.LastSequence != 3 || !first.HasMore {
		t.Fatalf("byte-capped page: count=%d last=%d err=%v", len(first.Events), first.LastSequence, err)
	}
	encoded, err := json.Marshal(first)
	if err != nil || len(encoded) > jobcontract.MaxEventPageBytes {
		t.Fatalf("complete page exceeds byte budget: bytes=%d err=%v", len(encoded), err)
	}
	second, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, first.LastSequence, 200)
	if err != nil || len(second.Events) != 1 || second.LastSequence != 4 || second.HasMore {
		t.Fatalf("resumed page lost an event: count=%d last=%d err=%v", len(second.Events), second.LastSequence, err)
	}
}

func TestJobEventPageAdmitsOneMaximumCompleteEvent(t *testing.T) {
	repository, db, snapshot := openJournalFixture(t)
	event := fixtureProgressEvent(snapshot, 2)
	event.Payload = json.RawMessage(`""`)
	base := canonicalFixtureJSON(t, event)
	event.Payload = json.RawMessage(`"` + strings.Repeat("x", jobcontract.MaxEventBytes-len(base)) + `"`)
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(canonicalFixtureJSON(t, event)) != jobcontract.MaxEventBytes {
		t.Fatal("fixture is not at the exact complete-event limit")
	}
	insertJournalFixtureEvent(t, db, event)
	snapshot.LatestSequence = 2
	setJournalFixtureSnapshot(t, db, snapshot)
	page, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, 1, 200)
	if err != nil || len(page.Events) != 1 || page.LastSequence != 2 || page.HasMore {
		t.Fatalf("maximum event page rejected: %v", err)
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded) > jobcontract.MaxEventPageBytes {
		t.Fatalf("maximum event made an oversized page: %v", err)
	}
}

func TestJobEventReaderRejectsMissingExtraAndOversizedRecords(t *testing.T) {
	for _, scenario := range []string{"missing", "extra", "oversized"} {
		t.Run(scenario, func(t *testing.T) {
			repository, db, snapshot := openJournalFixture(t)
			switch scenario {
			case "missing":
				snapshot.LatestSequence = 2
				setJournalFixtureSnapshot(t, db, snapshot)
			case "extra":
				insertJournalFixtureEvent(t, db, fixtureProgressEvent(snapshot, 2))
			case "oversized":
				event := fixtureProgressEvent(snapshot, 2)
				event.Payload = json.RawMessage(`"` + strings.Repeat("x", jobcontract.MaxEventBytes) + `"`)
				insertJournalFixtureEvent(t, db, event)
				snapshot.LatestSequence = 2
				setJournalFixtureSnapshot(t, db, snapshot)
			}
			if _, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, 0, 200); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("invalid journal was served: %v", err)
			}
		})
	}
}

func TestJobEventCursorAndLimitBoundaries(t *testing.T) {
	repository, _, snapshot := openJournalFixture(t)
	for _, limit := range []uint32{0, 201} {
		if _, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, 0, limit); !errors.Is(err, jobhost.ErrInvalid) {
			t.Fatalf("invalid page limit %d accepted: %v", limit, err)
		}
	}
	for _, limit := range []uint32{1, 200} {
		page, err := repository.JobEvents(t.Context(), snapshot.Receipt.JobID, snapshot.LatestSequence, limit)
		if err != nil || page.Events == nil || len(page.Events) != 0 || page.LastSequence != 1 || page.HasMore {
			t.Fatalf("latest cursor did not return an explicit empty page: %+v err=%v", page, err)
		}
	}
}
