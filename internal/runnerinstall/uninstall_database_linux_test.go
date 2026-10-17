package runnerinstall

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

func writeUninstallMaintenance(t *testing.T, path string, request UninstallRequest) {
	t.Helper()
	record := jobcontract.MaintenanceRecord{Contract: request.Contract, NodeInstallationID: request.InstallationID,
		OperationID: request.OperationID, Paused: true, ChangedAt: time.Now().UTC()}
	document, _ := json.Marshal(record)
	if err := os.WriteFile(filepath.Join(path, "maintenance.json"), document, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestUninstallReadOnlyProofSeesWALAndTerminalUnsettledFacts(t *testing.T) {
	paths := layout{state: t.TempDir()}
	if err := os.Chmod(paths.state, 0700); err != nil {
		t.Fatal(err)
	}
	owner := account{uid: os.Geteuid(), gid: os.Getegid()}
	request := uninstallRequestFixture()
	runtime, err := runnerstate.Open(paths.state)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	writeUninstallMaintenance(t, paths.state, request)
	path := filepath.Join(paths.state, "runner.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "uninstall-proof"}); err != nil {
		t.Fatal(err)
	}
	control, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	if _, err := control.ExecContext(t.Context(), `PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0`); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	if err := confirmUninstallIdle(
		t.Context(),
		paths,
		owner,
		request,
	); err != nil {
		t.Fatalf("empty current database was not idle: %v", err)
	}
	// Hold the old read snapshot so job facts remain in WAL, never checkpointed
	// into the baseline main file. This is local fixture data, no executor runs.
	held, err := control.BeginTx(t.Context(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback()
	var count int
	if err := held.QueryRowContext(t.Context(), `SELECT count(*) FROM execution_jobs`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	before := fixtureBytes(t, path)
	command := quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "http://127.0.0.1:1/v1", APIKey: "uninstall-fixture-only", ModelID: "fixture",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 30000, InputTokens: 32, OutputTokens: 2,
	}
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(t.Context(), command)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	spec := jobcontract.JobSpec{Contract: request.Contract, JobID: request.OperationID,
		NodeInstallationID: request.InstallationID, EngineBuildID: request.ExpectedBuildID,
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: request.NodeID},
		Kind:   jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{}}
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: request.NodeID, InstallationID: request.InstallationID,
		EngineBuildID: request.ExpectedBuildID, RunnerVersion: "uninstall-proof", OS: "linux", Arch: "amd64"}
	at := time.Now().UTC()
	accepted, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
		Spec: spec, Origin: origin, SecretCommitment: strings.Repeat("c", 64), AcceptedAt: at,
		ContentClaims: []jobcontent.Claim{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := confirmUninstallIdle(
		t.Context(),
		paths,
		owner,
		request,
	); !errors.Is(err, ErrBusy) {
		t.Fatalf("WAL-only queued job was overlooked: %v", err)
	}
	failure := &domain.RunFailure{Phase: "runner", ErrorCode: "runner_interrupted"}
	change := jobhost.Change{JobID: spec.JobID, ExpectedRevision: accepted.Receipt.Revision,
		Status: domain.RunFailed, Failure: failure, At: at.Add(time.Second)}
	if _, err := repository.ChangeJob(t.Context(), change); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SealPerformanceResult(t.Context(), spec.JobID, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := confirmUninstallIdle(
		t.Context(),
		paths,
		owner,
		request,
	); !errors.Is(err, ErrBusy) {
		t.Fatalf("terminal and sealed but unsettled job was overlooked: %v", err)
	}
	if err := repository.MarkJobSettled(t.Context(), spec.JobID); err != nil {
		t.Fatal(err)
	}
	if err := confirmUninstallIdle(
		t.Context(),
		paths,
		owner,
		request,
	); err != nil {
		t.Fatalf("fully settled current facts were not accepted: %v", err)
	}
	if !bytes.Equal(before, fixtureBytes(t, path)) {
		t.Fatal("fixture was checkpointed or read-only verification wrote the main database")
	}
	if info, err := os.Stat(path + "-wal"); err != nil || info.Size() == 0 {
		t.Fatal("proof did not exercise retained WAL facts")
	}
}

func TestUninstallDatabaseProofRejectsMissingCorruptAndForeignMaintenance(t *testing.T) {
	for _, scenario := range []string{"missing", "corrupt", "unknown_schema", "foreign_gate", "null_gate", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			paths := layout{state: t.TempDir()}
			owner := account{uid: os.Geteuid(), gid: os.Getegid()}
			request := uninstallRequestFixture()
			writeUninstallMaintenance(t, paths.state, request)
			path := filepath.Join(paths.state, "runner.db")
			if scenario != "missing" {
				if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "uninstall-proof"}); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "corrupt":
				_ = os.WriteFile(path, []byte("preserve unsupported bytes"), 0600)
			case "unknown_schema":
				db, _ := sql.Open("sqlite", path)
				_, err := db.ExecContext(t.Context(), `
					CREATE TABLE unknown_data(value TEXT);
					INSERT INTO unknown_data VALUES('retain')`)
				_ = db.Close()
				if err != nil {
					t.Fatal(err)
				}
			case "foreign_gate":
				other := request
				other.OperationID = "21905cf6-f224-4097-9d98-90026e5c392b"
				writeUninstallMaintenance(t, paths.state, other)
			case "null_gate":
				_ = os.WriteFile(filepath.Join(paths.state, "maintenance.json"), []byte("null"), 0600)
			}
			before, _ := os.ReadFile(path)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			if err := confirmUninstallIdle(
				ctx,
				paths,
				owner,
				request,
			); err == nil {
				t.Fatal("unknown usage or storage was accepted as idle")
			}
			after, _ := os.ReadFile(path)
			if !bytes.Equal(before, after) {
				t.Fatal("rejected storage was reset or changed")
			}
		})
	}
}
