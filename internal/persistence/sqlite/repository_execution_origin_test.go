package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestRunExecutionOriginRequiresCurrentRunAndPreservesUnmanagedNull(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	run := runWithCompleteSnapshot(t)
	if _, err := repository.RunExecutionOrigin(t.Context(), run.Meta().ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing run was treated as unmanaged: %v", err)
	}
	if err := repository.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	if origin, err := repository.RunExecutionOrigin(t.Context(), run.Meta().ID); err != nil || origin != nil {
		t.Fatalf("current unmanaged run acquired an inferred executor: origin=%v error=%v", origin, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.RunExecutionOrigin(ctx, run.Meta().ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation became unmanaged: %v", err)
	}
	if _, err := repository.RunExecutionOrigin(nil, run.Meta().ID); err == nil {
		t.Fatal("nil context was accepted")
	}
}

func TestRunExecutionOriginFreezesAcceptedVersionAcrossCompatibleRetryAndMirror(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "owned", true: "mirror"}[remote], func(t *testing.T) {
			node := openOperationalRepository(t)
			defer node.Close()
			run := runWithCompleteSnapshot(t)
			spec := jobSpecForRun(t, run)
			if remote {
				spec = remoteSpec(spec)
			}
			input := jobAcceptance(spec)
			input.Origin.RunnerVersion = "accepted-runner-old"
			accepted, _, err := node.AcceptJob(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			input.Origin.RunnerVersion = "upgraded-compatible-runner"
			input.AcceptedAt = input.AcceptedAt.Add(time.Hour)
			retry, created, err := node.AcceptJob(t.Context(), input)
			if err != nil || created || retry.Receipt.Origin != accepted.Receipt.Origin {
				t.Fatalf("retry replaced history with current runner: created=%v error=%v", created, err)
			}
			advanceJobFixture(t, node, accepted, domain.RunRunning)
			reader := node
			if remote {
				reader = openOperationalRepository(t)
				defer reader.Close()
				applyRemoteEvents(t, reader, remoteEvents(t, node, spec))
			}
			origin, err := reader.RunExecutionOrigin(t.Context(), run.Meta().ID)
			if err != nil || origin == nil || *origin != accepted.Receipt.Origin {
				t.Fatalf("accepted executor was not preserved: origin=%v error=%v", origin, err)
			}
			origin.RunnerVersion = "caller mutation"
			stored, err := reader.RunExecutionOrigin(t.Context(), run.Meta().ID)
			if err != nil || stored.RunnerVersion != "accepted-runner-old" {
				t.Fatalf("caller mutation changed executor: %v", err)
			}
		})
	}
}

