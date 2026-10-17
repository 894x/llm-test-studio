package sqlite_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func TestLocalExecutorIdentityPersistsAcrossRepositoriesAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "local-identity.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "identity-test"}); err != nil {
		t.Fatal(err)
	}
	first, second := reopenHardeningRepository(t, path), reopenHardeningRepository(t, path)
	defer first.Close()
	defer second.Close()
	results := make(chan jobhost.LocalIdentity, 8)
	failures := make(chan error, 8)
	var wait sync.WaitGroup
	for index := range 8 {
		repository := first
		if index%2 != 0 {
			repository = second
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			identity, err := repository.LocalExecutorIdentity(t.Context())
			if err != nil {
				failures <- err
				return
			}
			results <- identity
		}()
	}
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var expected jobhost.LocalIdentity
	count := 0
	for identity := range results {
		if identity.Validate() != nil {
			t.Fatal("invalid local executor identity")
		}
		if count == 0 {
			expected = jobhost.LocalIdentity{NodeID: identity.NodeID, InstallationID: identity.InstallationID,
				CommitmentKey: bytes.Clone(identity.CommitmentKey)}
		}
		if identity.NodeID != expected.NodeID || identity.InstallationID != expected.InstallationID ||
			!bytes.Equal(identity.CommitmentKey, expected.CommitmentKey) {
			t.Fatal("independent initializers minted different identities")
		}
		identity.Clear()
		count++
	}
	defer expected.Clear()
	if count != 8 {
		t.Fatal("an initializer did not return")
	}
	_ = first.Close()
	_ = second.Close()
	reopened := reopenHardeningRepository(t, path)
	defer reopened.Close()
	identity, err := reopened.LocalExecutorIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Clear()
	if identity.NodeID != expected.NodeID || identity.InstallationID != expected.InstallationID ||
		!bytes.Equal(identity.CommitmentKey, expected.CommitmentKey) {
		t.Fatal("reopen replaced the persistent local identity")
	}
	if _, err := json.Marshal(identity); err == nil {
		t.Fatal("private executor key was allowed into a public JSON document")
	}
	if !strings.Contains(fmt.Sprintf("%#v", identity), "redacted") {
		t.Fatal("private executor identity was not redacted")
	}
}

func TestLocalExecutorIdentityRefusesMissingOwnerBesideExistingWork(t *testing.T) {
	for _, outbox := range []bool{false, true} {
		t.Run(fmt.Sprint(outbox), func(t *testing.T) {
			repository := openOperationalRepository(t)
			defer repository.Close()
			spec := jobSpecForRun(t, runWithCompleteSnapshot(t))
			if outbox {
				if _, err := repository.CreateSubmission(t.Context(), submissionFixture(t, spec)); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				identity, err := repository.LocalExecutorIdentity(t.Context())
				if !errors.Is(err, persistence.ErrCorrupt) || len(identity.CommitmentKey) != 0 {
					t.Fatal("startup replaced a missing execution owner")
				}
			}
			if outbox {
				if _, err := repository.GetSubmission(t.Context(), spec.JobID); err != nil {
					t.Fatal("missing identity rejection modified the outbox")
				}
			} else if _, err := repository.GetJob(t.Context(), spec.JobID); err != nil {
				t.Fatal("missing identity rejection modified the accepted job")
			}
		})
	}
}

func TestLocalExecutorIdentityRejectsCorruptionWithoutRewriting(t *testing.T) {
	for name, statement := range map[string]string{
		"uuid":      "UPDATE local_executor_identity SET node_id = 'bad'",
		"oversized": "UPDATE local_executor_identity SET node_id = CAST(zeroblob(9437184) AS TEXT)",
		"key_type":  "UPDATE local_executor_identity SET commitment_key = '01234567890123456789012345678901'",
		"zero_key":  "UPDATE local_executor_identity SET commitment_key = zeroblob(32)",
		"singleton": "UPDATE local_executor_identity SET singleton = 2",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "corrupt-identity.db")
			if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "identity-test"}); err != nil {
				t.Fatal(err)
			}
			repository := reopenHardeningRepository(t, path)
			defer repository.Close()
			identity, err := repository.LocalExecutorIdentity(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			identity.Clear()
			db := openDatabase(t, path)
			defer db.Close()
			if _, err := db.ExecContext(t.Context(), "PRAGMA ignore_check_constraints = ON"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
			read := func() []byte {
				var singleton int
				var nodeID, installationID, key []byte
				if err := db.QueryRowContext(t.Context(), `SELECT singleton, node_id, installation_id, commitment_key
					FROM local_executor_identity`).Scan(
					&singleton,
					&nodeID,
					&installationID,
					&key,
				); err != nil {
					t.Fatal(err)
				}
				value, err := json.Marshal([]any{singleton, nodeID, installationID, key})
				if err != nil {
					t.Fatal(err)
				}
				return value
			}
			before := read()
			for range 2 {
				identity, err := repository.LocalExecutorIdentity(t.Context())
				if !errors.Is(err, persistence.ErrCorrupt) || len(identity.CommitmentKey) != 0 {
					t.Fatal("corrupt identity returned a usable key")
				}
			}
			if !bytes.Equal(before, read()) {
				t.Fatal("corrupt identity was rewritten")
			}
		})
	}
}

type recoveryOwnerClock struct{}

func (recoveryOwnerClock) Now() time.Time { return time.Now().UTC() }

func TestUnmanagedRunRecoveryDoesNotMutateJobHostOwnedRuns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery-owner.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "identity-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	defer repository.Close()
	owned := runWithCompleteSnapshot(t)
	spec := jobSpecForRun(t, owned)
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	// A corrupt nullable owner must not hide unrelated runs from recovery.
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(),
		`INSERT INTO execution_job_runs (run_id, job_id, position) VALUES (NULL, ?, 1)`, spec.JobID,
	); err != nil {
		t.Fatal(err)
	}
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	unmanaged, err := domain.NewRun(meta, owned.PlanID(), owned.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateRun(t.Context(), unmanaged); err != nil {
		t.Fatal(err)
	}
	eligible, err := repository.ListRecoveryRuns(t.Context())
	if err != nil || len(eligible) != 1 || eligible[0].Meta().ID != unmanaged.Meta().ID {
		t.Fatal("unmanaged recovery selected another execution owner")
	}
	if err := runs.RecoverInterrupted(t.Context(), repository, recoveryOwnerClock{}); err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetRun(t.Context(), owned.Meta().ID)
	if err != nil || current.Status() != domain.RunQueued || current.Meta().Revision != owned.Meta().Revision {
		t.Fatal("unmanaged recovery changed an accepted JobHost run")
	}
	all, err := repository.ListRuns(t.Context())
	if err != nil || len(all) != 2 {
		t.Fatal("ordinary run listing hid a JobHost run")
	}
}

func TestLocalExecutorIdentityPreservesCanceledContext(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.LocalExecutorIdentity(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("local identity hid caller cancellation")
	}
}
