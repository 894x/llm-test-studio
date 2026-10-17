package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

func assertRemoteNodeUsage(t *testing.T, repository *persistence.Repository, want bool) {
	t.Helper()
	blocked, err := repository.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.ConfigurationID,
		remoteOrigin.NodeID, remoteOrigin.InstallationID)
	if err != nil || blocked != want {
		t.Fatalf("remote node usage: blocked=%v want=%v error=%v", blocked, want, err)
	}
}

func TestRemoteNodeUsageRequiresCompleteReportAndRetainsArtifactReferences(t *testing.T) {
	for _, files := range []string{"none", "attachments", "evidence"} {
		t.Run(files, func(t *testing.T) {
			node, desktop := openOperationalRepository(t), openOperationalRepository(t)
			defer node.Close()
			defer desktop.Close()
			fixture := remoteUsageRunFixture(t, files)
			spec := populateRemoteUsageRun(t, node, fixture)
			assertRemoteNodeUsage(t, desktop, false)
			events := remoteEvents(t, node, spec)
			applyRemoteEvents(t, desktop, events[:1])
			assertRemoteNodeUsage(t, desktop, true)
			applyRemoteEvents(t, desktop, events[1:])
			assertRemoteNodeUsage(t, desktop, true)
			origin := executionOrigin(spec)
			fixture.report.ExecutionOrigin = &origin
			document, err := jobcontract.EncodeRunReport(fixture.report)
			if err != nil {
				t.Fatal(err)
			}
			if err := desktop.ImportRemoteRunReport(t.Context(), remoteresults.RunReportImport{
				Origin: remoteOrigin, JobID: spec.JobID, Document: document,
			}); err != nil {
				t.Fatal(err)
			}
			assertRemoteNodeUsage(t, desktop, files != "none")
			if files == "none" {
				control := controlFixture(
					t,
					desktop,
					spec.JobID,
					1,
				)
				if _, err := desktop.CreateControl(t.Context(), control); err != nil {
					t.Fatal(err)
				}
				assertRemoteNodeUsage(t, desktop, true)
				control.Revision++
				control.State, control.Observation = jobs.ControlRejected, jobs.ObservationRejected
				control.UpdatedAt = control.UpdatedAt.Add(time.Second)
				if err := desktop.UpdateControl(t.Context(), control, control.Revision-1); err != nil {
					t.Fatal(err)
				}
				assertRemoteNodeUsage(t, desktop, false)
			}
			if _, err := desktop.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.ConfigurationID,
				remoteOrigin.ConfigurationID, remoteOrigin.InstallationID); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatal("mismatched executor identity was ignored")
			}
			if blocked, err := desktop.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.ConfigurationID, "", ""); err != nil || !blocked {
				t.Fatal("unbound configuration ignored its existing task associations")
			}
			if blocked, err := desktop.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.NodeID, "", ""); err != nil || blocked {
				t.Fatal("unrelated unbound configuration was blocked by another configuration's jobs")
			}
		})
	}
}

func TestRemoteNodeUsageRetainsPreparedPendingAcceptedAndPendingControls(t *testing.T) {
	node, desktop := openOperationalRepository(t), openOperationalRepository(t)
	defer node.Close()
	defer desktop.Close()
	fixture := remoteUsageRunFixture(t, "none")
	spec := populateRemoteUsageRun(t, node, fixture)
	record := submissionFixture(t, spec)
	if _, err := desktop.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, true)
	pending := pendingSubmission(record)
	if err := desktop.UpdateSubmission(t.Context(), pending, record.Revision); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, true)
	accepted := acceptedSubmission(t, pending)
	serverSnapshot, err := node.GetJob(t.Context(), spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	accepted.Receipt = &serverSnapshot.Receipt
	if err := desktop.UpdateSubmission(t.Context(), accepted, pending.Revision); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, true)
	applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
	origin := executionOrigin(spec)
	fixture.report.ExecutionOrigin = &origin
	document, _ := jobcontract.EncodeRunReport(fixture.report)
	if err := desktop.ImportRemoteRunReport(t.Context(), remoteresults.RunReportImport{
		Origin: remoteOrigin, JobID: spec.JobID, Document: document,
	}); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, false)
	commandMeta := entityMeta("91000000-0000-4000-8000-000000000008", 1)
	control := jobs.ControlRecord{SchemaVersion: 1, Revision: 1, State: jobs.ControlPending,
		Target: controlFixture(
			t,
			desktop,
			spec.JobID,
			1,
		).Target,
		CreatedAt: commandMeta.CreatedAt, UpdatedAt: commandMeta.UpdatedAt,
		Command: jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: commandMeta.ID,
			JobID: spec.JobID, Kind: jobcontract.CommandCancel, ExpectedRevision: 1}}
	if _, err := desktop.CreateControl(t.Context(), control); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, true)
}