func TestExecutionOriginTamperingFailsReadsCommandsEventsAndStateWrites(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	accepted := fixture.snapshot
	command := commandFor(accepted, jobcontract.CommandCancel)
	if _, _, err := fixture.executor.ApplyJobCommand(t.Context(), command, accepted.UpdatedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_job_commands SET document_json =
		json_set(document_json, '$.snapshot.receipt.origin.runner_version', 'forged-runner') WHERE id = ?`, command.CommandID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.GetJobCommand(t.Context(), command.CommandID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("changed command source remained readable: %v", err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_job_events SET document_json =
		json_set(document_json, '$.payload.receipt.origin.os', 'forged-os') WHERE job_id = ? AND kind = 'state'`, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.JobEvents(t.Context(), fixture.spec.JobID, 0, 200); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("changed state event source remained readable: %v", err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_jobs SET snapshot_json =
		json_set(snapshot_json, '$.receipt.origin.arch', 'forged-arch') WHERE id = ?`, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.executor.GetJob(t.Context(), fixture.spec.JobID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("changed snapshot source remained readable: %v", err)
	}
	if _, err := fixture.executor.RunExecutionOrigin(t.Context(), fixture.spec.Spec.Run.Meta.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("changed snapshot source became unmanaged: %v", err)
	}
	if _, err := fixture.executor.ChangeJob(t.Context(), jobhost.Change{
		JobID: fixture.spec.JobID, ExpectedRevision: 2, Status: domain.RunCancelled,
		At: accepted.UpdatedAt.Add(time.Minute),
	}); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("state write accepted forged source: %v", err)
	}
}

func TestWarmAcceptedOriginCacheRejectsSameLengthJournalChangesAcrossReopen(t *testing.T) {
	fixture := newSubmittedReadFixture(t, false)
	if _, err := fixture.executor.GetJob(t.Context(), fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM execution_job_events
		WHERE job_id = ? AND sequence = 1`, fixture.spec.JobID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	changed := bytes.Replace(original, []byte(`"test-runner-1"`), []byte(`"test-runner-2"`), 1)
	if len(changed) != len(original) || bytes.Equal(changed, original) {
		t.Fatal("test must mutate accepted bytes without changing their size")
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_job_events SET document_json = ?
		WHERE job_id = ? AND sequence = 1`, changed, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := fixture.executor.GetJob(t.Context(), fixture.spec.JobID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("warm cache accepted changed admission bytes: %v", err)
		}
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_job_events SET document_json = ?
		WHERE job_id = ? AND sequence = 1`, original, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	stored, err := fixture.executor.GetJob(t.Context(), fixture.spec.JobID)
	if err != nil || stored.Receipt.Origin != fixture.snapshot.Receipt.Origin {
		t.Fatal("a rejected row poisoned later validation of the original bytes")
	}
	var path string
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err := fixture.repository.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	got, err := reopened.GetJob(t.Context(), fixture.spec.JobID)
	if err != nil || got.Receipt.Origin != fixture.snapshot.Receipt.Origin {
		t.Fatalf("independent reopened repository changed the durable source: %v", err)
	}
}

func TestWarmRunOriginCacheStillValidatesSpecSnapshotAndPosition(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, target := range []string{"spec", "snapshot", "position"} {
			name := map[bool]string{false: "owned", true: "mirror"}[remote] + "/" + target
			t.Run(name, func(t *testing.T) {
				fixture := newSubmittedReadFixture(t, remote)
				runID := fixture.spec.Spec.Run.Meta.ID
				if _, err := fixture.repository.RunExecutionOrigin(t.Context(), runID); err != nil {
					t.Fatal(err)
				}
				originalName := fixture.spec.Spec.Run.Snapshot.Entries[0].CaseDefinitions[0].Name
				changedName := "X" + originalName[1:]
				switch target {
				case "spec":
					query := `UPDATE execution_jobs SET spec_json = json_set(spec_json,
						'$.spec.run.snapshot.entries[0].case_definitions[0].name', ?) WHERE id = ?`
					if remote {
						query = `UPDATE remote_job_mirrors SET spec_json = json_set(spec_json,
							'$.spec.run.snapshot.entries[0].case_definitions[0].name', ?) WHERE job_id = ?`
					}
					if _, err := fixture.db.ExecContext(t.Context(), query, changedName, fixture.spec.JobID); err != nil {
						t.Fatal(err)
					}
				case "snapshot":
					if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
						t.Fatal(err)
					}
					if _, err := fixture.db.ExecContext(t.Context(), `UPDATE execution_run_revisions
						SET snapshot_json = json_set(snapshot_json, '$.entries[0].case_definitions[0].name', ?),
						document_json = json_set(document_json, '$.plan_snapshot.entries[0].case_definitions[0].name', ?)
						WHERE run_id = ? AND revision = 1`, changedName, changedName, runID); err != nil {
						t.Fatal(err)
					}
					if _, err := fixture.repository.GetRun(t.Context(), runID); err != nil {
						t.Fatalf("tamper fixture must remain a valid current run: %v", err)
					}
				case "position":
					query := `UPDATE execution_job_runs SET position = 1 WHERE run_id = ?`
					if remote {
						query = `UPDATE remote_job_runs SET position = 1 WHERE run_id = ?`
					}
					if _, err := fixture.db.ExecContext(t.Context(), query, runID); err != nil {
						t.Fatal(err)
					}
				}
				for range 2 {
					if _, err := fixture.repository.RunExecutionOrigin(t.Context(), runID); !errors.Is(err, persistence.ErrCorrupt) {
						t.Fatalf("warm cache bypassed changed %s: %v", target, err)
					}
				}
			})
		}
	}
}

func TestRunOriginSnapshotBindingPreservesNativeObjectKeysAndLargeIntegers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native-origin.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "origin-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	run := runWithCompleteSnapshot(t)
	snapshot := run.Snapshot()
	definition := &snapshot.Entries[0].CaseDefinitions[0]
	definition.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{
		"request":{"body":{"seed":9007199254740993,"messages":[{"role":"user","content":"hello"}]}},
		"inputs":{},"assertions":[{"value":200,"source":"http.status","operator":"equals","id":"status"}]}`)
	run, err := domain.NewRun(run.Meta(), run.PlanID(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	input := jobAcceptance(jobSpecForRun(t, run))
	if _, _, err := repository.AcceptJob(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		origin, err := repository.RunExecutionOrigin(t.Context(), run.Meta().ID)
		if err != nil || origin == nil || *origin != input.Origin {
			t.Fatalf("canonical snapshot binding changed native JSON semantics: %v", err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
		t.Fatal(err)
	}
	const exactInteger, roundedInteger = "9007199254740993", "9007199254740992"
	var containsExact bool
	if err := db.QueryRowContext(t.Context(), `SELECT instr(snapshot_json, ?) > 0
		FROM execution_run_revisions WHERE run_id = ? AND revision = 1`, exactInteger, run.Meta().ID).Scan(&containsExact); err != nil || !containsExact {
		t.Fatal("native JSON fixture lost its exact integer before tampering")
	}
	if _, err := db.ExecContext(
		t.Context(),
		`UPDATE execution_run_revisions SET snapshot_json = replace(snapshot_json, ?, ?),
		document_json = replace(document_json, ?, ?) WHERE run_id = ? AND revision = 1`,
		exactInteger,
		roundedInteger,
		exactInteger,
		roundedInteger,
		run.Meta().ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRun(t.Context(), run.Meta().ID); err != nil {
		t.Fatalf("modified integer must remain a valid current run: %v", err)
	}
	for range 2 {
		if _, err := repository.RunExecutionOrigin(t.Context(), run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("warm snapshot digest rounded away an integer change: %v", err)
		}
	}
}

func TestRunExecutionOriginRejectsDuplicateOwnerNamespacesAndInvalidMirrorSource(t *testing.T) {
	fixture := newSubmittedReadFixture(t, true)
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE remote_job_mirrors SET snapshot_json =
		json_set(snapshot_json, '$.receipt.origin.runner_version', 'forged-runner') WHERE job_id = ?`, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.RunExecutionOrigin(t.Context(), fixture.spec.Spec.Run.Meta.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("mirror source forgery became an origin: %v", err)
	}
	digest, err := fixture.spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	_, err = fixture.db.ExecContext(t.Context(), `INSERT INTO execution_jobs(
        id, installation_id, engine_build_id, kind, revision, status, accepted_at,
        spec_sha256, secret_commitment, spec_json, snapshot_json, last_sequence)
        VALUES(?, ?, ?, ?, 1, 'queued', ?, ?, ?, ?, ?, 1)`,
		fixture.spec.JobID, fixture.spec.NodeInstallationID, fixture.spec.EngineBuildID, fixture.spec.Kind,
		fixture.snapshot.Receipt.AcceptedAt.Format(time.RFC3339Nano), digest, digest,
		canonicalFixtureJSON(t, fixture.spec), canonicalFixtureJSON(t, fixture.snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `INSERT INTO execution_job_runs(run_id, job_id, position)
        VALUES(?, ?, 0)`, fixture.spec.Spec.Run.Meta.ID, fixture.spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.RunExecutionOrigin(t.Context(), fixture.spec.Spec.Run.Meta.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("duplicate ownership namespace accepted: %v", err)
	}
}

func TestManagedRunReportRequiresExactOriginBeforeSealingAndReadingDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed-report-origin.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "origin-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	db := openDatabase(t, path)
	defer db.Close()
	output := newRepositoryFixture(t)
	accepted, _, err := repository.AcceptJob(t.Context(), jobAcceptance(jobSpecForRun(t, output.run)))
	if err != nil {
		t.Fatal(err)
	}
	running := transitionRun(t, repository, output.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(t.Context(), output.evidence); err != nil {
		t.Fatal(err)
	}
	if err := repository.AppendResults(t.Context(), output.result); err != nil {
		t.Fatal(err)
	}
	transitionRun(t, repository, running, domain.RunCompleted)
	if err := repository.CreateReport(t.Context(), output.report); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("managed report with null origin sealed run: %v", err)
	}
	origin := accepted.Receipt.Origin
	wrong := origin
	wrong.RunnerVersion = "prepared-or-current-version"
	output.report.ExecutionOrigin = &wrong
	if err := repository.CreateReport(t.Context(), output.report); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("managed report with another version sealed run: %v", err)
	}
	output.report.ExecutionOrigin = &origin
	if err := repository.CreateReport(t.Context(), output.report); err != nil {
		t.Fatalf("rejected report mutated the run seal: %v", err)
	}
	if _, err := repository.ListReportProjections(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.ListReportProjections(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE reports SET document_json =
		json_set(document_json, '$.execution_origin.runner_version', 'forged-runner') WHERE id = ?`, output.report.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetReport(t.Context(), output.report.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("report source forgery remained readable: %v", err)
	}
	if _, err := repository.ListReportProjections(t.Context()); err != nil {
		t.Fatalf("report summaries scanned deep origin relations: %v", err)
	}
}

func TestManagedPerformanceArchiveRequiresAcceptedOriginAndRejectsMissingFieldWithoutMutation(t *testing.T) {
	fixture := newPerformanceStorageFixture(t)
	report := fixture.checkpoints[len(fixture.checkpoints)-1].Report
	report.Archived, report.ArchiveStatus = true, quicktest.PerformanceArchiveArchived
	report.GeneratedAt = repositoryEpoch.Add(time.Hour).Format(time.RFC3339Nano)
	if err := fixture.repository.SaveQuickPerformanceReport(t.Context(), report); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("managed archive accepted null origin: %v", err)
	}
	origin := executionOrigin(fixture.spec)
	report.ExecutionOrigin = &origin
	if err := fixture.repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.GetQuickPerformanceReport(t.Context(), report.ReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `DROP TRIGGER quick_performance_reports_no_update`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE quick_performance_reports SET document_json =
		json_set(document_json, '$.execution_origin.runner_version', 'forged-runner') WHERE id = ?`, report.ReportID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.GetQuickPerformanceReport(t.Context(), report.ReportID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("archive accepted forged source: %v", err)
	}
	if _, err := fixture.db.ExecContext(t.Context(), `UPDATE quick_performance_reports SET document_json =
		json_remove(document_json, '$.execution_origin') WHERE id = ?`, report.ReportID); err != nil {
		t.Fatal(err)
	}
	var before, after []byte
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM quick_performance_reports WHERE id = ?`, report.ReportID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repository.GetQuickPerformanceReport(t.Context(), report.ReportID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("old missing-source archive was accepted: %v", err)
	}
	if err := fixture.db.QueryRowContext(t.Context(), `SELECT document_json FROM quick_performance_reports WHERE id = ?`, report.ReportID).Scan(&after); err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected old archive was mutated")
	}
}

func TestUnmanagedPerformanceArchiveCannotClaimAnExecutor(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unmanaged-origin.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "origin-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	report := validQuickPerformanceReport("77777777-7777-4777-8777-777777777774", "2026-08-31T15:33:00Z")
	origin := executionOrigin(jobSpecForRun(t, runWithCompleteSnapshot(t)))
	report.ExecutionOrigin = &origin
	if err := repository.SaveQuickPerformanceReport(t.Context(), report); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("unmanaged archive claimed an unowned executor: %v", err)
	}
	report.ExecutionOrigin = nil
	if err := repository.SaveQuickPerformanceReport(t.Context(), report); err != nil {
		t.Fatal(err)
	}
}
