package sqlite

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
)

func openReadViewFixture(t *testing.T) (*Repository, *Repository) {
	t.Helper()
	filename := "read view # % shared.db"
	if runtime.GOOS != "windows" {
		filename = "read view #? % shared.db"
	}
	path := filepath.Join(t.TempDir(), filename)
	uriPath := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" {
		uriPath = "/" + uriPath
	}
	location := url.URL{Scheme: "file", Path: uriPath}
	if err := Migrate(t.Context(), location.String(), MigrateOptions{AppVersion: "read-view-test"}); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRepository(t.Context(), location.String(), RepositoryOptions{BusyTimeout: 123 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	reader, err := writer.OpenReadOnlyView(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	for _, repository := range []*Repository{writer, reader} {
		var actual string
		if err := repository.db.QueryRowContext(t.Context(),
			`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&actual); err != nil {
			t.Fatal(err)
		}
		if filepath.Clean(actual) != path {
			t.Fatalf("repository opened %q instead of the exact path %q", actual, path)
		}
	}
	return writer, reader
}

func TestRunnerReadViewLongSnapshotDoesNotBlockExecution(t *testing.T) {
	writer, reader := openReadViewFixture(t)
	run := readViewRunFixture(t)
	if err := writer.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before int64
	revisionQuery := `SELECT current_revision FROM execution_runs WHERE id=?`
	if err := tx.QueryRowContext(t.Context(), revisionQuery, run.Meta().ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Keep the reader transaction and its old snapshot open until every critical
	// operation has completed. This is a gate, not a CPU-speed threshold test.
	critical, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := writer.GetRun(critical, run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	next, err := run.Transition(domain.RunStarting, run.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.UpdateRun(critical, run.Meta().Revision, next); err != nil {
		t.Fatalf("writer waited for the HTTP snapshot: %v", err)
	}
	if err := writer.CheckWritable(critical); err != nil {
		t.Fatal(err)
	}
	var during int64
	if err := tx.QueryRowContext(t.Context(), revisionQuery, run.Meta().ID).Scan(&during); err != nil {
		t.Fatal(err)
	}
	if before != 1 || during != before {
		t.Fatalf("snapshot changed during writer commit: %d -> %d", before, during)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := reader.GetRun(t.Context(), run.Meta().ID)
	if err != nil || loaded.Meta().Revision != 2 {
		t.Fatalf("new read did not see the committed revision: %v", err)
	}
}

func TestRunnerReadViewEnforcesReadOnlyAcrossReplacement(t *testing.T) {
	writer, reader := openReadViewFixture(t)
	for replacement := 0; replacement < 2; replacement++ {
		if replacement != 0 {
			connection, err := reader.db.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := connection.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			_ = connection.Close()
		}
		var journal string
		var queryOnly, foreignKeys, busy int
		if err := reader.db.QueryRowContext(t.Context(), `PRAGMA journal_mode`).Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := reader.db.QueryRowContext(t.Context(), `PRAGMA query_only`).Scan(&queryOnly); err != nil {
			t.Fatal(err)
		}
		if err := reader.db.QueryRowContext(t.Context(), `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := reader.db.QueryRowContext(t.Context(), `PRAGMA busy_timeout`).Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if journal != "wal" || queryOnly != 1 || foreignKeys != 1 || busy != 123 {
			t.Fatalf("reader settings: %s/%d/%d/%d", journal, queryOnly, foreignKeys, busy)
		}
		if _, err := reader.db.ExecContext(t.Context(), `PRAGMA query_only=OFF`); err != nil {
			t.Fatal(err)
		}
		if err := reader.CheckWritable(t.Context()); err == nil {
			t.Fatal("mode=ro did not independently prevent writes")
		}
	}
	if err := writer.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerReadViewQueueCancellationAndClose(t *testing.T) {
	writer, reader := openReadViewFixture(t)
	held, err := reader.db.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	queued, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := reader.GetRun(queued, "30000000-0000-4000-8000-000000000006"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued reader ignored cancellation: %v", err)
	}
	if err := writer.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	// HTTP owns the work using its view and must join it before Close. DB.Close
	// alone does not join an already leased transaction or subsequent CPU work.
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.GetRun(t.Context(), "30000000-0000-4000-8000-000000000006"); err == nil {
		t.Fatal("closed read view remained usable")
	}
	if err := writer.CheckWritable(t.Context()); err != nil {
		t.Fatal("closing HTTP read view closed execution repository")
	}
}

func TestRunnerReadViewRejectsMemoryDatabaseWithoutChangingIt(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	create := `CREATE TABLE sentinel(value TEXT); INSERT INTO sentinel VALUES('preserved')`
	if _, err := db.ExecContext(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	writer := &Repository{db: db}
	if _, err := writer.OpenReadOnlyView(t.Context()); err == nil || !strings.Contains(err.Error(), "file-backed") {
		t.Fatalf("memory read view = %v", err)
	}
	var value string
	err = db.QueryRowContext(t.Context(), `SELECT value FROM sentinel`).Scan(&value)
	if err != nil || value != "preserved" {
		t.Fatalf("rejected construction changed data: %q, %v", value, err)
	}
}

func TestRunnerReadViewRefusesUnavailableWALWithoutChangingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "read-view-test"}); err != nil {
		t.Fatal(err)
	}
	writer, err := OpenRepository(t.Context(), path, RepositoryOptions{BusyTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	run := readViewRunFixture(t)
	if err := writer.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(t.Context(), `SELECT count(*) FROM execution_runs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.OpenReadOnlyView(t.Context()); err == nil {
		t.Fatal("view opened without being able to establish WAL")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	loaded, err := writer.GetRun(t.Context(), run.Meta().ID)
	if err != nil || loaded.Meta().Revision != 1 {
		t.Fatalf("WAL failure changed current run: %v", err)
	}
	reader, err := writer.OpenReadOnlyView(t.Context())
	if err != nil {
		t.Fatalf("released WAL blocker still prevented construction: %v", err)
	}
	defer reader.Close()
}

func TestRunnerReadViewRejectsCancelledConstruction(t *testing.T) {
	writer, _ := openReadViewFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := writer.OpenReadOnlyView(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("construction ignored cancellation: %v", err)
	}
}

func TestRunnerReadViewUsesCurrentSchemaValidationWithoutReset(t *testing.T) {
	writer, reader := openReadViewFixture(t)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	create := `CREATE TABLE unknown_contract(value TEXT); INSERT INTO unknown_contract VALUES('preserved')`
	if _, err := writer.db.ExecContext(t.Context(), create); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.OpenReadOnlyView(t.Context()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("read view bypassed current schema validation: %v", err)
	}
	var value string
	err := writer.db.QueryRowContext(t.Context(), `SELECT value FROM unknown_contract`).Scan(&value)
	if err != nil || value != "preserved" {
		t.Fatalf("rejected view reset unknown data: %q, %v", value, err)
	}
}

func readViewMeta(id string, revision uint64) domain.EntityMeta {
	meta, _ := domain.NewEntityMeta(time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC))
	meta.ID, meta.Revision = id, revision
	return meta
}
func readViewRunFixture(t *testing.T) domain.Run {
	t.Helper()

	const (
		modelID   = "30000000-0000-4000-8000-000000000001"
		channelID = "30000000-0000-4000-8000-000000000002"
		mappingID = "30000000-0000-4000-8000-000000000003"
		caseID    = "30000000-0000-4000-8000-000000000004"
		planID    = "30000000-0000-4000-8000-000000000005"
		runID     = "30000000-0000-4000-8000-000000000006"
		suiteID   = "30000000-0000-4000-8000-00000000000b"
		entryID   = "30000000-0000-4000-8000-00000000000c"
	)
	load := domain.LoadProfile{
		Mode:             domain.LoadSingle,
		Concurrency:      1,
		RequestCount:     1,
		RequestTimeoutMS: 30_000,
	}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 5_000}}
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	testCase := domain.TestCase{
		EntityMeta: readViewMeta(caseID, 1),
		Key:        "T-SNAPSHOT-ONLY",
		Name:       "Snapshot-only run case",
		Dimension:  "persistence",

		Enabled:       true,
		Default:       true,
		Severity:      domain.CaseSeverityCritical,
		ExecutionMode: domain.CaseExecutionAutomatic,
		Definitions: domain.ProtocolDefinitions{
			domain.ProtocolOpenAIChat: json.RawMessage(`{
				"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},
				"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]
			}`),
		},
	}
	suite := domain.Suite{
		EntityMeta: readViewMeta(suiteID, 1), Key: "snapshot-only", Name: "Snapshot-only suite",
		Protocol: domain.ProtocolOpenAIChat,
		Cases:    []domain.CaseRef{{CaseID: caseRef.CaseID}}, Inputs: []domain.SuiteInput{},
	}
	plan := domain.Plan{
		EntityMeta: readViewMeta(planID, 1),
		Name:       "Snapshot-only plan",
		Protocol:   domain.ProtocolOpenAIChat,
		Entries: []domain.PlanEntry{{
			EntryID: entryID, TargetKind: domain.PlanTargetSuite, TargetID: suiteID,
			Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
	}
	mapping := domain.ChannelModel{Protocols: []domain.Protocol{domain.ProtocolOpenAIChat},
		EntityMeta:        readViewMeta(mappingID, 1),
		ChannelID:         channelID,
		ModelID:           modelID,
		UpstreamModelName: "upstream-snapshot-only",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: planID, Revision: 1},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1},
			Name:              "Snapshot-only model",
			Protocol:          domain.ProtocolOpenAIChat,
			Capabilities:      []string{"chat", "streaming"},
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1},
			Name:              "Snapshot-only channel",
			BaseURL:           "https://snapshot-only.example.test/v1",
			Protocol:          domain.ProtocolOpenAIChat,
			UpstreamModelName: mapping.UpstreamModelName,
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct",
			AppVersion: "test", EngineVersion: "go-test",
		},
		PlanDocument: &plan,
		Mapping:      &mapping,
		Entries: []domain.RunEntrySnapshot{{
			EntryID: entryID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
			Name: suite.Name, Key: suite.Key, Suite: &suite,
			CaseInputs:      map[string]map[string]json.RawMessage{caseRef.CaseID: {}},
			Cases:           []domain.CaseRevisionRef{caseRef},
			CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
	}
	run, err := domain.NewRun(readViewMeta(runID, 1), planID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	return run
}