func TestRemoteNodeUsageRequiresSealedPerformanceResultAndItsEligibleArchive(t *testing.T) {
	for _, measured := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancelled_before_execution", true: "measured_archive"}[measured], func(t *testing.T) {
			core := newPerformanceStorageFixture(t)
			node, desktop := openOperationalRepository(t), openOperationalRepository(t)
			defer node.Close()
			defer desktop.Close()
			spec := remoteSpec(core.spec)
			if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
				t.Fatal(err)
			}
			status := domain.RunCancelled
			if measured {
				status = domain.RunCompleted
				for _, next := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
					current, _ := node.GetJob(t.Context(), spec.JobID)
					if _, err := node.ChangeJob(t.Context(), jobhost.Change{JobID: spec.JobID,
						ExpectedRevision: current.Receipt.Revision, Status: next, At: current.UpdatedAt.Add(time.Second)}); err != nil {
						t.Fatal(err)
					}
				}
				for index := range core.checkpoints {
					if _, err := node.RecordPerformanceObservation(t.Context(), core.input(index)); err != nil {
						t.Fatal(err)
					}
				}
			}
			current, _ := node.GetJob(t.Context(), spec.JobID)
			if _, err := node.ChangeJob(t.Context(), jobhost.Change{JobID: spec.JobID,
				ExpectedRevision: current.Receipt.Revision, Status: status, At: current.UpdatedAt.Add(time.Second)}); err != nil {
				t.Fatal(err)
			}
			applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
			assertRemoteNodeUsage(t, desktop, true)
			current, _ = node.GetJob(t.Context(), spec.JobID)
			sealed, err := node.SealPerformanceResult(t.Context(), spec.JobID, current.UpdatedAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if measured {
				archive, eligible, err := sealed.Result.StatisticalArchive()
				if err != nil || !eligible {
					t.Fatal("fixture did not produce an eligible archive")
				}
				if err := node.SaveQuickPerformanceReport(t.Context(), archive); err != nil {
					t.Fatal(err)
				}
			}
			applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
			assertRemoteNodeUsage(t, desktop, true)
			if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
				Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventPerformanceResult,
				Document: canonicalFixtureJSON(t, sealed.Result),
			}); err != nil {
				t.Fatal(err)
			}
			assertRemoteNodeUsage(t, desktop, measured)
			if measured {
				archive, eligible, err := sealed.Result.StatisticalArchive()
				if err != nil || !eligible {
					t.Fatal("fixture did not produce an eligible archive")
				}
				if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
					Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventReport,
					Document: canonicalFixtureJSON(t, archive),
				}); err != nil {
					t.Fatal(err)
				}
				assertRemoteNodeUsage(t, desktop, false)
			}
		})
	}
}

func TestRemoteNodeUsageFailsClosedForUnavailableAndCorruptStorage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "usage-test"}); err != nil {
		t.Fatal(err)
	}
	repository := reopenHardeningRepository(t, path)
	record := submissionFixture(t, remoteSpec(jobSpecForRun(t, newRepositoryFixture(t).run)))
	if _, err := repository.CreateSubmission(t.Context(), record); err != nil {
		t.Fatal(err)
	}
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `UPDATE job_submissions SET document_json = '{}' WHERE job_id = ?`,
		record.Prepared.Spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.ConfigurationID,
		remoteOrigin.NodeID, remoteOrigin.InstallationID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("corrupt submission was interpreted as absent usage")
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RemoteNodeRemovalBlocked(context.Background(), remoteOrigin.ConfigurationID,
		remoteOrigin.NodeID, remoteOrigin.InstallationID); err == nil {
		t.Fatal("closed database was interpreted as empty usage")
	}
}

