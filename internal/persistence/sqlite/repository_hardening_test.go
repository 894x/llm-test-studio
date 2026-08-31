package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/894x/llm-studio/internal/domain"
	persistence "github.com/894x/llm-studio/internal/persistence/sqlite"
)

func TestRepositoryRunUpdatesAreAppendOnlyAndRecoverable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "append-only-runs.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	fixture := newRepositoryFixture(t)
	createRunGraph(t, repository, fixture)
	starting := transitionRun(t, repository, fixture.run, domain.RunStarting)
	running := transitionRun(t, repository, starting, domain.RunRunning)

	for revision, want := range map[uint64]domain.Run{1: fixture.run, 2: starting, 3: running} {
		got, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, revision)
		if err != nil {
			t.Fatalf("GetRunRevision(%d) error = %v", revision, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("GetRunRevision(%d) = %#v, want %#v", revision, got, want)
		}
	}
	if got, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); err != nil || !reflect.DeepEqual(got, running) {
		t.Fatalf("GetRun() = %#v, %v, want running revision", got, err)
	}
	if err := repository.UpdateRun(context.Background(), 1, starting); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateRun() error = %v, want ErrConflict", err)
	}
	if _, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, 4); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetRunRevision(4) error = %v, want ErrNotFound", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM execution_run_revisions WHERE run_id = '`+fixture.run.Meta().ID+`'`); got != 3 {
		t.Fatalf("stored run revision count = %d, want 3", got)
	}
	var current int
	if err := db.QueryRow(`SELECT current_revision FROM execution_runs WHERE id = ?`, fixture.run.Meta().ID).Scan(&current); err != nil {
		t.Fatalf("read current run revision: %v", err)
	}
	if current != 3 {
		t.Fatalf("current run revision = %d, want 3", current)
	}
}

func TestRepositoryRunRevisionRowsAreDatabaseImmutable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		statement string
	}{
		{"update", `UPDATE execution_run_revisions SET status = 'queued' WHERE run_id = ? AND revision = 1`},
		{"delete", `DELETE FROM execution_run_revisions WHERE run_id = ? AND revision = 1`},
		{"gap insert", `
			INSERT INTO execution_run_revisions(
				run_id, schema_version, revision, created_at, updated_at,
				plan_id, plan_revision, status, snapshot_json, document_json
			)
			SELECT run_id, schema_version, 4, created_at, updated_at,
			       plan_id, plan_revision, status, snapshot_json,
			       json_set(document_json, '$.revision', 4)
			FROM execution_run_revisions WHERE run_id = ? AND revision = 1
		`},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			createRunGraph(t, repository, fixture)
			transitionRun(t, repository, fixture.run, domain.RunStarting)
			closeForTamper(t, repository)
			db := openDatabase(t, path)
			defer db.Close()
			if _, err := db.Exec(test.statement, fixture.run.Meta().ID); err == nil {
				t.Fatalf("%s of execution_run_revisions unexpectedly succeeded", test.name)
			}
			if got := queryInt(t, db, `SELECT COUNT(*) FROM execution_run_revisions WHERE run_id = '`+fixture.run.Meta().ID+`'`); got != 2 {
				t.Fatalf("run revision count after rejected %s = %d, want 2", test.name, got)
			}
		})
	}
}

