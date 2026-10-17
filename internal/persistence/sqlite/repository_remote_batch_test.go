package sqlite_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

type remoteBatchFixture struct {
	repository *persistence.Repository
	db         *sql.DB
	spec       jobcontract.JobSpec
	events     []jobcontract.Event
	result     jobcontract.Event
}

func newRemoteBatchFixture(t *testing.T) remoteBatchFixture {
	t.Helper()
	node := openOperationalRepository(t)
	t.Cleanup(func() { _ = node.Close() })
	spec := populateRemoteRun(t, node, newRepositoryFixture(t))
	events := remoteEvents(t, node, spec)
	path := filepath.Join(t.TempDir(), "remote-batch.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "batch-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	t.Cleanup(func() { _ = repository.Close() })
	db := openDatabase(t, path)
	t.Cleanup(func() { _ = db.Close() })
	for index, event := range events {
		if event.Kind == jobcontract.EventResults {
			applyRemoteEvents(t, repository, events[:index])
			return remoteBatchFixture{repository: repository, db: db, spec: spec, events: events, result: event}
		}
	}
	t.Fatal("fixture has no result event")
	return remoteBatchFixture{}
}

func (fixture remoteBatchFixture) requireRejectedPage(t *testing.T, events []jobcontract.Event) {
	t.Helper()
	before, err := fixture.repository.GetRemoteMirror(t.Context(), fixture.spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.repository.ApplyRemoteBatch(t.Context(), remoteresults.Batch{
		Origin: remoteOrigin, JobID: fixture.spec.JobID,
		AfterSequence: events[0].Sequence - 1, Events: events,
	})
	if !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("changed run binding accepted: %v", err)
	}
	var cursor uint64
	if err := fixture.db.QueryRowContext(t.Context(),
		`SELECT applied_sequence FROM remote_job_mirrors WHERE job_id = ?`, fixture.spec.JobID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	if cursor != before.Snapshot.LatestSequence {
		t.Fatal("rejected page advanced cursor")
	}
}

func TestRemoteBatchWarmRunCacheRechecksRowsWithinPage(t *testing.T) {
	for _, target := range []string{"position", "seal"} {
		t.Run(target, func(t *testing.T) {
			fixture := newRemoteBatchFixture(t)
			mutation := `UPDATE remote_job_runs SET position = 1;`
			if target == "seal" {
				mutation = `UPDATE execution_runs SET sealed = 1;`
			}
			if _, err := fixture.db.ExecContext(t.Context(), `CREATE TRIGGER change_batch_binding
				AFTER INSERT ON remote_job_events WHEN NEW.kind = 'results' BEGIN `+mutation+` END`); err != nil {
				t.Fatal(err)
			}
			second := fixture.result
			second.Sequence++
			fixture.requireRejectedPage(t, []jobcontract.Event{fixture.result, second})
			if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER change_batch_binding`); err != nil {
				t.Fatal(err)
			}
			// The failed transaction's row mutation, inserted result and cache all disappear.
			applyRemoteEvents(t, fixture.repository, []jobcontract.Event{fixture.result, second})
		})
	}
}

func TestRemoteBatchWarmRunCacheRejectsCrossPageSameRevisionTampering(t *testing.T) {
	for _, target := range []string{"snapshot", "position"} {
		t.Run(target, func(t *testing.T) {
			fixture := newRemoteBatchFixture(t)
			applyRemoteEvents(t, fixture.repository, []jobcontract.Event{fixture.result})
			runID := fixture.spec.Spec.Run.Meta.ID
			switch target {
			case "snapshot":
				if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
					t.Fatal(err)
				}
				before, err := fixture.repository.GetRun(t.Context(), runID)
				if err != nil {
					t.Fatal(err)
				}
				name := before.Snapshot().Entries[0].CaseDefinitions[0].Name
				changed := "X" + name[1:]
				if len(changed) != len(name) {
					t.Fatal("tamper must preserve byte length")
				}
				if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_run_revisions SET
					snapshot_json = json_set(snapshot_json, '$.entries[0].case_definitions[0].name', ?),
					document_json = json_set(document_json, '$.plan_snapshot.entries[0].case_definitions[0].name', ?)
					WHERE run_id = ? AND revision = ?`, changed, changed, runID, before.Meta().Revision); err != nil {
					t.Fatal(err)
				}
				current, err := fixture.repository.GetRun(t.Context(), runID)
				if err != nil || current.Meta().Revision != before.Meta().Revision {
					t.Fatalf("tamper must remain a valid same-revision current run: %v", err)
				}
			case "position":
				if _, err := fixture.db.ExecContext(t.Context(),
					`UPDATE remote_job_runs SET position = 1 WHERE run_id = ?`, runID); err != nil {
					t.Fatal(err)
				}
			}
			replay := fixture.result
			replay.Sequence++
			fixture.requireRejectedPage(t, []jobcontract.Event{replay})
		})
	}
}

func TestRemoteBatchOldRunRevisionReplayKeepsLatestRunForReportReference(t *testing.T) {
	fixture := newRemoteBatchFixture(t)
	applyRemoteEvents(t, fixture.repository, fixture.events[int(fixture.result.Sequence)-1:])
	var oldRun, reference jobcontract.Event
	for _, event := range fixture.events {
		if event.Kind == jobcontract.EventRun && oldRun.Sequence == 0 {
			oldRun = event
		}
		if event.Kind == jobcontract.EventReport {
			reference = event
		}
	}
	if oldRun.Sequence == 0 || reference.Sequence == 0 {
		t.Fatal("fixture lacks run history or report reference")
	}
	result := fixture.result
	result.Sequence = uint64(len(fixture.events)) + 1
	oldRun.Sequence = result.Sequence + 1
	reference.Sequence = result.Sequence + 2
	applyRemoteEvents(t, fixture.repository, []jobcontract.Event{result, oldRun, reference})
	current, err := fixture.repository.GetRun(t.Context(), fixture.spec.Spec.Run.Meta.ID)
	if err != nil || current.Status() != domain.RunCompleted || current.Meta().Revision != 4 {
		t.Fatalf("old replay replaced the current terminal run: %v", err)
	}
}
