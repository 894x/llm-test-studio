package sqlite

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestRemoteSyncPriorityStatementRetainsSnapshotAcrossConcurrentRepositoryWrite(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "priority-snapshot.db")
	if err := Migrate(ctx, path, MigrateOptions{AppVersion: "priority-snapshot-test"}); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRepository(ctx, path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	writer, err := OpenRepository(ctx, path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	var journal string
	if err := writer.db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&journal); err != nil || journal != "wal" {
		t.Fatalf("enable WAL in isolated test database: %q, %v", journal, err)
	}
	origin := remoteresults.Origin{
		ConfigurationID: "81000000-0000-4000-8000-000000000001",
		NodeID:          "81000000-0000-4000-8000-000000000002",
		InstallationID:  "72000000-0000-4000-8000-000000000002",
	}
	ids := []string{"84000000-0000-4000-8000-000000000001", "84000000-0000-4000-8000-000000000002"}
	build := strings.Repeat("a", 64)
	at := time.Date(2026, time.October, 16, 0, 0, 0, 0, time.UTC)
	snapshot := jobcontract.Snapshot{Receipt: jobcontract.Receipt{
		Contract: jobcontract.CurrentContract, Kind: jobcontract.KindRun, AcceptedAt: at,
		Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: origin.NodeID, InstallationID: origin.InstallationID,
			EngineBuildID: build, RunnerVersion: "snapshot-test", OS: "linux", Arch: "amd64"},
		Status: domain.RunQueued, Revision: 1,
		RunIDs: []string{"85000000-0000-4000-8000-000000000001"}, ReportIDs: []string{},
	}, LatestSequence: 1, UpdatedAt: at}
	for _, id := range ids {
		snapshot.Receipt.JobID = id
		rawSnapshot, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		// This query-only fixture exercises the small-header port. Payload
		// authentication remains the real mirror reader's responsibility.
		spec, err := json.Marshal(jobcontract.JobSpec{Contract: jobcontract.CurrentContract, JobID: id,
			NodeInstallationID: origin.InstallationID, EngineBuildID: build,
			Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: origin.NodeID},
			Kind:   jobcontract.KindRun, Assets: []jobcontract.Asset{}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.db.ExecContext(
			ctx,
			`INSERT INTO remote_job_mirrors(job_id,configuration_id,node_id,installation_id,spec_sha256,
			spec_json,snapshot_json,applied_sequence) VALUES(?,?,?,?,?,?,?,1)`,
			id,
			origin.ConfigurationID,
			origin.NodeID,
			origin.InstallationID,
			strings.Repeat("b", 64),
			string(spec),
			string(rawSnapshot),
		); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := reader.db.QueryContext(
		ctx,
		"EXPLAIN QUERY PLAN "+remoteSyncPriorityHeadersSQL,
		origin.ConfigurationID,
		origin.NodeID,
		origin.InstallationID,
		"",
		maxSubmissionRecordBytes,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxStateBytes,
		jobcontract.MaxStateBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		t.Log(detail)
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	if err := plan.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err := reader.db.QueryContext(
		ctx,
		remoteSyncPriorityHeadersSQL,
		origin.ConfigurationID,
		origin.NodeID,
		origin.InstallationID,
		"",
		maxSubmissionRecordBytes,
		jobcontract.MaxSubmissionBytes,
		jobcontract.MaxStateBytes,
		jobcontract.MaxStateBytes,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	values := make([]any, 23)
	destinations := make([]any, len(values))
	for index := range values {
		destinations[index] = &values[index]
	}
	if !rows.Next() || rows.Scan(destinations...) != nil {
		t.Fatal("first priority row missing")
	}
	snapshot.Receipt.JobID, snapshot.Receipt.Status, snapshot.Receipt.Revision = ids[1], domain.RunCancelled, 2
	snapshot.LatestSequence, snapshot.UpdatedAt = 2, at.Add(time.Second)
	terminal, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.db.ExecContext(ctx,
		`UPDATE remote_job_mirrors SET snapshot_json = ?, applied_sequence = 2 WHERE job_id = ?`,
		string(terminal), ids[1]); err != nil {
		t.Fatalf("independent repository WAL write blocked by streaming reader: %v", err)
	}
	if !rows.Next() || rows.Scan(destinations...) != nil {
		t.Fatal("second priority row missing")
	}
	raw, ok := values[19].(string)
	if !ok {
		t.Fatalf("unexpected SQLite text representation: %T", values[19])
	}
	observed, err := jobcontract.DecodeSnapshot(bytes.NewBufferString(raw))
	if err != nil || observed.Receipt.Status != domain.RunQueued || observed.LatestSequence != 1 {
		t.Fatal("one statement mixed snapshots after concurrent write")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected priority statement tail")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	page, err := reader.ListRemoteSyncPriorityIDs(ctx, origin, "", 25)
	if err != nil || !slices.Equal(page.JobIDs, ids[:1]) {
		t.Fatalf("new priority statement did not observe committed terminal write: %+v, %v", page, err)
	}
}