func TestRepositoryDetectsRunRevisionHistoryGaps(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	createRunGraph(t, repository, fixture)
	starting := transitionRun(t, repository, fixture.run, domain.RunStarting)
	transitionRun(t, repository, starting, domain.RunRunning)

	db := openDatabase(t, path)
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS trg_execution_run_revisions_no_delete`); err != nil {
		db.Close()
		t.Fatalf("drop immutability trigger for tamper: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM execution_run_revisions WHERE run_id = ? AND revision = 2`, fixture.run.Meta().ID); err != nil {
		db.Close()
		t.Fatalf("delete middle run revision for tamper: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetRun() with history gap error = %v, want ErrCorrupt", err)
	}
	if _, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, 2); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetRunRevision(missing middle) error = %v, want ErrCorrupt", err)
	}
	if _, err := repository.ListRuns(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("ListRuns() with history gap error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryConcurrentRunCASReturnsStableConflict(t *testing.T) {
	path, first, fixture := openHardeningRepository(t)
	defer first.Close()
	second := reopenHardeningRepository(t, path)
	defer second.Close()
	createRunGraph(t, first, fixture)

	for index := 0; index < 16; index++ {
		run := fixture.run
		if index != 0 {
			var err error
			run, err = domain.NewRun(
				entityMeta(fmt.Sprintf("10000000-0000-4000-8000-%012d", 100+index), 1),
				fixture.plan.ID,
				fixture.run.Snapshot(),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := first.CreateRun(context.Background(), run); err != nil {
				t.Fatal(err)
			}
		}
		starting, err := run.Transition(domain.RunStarting, repositoryEpoch.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, repository := range []*persistence.Repository{first, second} {
			go func(repository *persistence.Repository) {
				<-start
				results <- repository.UpdateRun(context.Background(), 1, starting)
			}(repository)
		}
		close(start)
		firstErr, secondErr := <-results, <-results
		if !((firstErr == nil && errors.Is(secondErr, persistence.ErrConflict)) ||
			(secondErr == nil && errors.Is(firstErr, persistence.ErrConflict))) {
			t.Fatalf("concurrent UpdateRun() errors = (%v, %v), want one success and one ErrConflict", firstErr, secondErr)
		}
	}
}

func TestRepositoryRejectsResultAndEvidenceOutsideExecutionStates(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)

	result := fixture.result
	result.EvidenceIDs = nil
	if err := repository.CreateEvidence(ctx, fixture.evidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(queued) error = %v, want ErrConflict", err)
	}
	if err := repository.AppendResult(ctx, result); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(queued) error = %v, want ErrConflict", err)
	}

	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning, domain.RunCompleted)
	evidence := fixture.evidence
	evidence.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000031", 1)
	result.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000032", 1)
	if err := repository.CreateEvidence(ctx, evidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(%s) error = %v, want ErrConflict", run.Status(), err)
	}
	if err := repository.AppendResult(ctx, result); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(%s) error = %v, want ErrConflict", run.Status(), err)
	}
}

func TestRepositoryRejectsCredentialStoreRefIdentityMismatch(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		mutate func(*domain.CredentialRef)
	}{
		{"purpose", func(value *domain.CredentialRef) { value.Purpose = domain.CredentialIntegrationAdmin }},
		{"id", func(value *domain.CredentialRef) {
			value.StoreRef = "llm-studio/v1/channel_api_key/10000000-0000-4000-8000-000000000099"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := openRepository(t)
			defer repository.Close()
			credential := newRepositoryFixture(t).credential
			test.mutate(&credential)
			if err := repository.CreateCredentialRef(context.Background(), credential); err == nil {
				t.Fatal("CreateCredentialRef() accepted a StoreRef whose identity differs from the entity")
			}
		})
	}
}

