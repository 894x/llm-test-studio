package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobcontent"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func jobPageSnapshotFixture(t *testing.T) (*Repository, jobs.SubmissionRecord) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "job-page.db")
	if err := Migrate(t.Context(), path, MigrateOptions{AppVersion: "job-page-test"}); err != nil {
		t.Fatal(err)
	}
	repository, err := OpenRepository(t.Context(), path, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	run := readViewRunFixture(t)
	spec := jobcontract.JobSpec{
		Contract: jobcontract.CurrentContract, JobID: "72000000-0000-4000-8000-000000000001",
		NodeInstallationID: "72000000-0000-4000-8000-000000000002",
		EngineBuildID:      strings.Repeat("a", 64), Kind: jobcontract.KindRun,
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}, Assets: []jobcontract.Asset{},
		Spec: jobcontract.Payload{Run: &runs.PreparedRun{
			Meta: run.Meta(), PlanID: run.PlanID(), Snapshot: run.Snapshot(),
		}},
	}
	info := jobcontract.RunnerInfo{
		Contract: jobcontract.CurrentContract, NodeID: "72000000-0000-4000-8000-000000000009",
		NodeInstallationID: spec.NodeInstallationID, EngineBuildID: spec.EngineBuildID,
		RunnerVersion: "job-page-test", OS: "linux", Arch: "amd64", Ready: true, Healthy: true,
		Capabilities: jobcontract.RunnerCapabilities{
			JobKinds: []jobcontract.Kind{jobcontract.KindRun}, Protocols: []string{"openai-chat"},
			Features: []string{jobcontract.FeatureExecutionOrigin},
		},
		Budget: jobcontract.RunnerBudget{MaxActiveJobs: 1, MaxQueuedJobs: 16, MaxConcurrency: 1,
			QueueTimeoutNS: int64(time.Minute), ExecutionTimeoutNS: int64(time.Hour)},
	}
	record, err := jobs.NewSubmission(jobs.PreparedJob{Spec: spec, Runner: info}, run.Meta().CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	return repository, record
}

func TestJobPageProjectionReleasesItsOnlyConnection(t *testing.T) {
	repository, _ := jobPageSnapshotFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseProjection := sync.OnceFunc(func() { close(release) })
	defer releaseProjection()
	finished := make(chan error, 1)
	go func() {
		_, err := repository.readJobPage(
			t.Context(),
			"",
			jobs.MaxSubmissionPageSize,
			func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
				close(entered)
				<-release
				return projectStoredJobView(ctx, stored)
			},
		)
		finished <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("job page never entered projection")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	err := repository.CheckWritable(ctx)
	cancel()
	releaseProjection()
	if readErr := <-finished; readErr != nil {
		t.Fatal(readErr)
	}
	if err != nil {
		t.Fatalf("pure projection retained the only database connection: %v", err)
	}
}

func TestJobPageProjectionRetainsCapturedSubmissionRevision(t *testing.T) {
	repository, record := jobPageSnapshotFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	page, err := repository.readJobPage(
		ctx,
		"",
		jobs.MaxSubmissionPageSize,
		func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
			next := record
			next.Revision++
			next.State, next.Observation = jobs.SubmissionPending, jobs.ObservationNone
			next.UpdatedAt = next.UpdatedAt.Add(time.Second)
			if err := repository.UpdateSubmission(ctx, next, record.Revision); err != nil {
				return jobs.JobView{}, err
			}
			return projectStoredJobView(ctx, stored)
		},
	)
	if err != nil || len(page.Jobs) != 1 {
		t.Fatalf("read captured page: %v", err)
	}
	submission := page.Jobs[0].Submission
	if submission == nil || submission.Delivery != jobs.SubmissionPrepared || submission.DeliveryRevision != 1 {
		t.Fatal("projection mixed a later submission revision into its captured snapshot")
	}
	next, err := repository.Jobs(t.Context(), "", jobs.MaxSubmissionPageSize)
	if err != nil || len(next.Jobs) != 1 || next.Jobs[0].Submission.DeliveryRevision != 2 {
		t.Fatalf("next page did not observe the committed successor: %v", err)
	}
}