func TestRemoteNodeUsageRejectsUnknownDownloadFactsAndIncompleteLocalResults(t *testing.T) {
	node := openOperationalRepository(t)
	defer node.Close()
	fixture := remoteUsageRunFixture(t, "none")
	spec := populateRemoteUsageRun(t, node, fixture)
	path := filepath.Join(t.TempDir(), "usage-result.db")
	if err := persistence.Migrate(t.Context(), path, persistence.MigrateOptions{AppVersion: "usage-test"}); err != nil {
		t.Fatal(err)
	}
	desktop := reopenHardeningRepository(t, path)
	defer desktop.Close()
	events := remoteEvents(t, node, spec)
	applyRemoteEvents(t, desktop, events)
	origin := executionOrigin(spec)
	fixture.report.ExecutionOrigin = &origin
	document, _ := jobcontract.EncodeRunReport(fixture.report)
	if err := desktop.ImportRemoteRunReport(t.Context(), remoteresults.RunReportImport{
		Origin: remoteOrigin, JobID: spec.JobID, Document: document,
	}); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, false)
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.ExecContext(t.Context(), `INSERT INTO remote_job_events(job_id, sequence, kind, document_json)
		VALUES(?, ?, 'artifact_download', '{}')`, spec.JobID, len(events)+1); err != nil {
		t.Fatal(err)
	}
	assertRemoteNodeUsage(t, desktop, true)
	if _, err := db.ExecContext(t.Context(), `DELETE FROM remote_job_events WHERE job_id = ? AND kind = 'artifact_download'`,
		spec.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DELETE FROM case_results WHERE id = ?`, fixture.result.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := desktop.RemoteNodeRemovalBlocked(t.Context(), remoteOrigin.ConfigurationID,
		remoteOrigin.NodeID, remoteOrigin.InstallationID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatal("an imported report without its complete local result projection allowed removal")
	}
}

func TestRemoteNodeUsageChecksPastTheFirstPageOfCompletedJobs(t *testing.T) {
	core := newPerformanceStorageFixture(t)
	node, desktop := openOperationalRepository(t), openOperationalRepository(t)
	defer node.Close()
	defer desktop.Close()
	for index := range int(jobs.MaxSubmissionPageSize) + 1 {
		spec := remoteSpec(core.spec)
		spec.JobID = fmt.Sprintf("93000000-0000-4000-8000-%012d", index+1)
		performance := *spec.Spec.Performance
		performance.ReportID = fmt.Sprintf("93000000-0000-4000-8000-%012d", index+101)
		spec.Spec.Performance = &performance
		if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
			t.Fatal(err)
		}
		current, _ := node.GetJob(t.Context(), spec.JobID)
		if _, err := node.ChangeJob(t.Context(), jobhost.Change{JobID: spec.JobID,
			ExpectedRevision: current.Receipt.Revision, Status: domain.RunCancelled,
			At: current.UpdatedAt.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
		if index == int(jobs.MaxSubmissionPageSize) {
			applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
			break
		}
		current, _ = node.GetJob(t.Context(), spec.JobID)
		sealed, err := node.SealPerformanceResult(t.Context(), spec.JobID, current.UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		applyRemoteEvents(t, desktop, remoteEvents(t, node, spec))
		if err := desktop.ImportRemotePerformanceDocument(t.Context(), remoteresults.PerformanceDocumentImport{
			Origin: remoteOrigin, JobID: spec.JobID, Kind: jobcontract.EventPerformanceResult,
			Document: canonicalFixtureJSON(t, sealed.Result),
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertRemoteNodeUsage(t, desktop, true)
}

func remoteUsageRunFixture(t *testing.T, files string) repositoryFixture {
	t.Helper()
	fixture := newRepositoryFixture(t)
	if files != "attachments" {
		fixture.report.Attachments = []domain.ReportAttachment{}
	}
	if files != "evidence" {
		fixture.report.Evidence = []domain.Evidence{}
		fixture.result.EvidenceIDs = []string{}
		for index := range fixture.report.CaseResults {
			fixture.report.CaseResults[index].EvidenceIDs = []string{}
		}
		for index := range fixture.report.EntryReports {
			for result := range fixture.report.EntryReports[index].CaseResults {
				fixture.report.EntryReports[index].CaseResults[result].EvidenceIDs = []string{}
			}
		}
	}
	return fixture
}

func populateRemoteUsageRun(t *testing.T, node *persistence.Repository, fixture repositoryFixture) jobcontract.JobSpec {
	t.Helper()
	spec := remoteSpec(jobSpecForRun(t, fixture.run))
	origin := executionOrigin(spec)
	fixture.report.ExecutionOrigin = &origin
	if _, _, err := node.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
		t.Fatal(err)
	}
	current := fixture.run
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		next, err := current.Transition(status, current.Meta().UpdatedAt.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := node.UpdateRun(t.Context(), current.Meta().Revision, next); err != nil {
			t.Fatal(err)
		}
		current = next
	}
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning, domain.RunCompleted} {
		previous, err := node.GetJob(t.Context(), spec.JobID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := node.ChangeJob(t.Context(), jobhost.Change{JobID: spec.JobID,
			ExpectedRevision: previous.Receipt.Revision, Status: status, At: previous.UpdatedAt.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(fixture.report.Evidence) != 0 {
		if err := node.CreateEvidence(t.Context(), fixture.evidence); err != nil {
			t.Fatal(err)
		}
	}
	if err := node.AppendResults(t.Context(), fixture.result); err != nil {
		t.Fatal(err)
	}
	completed, err := current.Transition(domain.RunCompleted, current.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := node.UpdateRun(t.Context(), current.Meta().Revision, completed); err != nil {
		t.Fatal(err)
	}
	if err := node.CreateReport(t.Context(), fixture.report); err != nil {
		t.Fatal(err)
	}
	return spec
}