func TestRepositoryRejectsCorruptRepeatedColumnsAndRelations(t *testing.T) {
	t.Run("credential duplicate", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateCredentialRef(context.Background(), fixture.credential); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE credential_refs SET purpose = 'integration_admin' WHERE id = ?`, fixture.credential.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetCredentialRef(context.Background(), fixture.credential.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetCredentialRef() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("channel reference revision", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateCredentialRef(context.Background(), fixture.credential); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateChannel(context.Background(), fixture.channel); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `UPDATE channels SET credential_revision = 99 WHERE id = ?`, fixture.channel.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetChannel(context.Background(), fixture.channel.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetChannel() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("suite relation deleted", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateTestCase(context.Background(), fixture.testCase); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateSuite(context.Background(), fixture.suite); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamper(t, path, `DELETE FROM suite_cases WHERE suite_id = ?`, fixture.suite.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetSuite(context.Background(), fixture.suite.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetSuite() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("plan mapping relation deleted", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunGraph(t, repository, fixture)
		closeForTamper(t, repository)
		tamper(t, path, `DELETE FROM plan_channel_models WHERE plan_id = ?`, fixture.plan.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetPlan(context.Background(), fixture.plan.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetPlan() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetRun() with deleted pinned mapping error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, 1); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetRunRevision() with deleted pinned mapping error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListRuns(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListRuns() with deleted pinned mapping error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("report attachment owner", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunGraph(t, repository, fixture)
		run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
		if err := repository.CreateEvidence(context.Background(), fixture.evidence); err != nil {
			t.Fatal(err)
		}
		if err := repository.AppendResult(context.Background(), fixture.result); err != nil {
			t.Fatal(err)
		}
		run = transitionRun(t, repository, run, domain.RunCompleted)
		report := fixture.report
		report.RunStatus = run.Status()
		if err := repository.CreateReport(context.Background(), report); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `UPDATE artifacts SET run_id = '10000000-0000-4000-8000-000000000099' WHERE id = ?`, report.Attachments[0].ArtifactID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetReport(context.Background(), report.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetReport() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("channel model target protocol", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateCredentialRef(context.Background(), fixture.credential); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateModel(context.Background(), fixture.model); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateChannel(context.Background(), fixture.channel); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateChannelModel(context.Background(), fixture.mapping); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE models SET document_json = json_set(document_json, '$.protocol', 'kimi-k3') WHERE id = ?`, fixture.model.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetChannelModel(context.Background(), fixture.mapping.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetChannelModel() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("suite target payload", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateTestCase(context.Background(), fixture.testCase); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateSuite(context.Background(), fixture.suite); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE test_cases SET document_json = json_set(document_json, '$.name', '') WHERE id = ?`, fixture.testCase.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetSuite(context.Background(), fixture.suite.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetSuite() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("plan target payload", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunGraph(t, repository, fixture)
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE models SET document_json = json_set(document_json, '$.name', '') WHERE id = ?`, fixture.model.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetPlan(context.Background(), fixture.plan.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetPlan() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("output owner deleted", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunningOutputs(t, repository, fixture)
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `DELETE FROM execution_runs WHERE id = ?`, fixture.run.Meta().ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetEvidence(context.Background(), fixture.evidence.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetEvidence() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListEvidence(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListEvidence() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.GetResult(context.Background(), fixture.result.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetResult() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListResults(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListResults() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("result evidence deleted", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunningOutputs(t, repository, fixture)
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `DELETE FROM evidence WHERE id = ?`, fixture.evidence.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetResult(context.Background(), fixture.result.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetResult() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListResults(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListResults() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("result case outside run snapshot", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunningOutputs(t, repository, fixture)
		closeForTamper(t, repository)
		outsideCase := "10000000-0000-4000-8000-000000000099"
		tamperWithoutForeignKeys(t, path, `
			UPDATE case_results
			SET case_id = ?, document_json = json_set(document_json, '$.case_id', ?)
			WHERE id = ?
		`, outsideCase, outsideCase, fixture.result.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetResult(context.Background(), fixture.result.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetResult() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListResults(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListResults() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("report pinned run relation deleted", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		run := createRunningOutputs(t, repository, fixture)
		run = transitionRun(t, repository, run, domain.RunCompleted)
		report := fixture.report
		report.RunStatus = run.Status()
		if err := repository.CreateReport(context.Background(), report); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `DELETE FROM plan_channel_models WHERE plan_id = ?`, fixture.plan.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetReport(context.Background(), report.ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetReport() error = %v, want ErrCorrupt", err)
		}
		if _, err := repository.ListReports(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("ListReports() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("historical run observes root seal", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		createRunGraph(t, repository, fixture)
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE execution_runs SET sealed = 1 WHERE id = ?`, fixture.run.Meta().ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if _, err := repository.GetRunRevision(context.Background(), fixture.run.Meta().ID, 1); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("GetRunRevision() with root seal/report mismatch error = %v, want ErrCorrupt", err)
		}
	})
}

func TestRepositoryAggregateWritesRejectCorruptDependencies(t *testing.T) {
	t.Run("channel model channel credential", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		for _, create := range []func() error{
			func() error { return repository.CreateCredentialRef(context.Background(), fixture.credential) },
			func() error { return repository.CreateModel(context.Background(), fixture.model) },
			func() error { return repository.CreateChannel(context.Background(), fixture.channel) },
		} {
			if err := create(); err != nil {
				t.Fatal(err)
			}
		}
		closeForTamper(t, repository)
		tamperWithoutForeignKeys(t, path, `UPDATE channels SET credential_revision = 99 WHERE id = ?`, fixture.channel.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if err := repository.CreateChannelModel(context.Background(), fixture.mapping); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("CreateChannelModel() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("suite test case", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		if err := repository.CreateTestCase(context.Background(), fixture.testCase); err != nil {
			t.Fatal(err)
		}
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE test_cases SET document_json = json_set(document_json, '$.name', '') WHERE id = ?`, fixture.testCase.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if err := repository.CreateSuite(context.Background(), fixture.suite); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("CreateSuite() error = %v, want ErrCorrupt", err)
		}
	})

	t.Run("plan channel model", func(t *testing.T) {
		path, repository, fixture := openHardeningRepository(t)
		for _, create := range []func() error{
			func() error { return repository.CreateCredentialRef(context.Background(), fixture.credential) },
			func() error { return repository.CreateModel(context.Background(), fixture.model) },
			func() error { return repository.CreateChannel(context.Background(), fixture.channel) },
			func() error { return repository.CreateChannelModel(context.Background(), fixture.mapping) },
			func() error { return repository.CreateTestCase(context.Background(), fixture.testCase) },
			func() error { return repository.CreateSuite(context.Background(), fixture.suite) },
		} {
			if err := create(); err != nil {
				t.Fatal(err)
			}
		}
		closeForTamper(t, repository)
		tamper(t, path, `UPDATE channel_models SET document_json = json_set(document_json, '$.upstream_model_name', '') WHERE id = ?`, fixture.mapping.ID)
		repository = reopenHardeningRepository(t, path)
		defer repository.Close()
		if err := repository.CreatePlan(context.Background(), fixture.plan); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("CreatePlan() error = %v, want ErrCorrupt", err)
		}
	})
}

func TestRepositoryReportSealsFinalResultAndEvidenceCollections(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence(running) error = %v", err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatalf("AppendResult(running) error = %v", err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}

	extraEvidence := fixture.evidence
	extraEvidence.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000033", 1)
	extraResult := fixture.result
	extraResult.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000034", 1)
	extraResult.RequestID = "request-after-report"
	extraResult.EvidenceIDs = nil
	if err := repository.CreateEvidence(ctx, extraEvidence); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateEvidence(after report) error = %v, want ErrConflict", err)
	}
	if err := repository.AppendResult(ctx, extraResult); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("AppendResult(after report) error = %v, want ErrConflict", err)
	}
}

func TestRepositoryReportSealsRequestRowsBesideFinalCaseSummaries(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatal(err)
	}
	requestResult := fixture.result
	requestResult.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000035", 1)
	requestResult.RequestID = "request-1"
	requestResult.CaseID = ""
	requestResult.EvidenceIDs = nil
	if err := repository.AppendResult(ctx, requestResult); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() with request-level rows error = %v", err)
	}
	results, err := repository.ListResults(ctx, fixture.run.Meta().ID)
	if err != nil {
		t.Fatalf("ListResults() after report error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("sealed result row count = %d, want final summary plus request row", len(results))
	}
}

func TestRepositoryReportMatchesCanonicalEmptyResultCollections(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	result := fixture.result
	result.Metrics = map[string]float64{}
	result.EvidenceIDs = []string{}
	if err := repository.AppendResult(ctx, result); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	report.CaseResults = []domain.Result{result}
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() with canonically empty result collections error = %v", err)
	}
}

func TestRepositoryReportWriteFailureRollsBackSealAndAttachments(t *testing.T) {
	t.Parallel()

	path, repository, fixture := openHardeningRepository(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatal(err)
	}
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	duplicatePath := report.Attachments[0]
	duplicatePath.ArtifactID = "10000000-0000-4000-8000-000000000038"
	report.Attachments = append(report.Attachments, duplicatePath)
	if err := report.Validate(); err != nil {
		t.Fatalf("duplicate-path report fixture is invalid: %v", err)
	}
	if err := repository.CreateReport(ctx, report); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateReport() error = %v, want ErrConflict", err)
	}
	if _, err := repository.GetReport(ctx, report.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetReport() after rollback error = %v, want ErrNotFound", err)
	}
	if _, err := repository.GetRun(ctx, fixture.run.Meta().ID); err != nil {
		t.Fatalf("GetRun() found a leaked report seal: %v", err)
	}
	closeForTamper(t, repository)
	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT sealed FROM execution_runs WHERE id = '`+fixture.run.Meta().ID+`'`); got != 0 {
		t.Fatalf("run seal after rollback = %d, want 0", got)
	}
	for _, table := range []string{"reports", "artifacts", "report_attachments"} {
		if got := queryInt(t, db, `SELECT COUNT(*) FROM `+table); got != 0 {
			t.Fatalf("%s row count after rollback = %d, want 0", table, got)
		}
	}
}

func TestRepositoryPlanRevisionPinsModelChannelAndMappingRevisions(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	createRunGraph(t, repository, fixture)

	model := fixture.model
	model.EntityMeta = nextEntityMeta(t, model.EntityMeta, repositoryEpoch.Add(time.Minute))
	model.Name = "Fixture model v2"
	if err := repository.UpdateModel(ctx, 1, model); err != nil {
		t.Fatalf("UpdateModel() error = %v", err)
	}
	channel := fixture.channel
	channel.EntityMeta = nextEntityMeta(t, channel.EntityMeta, repositoryEpoch.Add(time.Minute))
	channel.Name = "Fixture channel v2"
	channel.BaseURL = "https://v2.example.test/v1"
	if err := repository.UpdateChannel(ctx, 1, channel); err != nil {
		t.Fatalf("UpdateChannel() error = %v", err)
	}
	mapping := fixture.mapping
	mapping.EntityMeta = nextEntityMeta(t, mapping.EntityMeta, repositoryEpoch.Add(time.Minute))
	mapping.UpstreamModelName = "upstream-fixture-v2"
	if err := repository.UpdateChannelModel(ctx, 1, mapping); err != nil {
		t.Fatalf("UpdateChannelModel() error = %v", err)
	}

	snapshot := fixture.run.Snapshot()
	snapshot.Model.Revision = 2
	snapshot.Model.Name = model.Name
	snapshot.Channel.Revision = 2
	snapshot.Channel.Name = channel.Name
	snapshot.Channel.BaseURL = channel.BaseURL
	snapshot.Channel.UpstreamModelName = mapping.UpstreamModelName
	drifted, err := domain.NewRun(entityMeta("10000000-0000-4000-8000-000000000036", 1), fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	if err := repository.CreateRun(ctx, drifted); err == nil {
		t.Fatal("CreateRun() accepted revisions newer than its pinned plan revision")
	}
	if _, err := repository.GetRun(ctx, drifted.Meta().ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetRun() after rejected snapshot error = %v, want ErrNotFound", err)
	}
	pinned, err := domain.NewRun(
		entityMeta("10000000-0000-4000-8000-000000000037", 1),
		fixture.plan.ID,
		fixture.run.Snapshot(),
	)
	if err != nil {
		t.Fatalf("NewRun(pinned) error = %v", err)
	}
	if err := repository.CreateRun(ctx, pinned); err != nil {
		t.Fatalf("CreateRun() with the original pinned revisions error = %v", err)
	}
}

func createRunGraph(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) {
	t.Helper()
	ctx := context.Background()
	steps := []struct {
		name string
		do   func() error
	}{
		{"credential", func() error { return repository.CreateCredentialRef(ctx, fixture.credential) }},
		{"model", func() error { return repository.CreateModel(ctx, fixture.model) }},
		{"channel", func() error { return repository.CreateChannel(ctx, fixture.channel) }},
		{"mapping", func() error { return repository.CreateChannelModel(ctx, fixture.mapping) }},
		{"case", func() error { return repository.CreateTestCase(ctx, fixture.testCase) }},
		{"suite", func() error { return repository.CreateSuite(ctx, fixture.suite) }},
		{"plan", func() error { return repository.CreatePlan(ctx, fixture.plan) }},
		{"run", func() error { return repository.CreateRun(ctx, fixture.run) }},
	}
	for _, step := range steps {
		if err := step.do(); err != nil {
			t.Fatalf("create %s: %v", step.name, err)
		}
	}
}

func TestRepositoryUpdateRunPersistsStructuredFailure(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)

	previous := run.Meta().Revision
	failed, err := run.Fail(domain.RunFailure{
		Phase: "execute", ErrorCode: "run_execution_failed",
	}, repositoryEpoch.Add(time.Duration(previous)*time.Minute))
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if err := repository.UpdateRun(context.Background(), previous, failed); err != nil {
		t.Fatalf("UpdateRun(failed) error = %v", err)
	}

	stored, err := repository.GetRun(context.Background(), failed.Meta().ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if !reflect.DeepEqual(stored, failed) {
		t.Fatalf("GetRun() = %#v, want %#v", stored, failed)
	}
}

func transitionRun(t *testing.T, repository *persistence.Repository, run domain.Run, statuses ...domain.RunStatus) domain.Run {
	t.Helper()
	for _, status := range statuses {
		previous := run.Meta().Revision
		var err error
		run, err = run.Transition(status, repositoryEpoch.Add(time.Duration(previous)*time.Minute))
		if err != nil {
			t.Fatalf("Transition(%s): %v", status, err)
		}
		if err := repository.UpdateRun(context.Background(), previous, run); err != nil {
			t.Fatalf("UpdateRun(%s): %v", status, err)
		}
	}
	return run
}

func createRunningOutputs(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) domain.Run {
	t.Helper()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(context.Background(), fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence() error = %v", err)
	}
	if err := repository.AppendResult(context.Background(), fixture.result); err != nil {
		t.Fatalf("AppendResult() error = %v", err)
	}
	return run
}

func nextEntityMeta(t *testing.T, meta domain.EntityMeta, at time.Time) domain.EntityMeta {
	t.Helper()
	next, err := meta.NextRevision(at)
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	return next
}

func openHardeningRepository(t *testing.T) (string, *persistence.Repository, repositoryFixture) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository-hardening.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return path, reopenHardeningRepository(t, path), newRepositoryFixture(t)
}

func reopenHardeningRepository(t *testing.T, path string) *persistence.Repository {
	t.Helper()
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	return repository
}

func closeForTamper(t *testing.T, repository *persistence.Repository) {
	t.Helper()
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func tamper(t *testing.T, path, statement string, arguments ...any) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("tamper repository row: %v", err)
	}
}

func tamperWithoutForeignKeys(t *testing.T, path, statement string, arguments ...any) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys for tamper: %v", err)
	}
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("tamper repository row: %v", err)
	}
}