func TestJobPageProjectionRejectsCapturedTamperingAndReleasesConnection(t *testing.T) {
	repository, record := jobPageSnapshotFixture(t)
	_, err := repository.readJobPage(
		t.Context(),
		"",
		jobs.MaxSubmissionPageSize,
		func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
			stored.execution.submission.document = append(stored.execution.submission.document, ' ')
			return projectStoredJobView(ctx, stored)
		},
	)
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("captured noncanonical record passed projection: %v", err)
	}
	if err := repository.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
	actual, err := repository.GetSubmission(t.Context(), record.Prepared.Spec.JobID)
	if err != nil || !record.SamePreparation(actual) || actual.Revision != record.Revision {
		t.Fatalf("rejected projection changed the source record: %v", err)
	}
}

func TestJobPageProjectionCancellationReturnsNoPartialPage(t *testing.T) {
	repository, _ := jobPageSnapshotFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	page, err := repository.readJobPage(
		ctx,
		"",
		jobs.MaxSubmissionPageSize,
		func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
			cancel()
			return projectStoredJobView(ctx, stored)
		},
	)
	if !errors.Is(err, context.Canceled) || len(page.Jobs) != 0 {
		t.Fatalf("cancelled projection returned a partial page: %v", err)
	}
	if err := repository.CheckWritable(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestJobPageProjectionRetainsCapturedRemoteRevisionAndAnchors(t *testing.T) {
	repository, record := jobPageSnapshotFixture(t)
	id := record.Prepared.Spec.JobID
	if _, err := repository.db.ExecContext(t.Context(), `DELETE FROM job_submissions WHERE job_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	nodePath := filepath.Join(t.TempDir(), "node.db")
	if err := Migrate(t.Context(), nodePath, MigrateOptions{AppVersion: "job-page-node"}); err != nil {
		t.Fatal(err)
	}
	node, err := OpenRepository(t.Context(), nodePath, RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	origin := remoteresults.Origin{
		ConfigurationID: "72000000-0000-4000-8000-000000000003",
		NodeID:          record.Prepared.Runner.NodeID, InstallationID: record.Prepared.Spec.NodeInstallationID,
	}
	spec := record.Prepared.Spec
	spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: origin.NodeID}
	snapshot, _, err := node.AcceptJob(t.Context(), jobhost.Acceptance{
		ContentClaims: []jobcontent.Claim{},
		Spec:          spec, SecretCommitment: strings.Repeat("b", 64), AcceptedAt: record.CreatedAt.Add(time.Minute),
		Origin: domain.ExecutionOrigin{Kind: "remote", NodeID: origin.NodeID, InstallationID: origin.InstallationID,
			RunnerVersion: record.Prepared.Runner.RunnerVersion, EngineBuildID: spec.EngineBuildID,
			OS: "linux", Arch: "amd64"},
	})
	if err != nil {
		t.Fatal(err)
	}
	importAfter := func(ctx context.Context, after uint64) error {
		page, err := node.JobEvents(
			ctx,
			id,
			after,
			200,
		)
		if err != nil {
			return err
		}
		_, err = repository.ApplyRemoteBatch(ctx, remoteresults.Batch{
			Origin: origin, JobID: id, AfterSequence: after, Events: page.Events,
		})
		return err
	}
	if err := importAfter(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	page, err := repository.readJobPage(
		ctx,
		"",
		jobs.MaxSubmissionPageSize,
		func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
			_, err := node.ChangeJob(ctx, jobhost.Change{
				JobID: id, ExpectedRevision: snapshot.Receipt.Revision, Status: domain.RunStarting,
				At: snapshot.UpdatedAt.Add(time.Second),
			})
			if err == nil {
				err = importAfter(ctx, snapshot.LatestSequence)
			}
			if err != nil {
				return jobs.JobView{}, err
			}
			return projectStoredJobView(ctx, stored)
		},
	)
	if err != nil || len(page.Jobs) != 1 {
		t.Fatalf("read captured mirror: %v", err)
	}
	view := page.Jobs[0]
	hasOriginalState := view.Execution != nil && view.Execution.Revision == 1 &&
		view.Execution.LatestSequence == 1 && view.Execution.Status == domain.RunQueued
	if view.Submission != nil || !hasOriginalState {
		t.Fatal("page mixed the successor mirror with captured accepted facts")
	}
	next, err := repository.Jobs(t.Context(), "", jobs.MaxSubmissionPageSize)
	if err != nil || len(next.Jobs) != 1 || next.Jobs[0].Execution.Revision != 2 {
		t.Fatalf("next mirror page did not observe the imported successor: %v", err)
	}
}

func TestJobPageRejectsNonintegerExecutionScalarsWithoutRetainingTheirBytes(t *testing.T) {
	for _, column := range []string{"revision", "last_sequence"} {
		t.Run(column, func(t *testing.T) {
			repository, record := jobPageSnapshotFixture(t)
			next := record
			next.Revision++
			next.State, next.Observation = jobs.SubmissionPending, jobs.ObservationNone
			next.UpdatedAt = next.UpdatedAt.Add(time.Second)
			if err := repository.UpdateSubmission(t.Context(), next, record.Revision); err != nil {
				t.Fatal(err)
			}
			spec, runner := record.Prepared.Spec, record.Prepared.Runner
			if _, _, err := repository.AcceptJob(t.Context(), jobhost.Acceptance{
				ContentClaims: []jobcontent.Claim{},
				Spec:          spec, SecretCommitment: strings.Repeat("b", 64), AcceptedAt: next.UpdatedAt.Add(time.Second),
				Origin: domain.ExecutionOrigin{Kind: "local", NodeID: runner.NodeID,
					InstallationID: runner.NodeInstallationID, EngineBuildID: runner.EngineBuildID,
					RunnerVersion: runner.RunnerVersion, OS: runner.OS, Arch: runner.Arch},
			}); err != nil {
				t.Fatal(err)
			}
			// INTEGER affinity in a non-STRICT table permits nonnumeric TEXT.
			// These two fixed internal columns must be screened by SQL before Scan.
			bad := strings.Repeat("x", 1<<20)
			statement := `UPDATE execution_jobs SET revision = ?, last_sequence = ? WHERE id = ?`
			arguments := []any{bad, bad, spec.JobID}
			if column == "last_sequence" {
				statement = `UPDATE execution_jobs SET last_sequence = ? WHERE id = ?`
				arguments = []any{bad, spec.JobID}
			}
			if _, err := repository.db.ExecContext(t.Context(), statement, arguments...); err != nil {
				t.Fatal(err)
			}
			projected := false
			page, err := repository.readJobPage(
				t.Context(),
				"",
				jobs.MaxSubmissionPageSize,
				func(ctx context.Context, stored storedJobView) (jobs.JobView, error) {
					projected = true
					if stored.execution.local.row.sequence != -1 {
						t.Fatal("SQL did not replace the noninteger scalar before Scan")
					}
					return projectStoredJobView(ctx, stored)
				},
			)
			if !projected || !errors.Is(err, ErrCorrupt) || len(page.Jobs) != 0 {
				t.Fatalf("noninteger %s escaped bounded source validation: %v", column, err)
			}
			if err := repository.CheckWritable(t.Context()); err != nil {
				t.Fatal(err)
			}
			var actual string
			if err := repository.db.QueryRowContext(
				t.Context(),
				`SELECT CASE WHEN ? = 'revision' THEN revision ELSE last_sequence END FROM execution_jobs WHERE id = ?`,
				column,
				spec.JobID,
			).Scan(&actual); err != nil || actual != bad {
				t.Fatalf("query rewrote the rejected scalar: %v", err)
			}
		})
	}
}
