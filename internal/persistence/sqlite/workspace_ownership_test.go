package sqlite_test

import (
	"errors"
	"fmt"
	"testing"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestWorkspaceRunProjectionsResolveLocalAndMirroredJobOwners(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, comparison := range []bool{false, true} {
			t.Run(fmt.Sprintf("remote=%v/comparison=%v", remote, comparison), func(t *testing.T) {
				node := openOperationalRepository(t)
				defer node.Close()
				spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
				if comparison {
					spec = comparisonJobSpec(t)
				}
				if remote {
					spec = remoteSpec(spec)
				}
				if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
					t.Fatal(err)
				}
				reader := node
				if remote {
					reader = openOperationalRepository(t)
					defer reader.Close()
					applyRemoteEvents(t, reader, remoteEvents(t, node, spec))
				}
				projections, err := reader.ListRunProjections(t.Context())
				if err != nil || len(projections) != len(spec.PreparedRuns()) {
					t.Fatalf("owned run projections = %#v, error = %v", projections, err)
				}
				for _, projection := range projections {
					if projection.JobID != spec.JobID {
						t.Fatalf(
							"run %q owner = %q, want %q",
							projection.Meta.ID,
							projection.JobID,
							spec.JobID,
						)
					}
				}
			})
		}
	}
}

func TestWorkspaceRunProjectionsKeepUnmanagedRunsReadable(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	run := runWithCompleteSnapshot(t)
	if err := repository.CreateRun(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	projections, err := repository.ListRunProjections(t.Context())
	if err != nil || len(projections) != 1 || projections[0].JobID != "" {
		t.Fatalf("unmanaged run projections = %#v, error = %v", projections, err)
	}
}

func TestWorkspaceRunProjectionsRejectCorruptJobOwnership(t *testing.T) {
	for _, remote := range []bool{false, true} {
		for _, mutation := range []string{"position", "job identity", "run identity"} {
			t.Run(fmt.Sprintf("remote=%v/%s", remote, mutation), func(t *testing.T) {
				fixture := newSubmittedReadFixture(t, remote)
				ownerTable, jobTable, jobColumn := "execution_job_runs", "execution_jobs", "id"
				if remote {
					ownerTable, jobTable, jobColumn = "remote_job_runs", "remote_job_mirrors", "job_id"
				}
				statement := fmt.Sprintf("UPDATE %s SET position = 1 WHERE job_id = ?", ownerTable)
				if mutation != "position" {
					path := "$.job_id"
					if mutation == "run identity" {
						path = "$.spec.run.meta.id"
					}
					statement = fmt.Sprintf(
						"UPDATE %s SET spec_json = json_set(spec_json, '%s', '%s') WHERE %s = ?",
						jobTable,
						path,
						"99999999-9999-4999-8999-999999999999",
						jobColumn,
					)
				}
				if _, err := fixture.db.ExecContext(t.Context(), statement, fixture.spec.JobID); err != nil {
					t.Fatal(err)
				}
				column, table, idColumn := "spec_json", jobTable, jobColumn
				if mutation == "position" {
					column, table, idColumn = "position", ownerTable, "job_id"
				}
				readStatement := fmt.Sprintf("SELECT %s FROM %s WHERE %s = ?", column, table, idColumn)
				var before, after string
				if err := fixture.db.QueryRowContext(t.Context(), readStatement, fixture.spec.JobID).Scan(&before); err != nil {
					t.Fatal(err)
				}
				_, err := fixture.repository.ListRunProjections(t.Context())
				if !errors.Is(err, persistence.ErrCorrupt) {
					t.Fatalf("corrupt job ownership error = %v, want ErrCorrupt", err)
				}
				if err := fixture.db.QueryRowContext(t.Context(), readStatement, fixture.spec.JobID).Scan(&after); err != nil {
					t.Fatal(err)
				}
				if before != after {
					t.Fatal("rejecting corrupt ownership changed the stored input")
				}
			})
		}
	}
}

func TestWorkspaceRunProjectionsRejectDuplicateJobNamespacesAndOwners(t *testing.T) {
	for _, duplicateClaim := range []bool{false, true} {
		t.Run(fmt.Sprintf("duplicate-claim=%v", duplicateClaim), func(t *testing.T) {
			fixture := newSubmittedReadFixture(t, false)
			mirrorJobID := fixture.spec.JobID
			if duplicateClaim {
				mirrorJobID = "99999999-9999-4999-8999-999999999999"
			}
			_, err := fixture.db.ExecContext(
				t.Context(),
				`INSERT INTO remote_job_mirrors(
                job_id, configuration_id, node_id, installation_id, spec_sha256, spec_json,
                snapshot_json, applied_sequence)
                SELECT ?, ?, ?, installation_id, spec_sha256, json_set(spec_json,'$.job_id',?),
                snapshot_json, last_sequence
                FROM execution_jobs WHERE id = ?`,
				mirrorJobID,
				remoteOrigin.ConfigurationID,
				remoteOrigin.NodeID,
				mirrorJobID,
				fixture.spec.JobID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if duplicateClaim {
				_, err := fixture.db.ExecContext(
					t.Context(),
					`INSERT INTO remote_job_runs(run_id, job_id, position)
                    SELECT run_id, ?, position FROM execution_job_runs WHERE job_id = ?`,
					mirrorJobID,
					fixture.spec.JobID,
				)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := fixture.repository.ListRunProjections(t.Context()); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("duplicate ownership error = %v, want ErrCorrupt", err)
			}
		})
	}
}
