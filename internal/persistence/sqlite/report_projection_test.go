package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

var _ reporting.Catalog = (*persistence.Repository)(nil)

func TestReportProjectionsReturnOnlyBoundedSummaryScalars(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	storeFixtureReport(t, repository, fixture)

	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() error = %v", err)
	}
	if len(projections) != 1 {
		t.Fatalf("projections = %#v", projections)
	}
	got := projections[0]
	if got.ID != fixture.report.ID || got.RunID != fixture.report.RunID || !got.GeneratedAt.Equal(fixture.report.GeneratedAt) {
		t.Fatalf("projection identity = %#v", got)
	}
	if got.RunStatus != domain.RunCompleted || got.PlanName != fixture.plan.Name ||
		got.ModelName != fixture.model.Name || got.ChannelName != fixture.channel.Name {
		t.Fatalf("projection labels/status = %#v", got)
	}
	if !got.Passed || got.Verdict != "pass" || got.IssueCount != 0 ||
		got.CaseCount != 1 || got.FailedCaseCount != 0 || got.AttachmentCount != 1 {
		t.Fatalf("projection aggregates = %#v", got)
	}

	serialized := strings.ToLower(strings.Join([]string{got.PlanName, got.ModelName, got.ChannelName, got.Verdict}, " "))
	for _, forbidden := range []string{"https://", "direct", "relative_path", "baseline", "evidence", "credential"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("bounded projection exposed %q: %#v", forbidden, got)
		}
	}
}

func TestReportProjectionsUseV2RunSnapshotAfterPlanCatalogRowIsDeleted(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	snapshot := fixture.run.Snapshot()
	snapshot.SchemaVersion = domain.CurrentRunSnapshotSchemaVersion
	planDocument := fixture.plan
	mappingDocument := fixture.mapping
	snapshot.PlanDocument = &planDocument
	snapshot.Mapping = &mappingDocument
	snapshot.CaseDefinitions = []domain.TestCase{fixture.testCase}
	run, err := domain.NewRun(fixture.run.Meta(), fixture.plan.ID, snapshot)
	if err != nil {
		t.Fatalf("NewRun(v2 snapshot) error = %v", err)
	}
	fixture.run = run
	fixture.report.PlanSnapshot = snapshot
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	repository = retireAndReopenOperationalRepository(t, path)
	defer repository.Close()

	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() after deleting plan catalog row error = %v", err)
	}
	if len(projections) != 1 || projections[0].PlanName != fixture.plan.Name {
		t.Fatalf("projections = %#v, want plan name %q", projections, fixture.plan.Name)
	}
}

func TestReportProjectionsMeasureDocumentBudgetsInUTF8Bytes(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	defer repository.Close()
	fixture.report.Conclusion.Verdict = "通过"
	storeFixtureReport(t, repository, fixture)
	tamper(t, path, `UPDATE reports SET document_json = CAST(document_json AS TEXT) WHERE id = ?`, fixture.report.ID)

	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() error = %v", err)
	}
	if len(projections) != 1 || projections[0].Verdict != fixture.report.Conclusion.Verdict {
		t.Fatalf("projections = %#v", projections)
	}
}

func TestReportProjectionsPreserveContextTermination(t *testing.T) {
	t.Parallel()

	repository := openOperationalRepository(t)
	defer repository.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repository.ListReportProjections(ctx)
	if !errors.Is(err, context.Canceled) || err.Error() != context.Canceled.Error() {
		t.Fatalf("ListReportProjections() error = %v, want context.Canceled", err)
	}
}

func TestReportProjectionsRejectReportScalarAndAggregateCorruption(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
		args      func(repositoryFixture) []any
	}{
		{
			name:      "column document id mismatch",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.id', ?) WHERE id = ?`,
			args: func(fixture repositoryFixture) []any {
				return []any{"ffffffff-ffff-4fff-8fff-ffffffffffff", fixture.report.ID}
			},
		},
		{
			name:      "generated timestamp mismatch",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.generated_at', ?) WHERE id = ?`,
			args: func(fixture repositoryFixture) []any {
				return []any{fixture.report.GeneratedAt.Add(time.Minute).Format(time.RFC3339Nano), fixture.report.ID}
			},
		},
		{
			name:      "model duplicate mismatch",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.model.name', 'other') WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "channel duplicate mismatch",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.channel.name', 'other') WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "non boolean conclusion",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.conclusion.passed', 'yes') WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "blank issue",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.conclusion.issues', json('[" "]')) WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "missing report result",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.case_results', json('[]')) WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "report result differs from stored result",
			statement: `UPDATE reports SET document_json = json_set(document_json, '$.case_results[0].success.sla', false) WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			tamper(t, path, test.statement, test.args(fixture)...)
			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectOwnerPlanAndAttachmentCorruption(t *testing.T) {
	for _, test := range []struct {
		name      string
		statement string
		args      func(repositoryFixture) []any
	}{
		{
			name:      "run is no longer sealed",
			statement: `UPDATE execution_runs SET sealed = 0 WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.run.Meta().ID} },
		},
		{
			name:      "run owner is missing",
			statement: `DELETE FROM execution_runs WHERE id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.run.Meta().ID} },
		},
		{
			name:      "attachment link is missing",
			statement: `DELETE FROM report_attachments WHERE report_id = ?`,
			args:      func(fixture repositoryFixture) []any { return []any{fixture.report.ID} },
		},
		{
			name:      "artifact owner differs",
			statement: `UPDATE artifacts SET document_json = json_set(document_json, '$.run_id', ?) WHERE run_id = ?`,
			args: func(fixture repositoryFixture) []any {
				return []any{"ffffffff-ffff-4fff-8fff-ffffffffffff", fixture.report.RunID}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			if test.name == "run owner is missing" {
				tamperWithoutForeignKeys(t, path, test.statement, test.args(fixture)...)
			} else {
				tamper(t, path, test.statement, test.args(fixture)...)
			}
			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectInvalidUUIDAtTheStorageBoundary(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	fixture.report.Attachments = []domain.ReportAttachment{}
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(
		t,
		path,
		`UPDATE reports SET id = ?, document_json = json_set(document_json, '$.id', ?) WHERE id = ?`,
		"not-a-uuid",
		"not-a-uuid",
		fixture.report.ID,
	)
	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListReportProjections(context.Background())
		return err
	})
}

func TestReportProjectionsRejectJointReportAndResultShapeCorruption(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(t, path, `UPDATE case_results SET document_json = json_remove(document_json, '$.success') WHERE id = ?`, fixture.result.ID)
	tamper(t, path, `UPDATE reports SET document_json = json_remove(document_json, '$.case_results[0].success') WHERE id = ?`, fixture.report.ID)
	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListReportProjections(context.Background())
		return err
	})
}

func TestReportProjectionsRejectJointlyMissingNestedRequiredFields(t *testing.T) {
	for _, test := range []struct {
		name            string
		storedStatement string
		reportStatement string
		storedID        func(repositoryFixture) string
	}{
		{
			name:            "result metadata",
			storedStatement: `UPDATE case_results SET document_json = json_remove(document_json, '$.updated_at') WHERE id = ?`,
			reportStatement: `UPDATE reports SET document_json = json_remove(document_json, '$.case_results[0].updated_at') WHERE id = ?`,
			storedID:        func(fixture repositoryFixture) string { return fixture.result.ID },
		},
		{
			name:            "evidence path",
			storedStatement: `UPDATE evidence SET document_json = json_remove(document_json, '$.relative_path') WHERE id = ?`,
			reportStatement: `UPDATE reports SET document_json = json_remove(document_json, '$.evidence[0].relative_path') WHERE id = ?`,
			storedID:        func(fixture repositoryFixture) string { return fixture.evidence.ID },
		},
		{
			name:            "artifact name",
			storedStatement: `UPDATE artifacts SET document_json = json_remove(document_json, '$.name') WHERE id = ?`,
			reportStatement: `UPDATE reports SET document_json = json_remove(document_json, '$.attachments[0].name') WHERE id = ?`,
			storedID:        func(fixture repositoryFixture) string { return fixture.report.Attachments[0].ArtifactID },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			tamper(t, path, test.storedStatement, test.storedID(fixture))
			tamper(t, path, test.reportStatement, fixture.report.ID)
			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectInvalidIntermediateRunTransition(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(t, path, `DROP TRIGGER trg_execution_run_revisions_no_update`)
	tamper(t, path, `
		UPDATE execution_run_revisions
		SET status = 'running', document_json = json_set(document_json, '$.status', 'running')
		WHERE run_id = ? AND revision = 2
	`, fixture.run.Meta().ID)
	restoreRunRevisionUpdateTrigger(t, path)
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()

	if _, err := repository.ListReportProjections(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("ListReportProjections() error = %v, want ErrCorrupt", err)
	}
}

func TestReportProjectionsIgnoreRetiredCatalogPlanSchema(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(t, path, `
		UPDATE test_plans
		SET schema_version = 99, document_json = json_set(document_json, '$.schema_version', 99)
		WHERE id = ? AND revision = ?
	`, fixture.plan.ID, fixture.plan.Revision)
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()

	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() error = %v", err)
	}
	if len(projections) != 1 || projections[0].PlanName != fixture.plan.Name {
		t.Fatalf("snapshot-backed projections = %#v, want plan name %q", projections, fixture.plan.Name)
	}
}

func TestReportProjectionsRejectSemanticallyEquivalentNonCanonicalJSON(t *testing.T) {
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, string, repositoryFixture)
	}{
		{
			name: "reordered report fields",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `
					UPDATE reports
					SET document_json = json_set(
						json_remove(document_json, '$.schema_version'),
						'$.schema_version', schema_version
					)
					WHERE id = ?
				`, fixture.report.ID)
			},
		},
		{
			name: "duplicate result field",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `
					UPDATE case_results
					SET document_json = substr(document_json, 1, length(document_json) - 1) ||
						',"id":' || json_quote(id) || '}'
					WHERE id = ?
				`, fixture.result.ID)
				tamper(t, path, `
					UPDATE reports
					SET document_json = json_set(
						document_json,
						'$.case_results[0]',
						json((SELECT document_json FROM case_results WHERE id = ?))
					)
					WHERE id = ?
				`, fixture.result.ID, fixture.report.ID)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			test.tamper(t, path, fixture)
			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectNonCanonicalScalarEscape(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	tamper(t, path, `
		UPDATE reports
		SET document_json = replace(
			document_json,
			'"run_status":"completed"',
			'"run_status":"compl\u0065ted"'
		)
		WHERE id = ? AND instr(document_json, '"run_status":"completed"') > 0
	`, fixture.report.ID)
	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListReportProjections(context.Background())
		return err
	})
}

func TestReportProjectionsReturnOnlyTheLatestPublishedLimit(t *testing.T) {
	const totalReports = 1000
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	cloneFixtureReports(t, path, fixture, totalReports)
	repository = reopenHardeningRepository(t, path)
	defer repository.Close()

	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() error = %v", err)
	}
	if len(projections) != 100 {
		t.Fatalf("projection count = %d, want 100 latest reports", len(projections))
	}
	wantNewestID := fixtureUUID("b", totalReports)
	if projections[0].ID != wantNewestID {
		t.Fatalf("newest projection id = %q, want %q", projections[0].ID, wantNewestID)
	}
}

func TestReportProjectionsLatestWindowUsesLosslessNanosecondOrder(t *testing.T) {
	const totalReports = reporting.MaxSnapshotReports + 1
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	cloneFixtureReports(t, path, fixture, totalReports)

	wholeSecond := fixture.report.GeneratedAt.Truncate(time.Second)
	generatedAt := make(map[string]time.Time, totalReports)
	for index := 1; index < totalReports; index++ {
		generatedAt[fixtureReportID(fixture, index)] = wholeSecond
	}
	wantNewestID := fixtureReportID(fixture, totalReports)
	generatedAt[wantNewestID] = wholeSecond.Add(900 * time.Millisecond)
	rewriteFixtureReportGeneratedTimes(t, path, generatedAt)

	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	projections, err := repository.ListReportProjections(context.Background())
	if err != nil {
		t.Fatalf("ListReportProjections() error = %v", err)
	}
	if len(projections) != reporting.MaxSnapshotReports {
		t.Fatalf("projection count = %d, want %d", len(projections), reporting.MaxSnapshotReports)
	}
	if projections[0].ID != wantNewestID {
		t.Fatalf("newest projection id = %q, want fractional-second report %q", projections[0].ID, wantNewestID)
	}
	if projections[1].ID != fixtureReportID(fixture, totalReports-1) {
		t.Fatalf("equal-time id tie-break = %q", projections[1].ID)
	}
}

func TestListReportsUsesLosslessNanosecondOrderAndIDTieBreak(t *testing.T) {
	const totalReports = 4
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	cloneFixtureReports(t, path, fixture, totalReports)

	wholeSecond := fixture.report.GeneratedAt.Truncate(time.Second)
	rewriteFixtureReportGeneratedTimes(t, path, map[string]time.Time{
		fixtureReportID(fixture, 1): wholeSecond,
		fixtureReportID(fixture, 2): wholeSecond.Add(900 * time.Millisecond),
		fixtureReportID(fixture, 3): wholeSecond.Add(90 * time.Millisecond),
		fixtureReportID(fixture, 4): wholeSecond.Add(900 * time.Millisecond),
	})

	repository = reopenHardeningRepository(t, path)
	defer repository.Close()
	reports, err := repository.ListReports(context.Background())
	if err != nil {
		t.Fatalf("ListReports() error = %v", err)
	}
	wantIDs := []string{
		fixtureReportID(fixture, 1),
		fixtureReportID(fixture, 3),
		fixtureReportID(fixture, 2),
		fixtureReportID(fixture, 4),
	}
	if len(reports) != len(wantIDs) {
		t.Fatalf("report count = %d, want %d", len(reports), len(wantIDs))
	}
	for index, wantID := range wantIDs {
		if reports[index].ID != wantID {
			t.Fatalf("reports[%d].ID = %q, want %q", index, reports[index].ID, wantID)
		}
	}
}

func TestReportProjectionsRejectNonCanonicalTimestampOutsideLatestWindow(t *testing.T) {
	const totalReports = reporting.MaxSnapshotReports + 1
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	cloneFixtureReports(t, path, fixture, totalReports)
	tamper(t, path, `UPDATE reports SET generated_at = '0001-01-01T00:00:00.0Z' WHERE id = ?`, fixture.report.ID)

	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListReportProjections(context.Background())
		return err
	})
}

func TestReportProjectionOrderingPreflightRejectsHiddenDocumentIdentityCorruption(t *testing.T) {
	const totalReports = reporting.MaxSnapshotReports + 1
	latestReportID := fixtureUUID("b", totalReports)
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, string, repositoryFixture)
	}{
		{
			name: "canonical timestamp column document mismatch",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `UPDATE reports SET generated_at = ? WHERE id = ?`,
					fixture.report.GeneratedAt.Add(-time.Hour).Format(time.RFC3339Nano), latestReportID)
			},
		},
		{
			name: "canonical id column document mismatch",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				generatedAt := make(map[string]time.Time, totalReports)
				for index := 1; index <= totalReports; index++ {
					generatedAt[fixtureReportID(fixture, index)] = fixture.report.GeneratedAt
				}
				rewriteFixtureReportGeneratedTimes(t, path, generatedAt)
				tamperWithoutForeignKeys(t, path, `UPDATE reports SET id = ? WHERE id = ?`,
					"00000000-0000-4000-8000-000000000001", latestReportID)
			},
		},
		{
			name: "duplicate document id",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `
					UPDATE reports
					SET document_json = '{"id":"' || id || '",' || substr(CAST(document_json AS TEXT), 2)
					WHERE id = ?
				`, fixture.report.ID)
			},
		},
		{
			name: "duplicate document timestamp",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `
					UPDATE reports
					SET document_json = '{"generated_at":"' || generated_at || '",' || substr(CAST(document_json AS TEXT), 2)
					WHERE id = ?
				`, fixture.report.ID)
			},
		},
		{
			name: "malformed document",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				db := openDatabase(t, path)
				defer db.Close()
				if _, err := db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
					t.Fatalf("enable malformed JSON tamper: %v", err)
				}
				if _, err := db.Exec(`UPDATE reports SET document_json = '{' WHERE id = ?`, fixture.report.ID); err != nil {
					t.Fatalf("tamper malformed hidden report JSON: %v", err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			cloneFixtureReports(t, path, fixture, totalReports)
			test.tamper(t, path, fixture)

			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectMissingOwnersInsideTheExactLatestWindow(t *testing.T) {
	const totalReports = reporting.MaxSnapshotReports + 1
	latestRunID := fixtureUUID("8", totalReports)
	missingPlanID := fixtureUUID("d", totalReports)

	for _, test := range []struct {
		name      string
		statement string
		args      []any
	}{
		{
			name:      "run owner",
			statement: `DELETE FROM execution_runs WHERE id = ?`,
			args:      []any{latestRunID},
		},
		{
			name:      "current revision",
			statement: `UPDATE execution_runs SET current_revision = 99 WHERE id = ?`,
			args:      []any{latestRunID},
		},
		{
			name:      "pinned plan",
			statement: `UPDATE execution_run_revisions SET plan_id = ? WHERE run_id = ? AND revision = 4`,
			args:      []any{missingPlanID, latestRunID},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			cloneFixtureReports(t, path, fixture, totalReports)
			if test.name == "pinned plan" {
				tamper(t, path, `DROP TRIGGER trg_execution_run_revisions_no_update`)
			}
			tamperWithoutForeignKeys(t, path, test.statement, test.args...)
			if test.name == "pinned plan" {
				restoreRunRevisionUpdateTrigger(t, path)
			}
			assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
				_, err := repository.ListReportProjections(context.Background())
				return err
			})
		})
	}
}

func TestReportProjectionsRejectDocumentsAboveByteBudgets(t *testing.T) {
	const (
		itemBudget = 8 << 20
	)
	for _, test := range []struct {
		name   string
		tamper func(*testing.T, string, repositoryFixture)
	}{
		{
			name: "report document",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				chunk := strings.Repeat("x", 7<<20)
				tamper(t, path, `
					UPDATE reports SET document_json = json_set(
						document_json, '$.timeline', json_array(
							json_object('padding', ?), json_object('padding', ?),
							json_object('padding', ?), json_object('padding', ?),
							json_object('padding', ?)
						)
					) WHERE id = ?
				`, chunk, chunk, chunk, chunk, chunk, fixture.report.ID)
			},
		},
		{
			name: "result item",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				padding := strings.Repeat("x", itemBudget+1)
				tamper(t, path, `UPDATE case_results SET document_json = json_set(document_json, '$.metrics', json_object(?, 1)) WHERE id = ?`, padding, fixture.result.ID)
				tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.case_results[0].metrics', json_object(?, 1)) WHERE id = ?`, padding, fixture.report.ID)
			},
		},
		{
			name: "evidence item",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				padding := strings.Repeat("x", itemBudget+1)
				tamper(t, path, `UPDATE evidence SET document_json = json_set(document_json, '$.relative_path', ?) WHERE id = ?`, padding, fixture.evidence.ID)
				tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.evidence[0].relative_path', ?) WHERE id = ?`, padding, fixture.report.ID)
			},
		},
		{
			name: "artifact item",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				padding := strings.Repeat("x", itemBudget+1)
				tamper(t, path, `UPDATE artifacts SET name = ?, document_json = json_set(document_json, '$.name', ?) WHERE id = ?`, padding, padding, fixture.report.Attachments[0].ArtifactID)
				tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.attachments[0].name', ?) WHERE id = ?`, padding, fixture.report.ID)
			},
		},
		{
			name: "opaque item",
			tamper: func(t *testing.T, path string, fixture repositoryFixture) {
				tamper(t, path, `UPDATE reports SET document_json = json_set(document_json, '$.timeline', json_array(json_object('padding', ?))) WHERE id = ?`, strings.Repeat("x", itemBudget+1), fixture.report.ID)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path, repository, fixture := openHardeningRepository(t)
			storeFixtureReport(t, repository, fixture)
			closeForTamper(t, repository)
			test.tamper(t, path, fixture)
			repository = reopenHardeningRepository(t, path)
			defer repository.Close()

			if _, err := repository.ListReportProjections(context.Background()); !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("ListReportProjections() error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestReportProjectionsClassifyMalformedJSONAsCorrupt(t *testing.T) {
	path, repository, fixture := openHardeningRepository(t)
	storeFixtureReport(t, repository, fixture)
	closeForTamper(t, repository)
	db := openDatabase(t, path)
	if _, err := db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatalf("enable malformed JSON tamper: %v", err)
	}
	if _, err := db.Exec(`UPDATE reports SET document_json = '{' WHERE id = ?`, fixture.report.ID); err != nil {
		t.Fatalf("tamper malformed report JSON: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close malformed JSON tamper database: %v", err)
	}
	assertCorruptOnLegacyReopenOrOperation(t, path, func(repository *persistence.Repository) error {
		_, err := repository.ListReportProjections(context.Background())
		return err
	})
}

func TestProjectionDocumentByteBudgetsRejectWrites(t *testing.T) {
	const (
		itemBudget = 8 << 20
	)
	for _, test := range []struct {
		name  string
		write func(*testing.T, *persistence.Repository, repositoryFixture) error
	}{
		{
			name: "evidence item",
			write: func(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) error {
				createRunGraph(t, repository, fixture)
				transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
				evidence := fixture.evidence
				evidence.RelativePath = strings.Repeat("x", itemBudget+1)
				return repository.CreateEvidence(context.Background(), evidence)
			},
		},
		{
			name: "result item",
			write: func(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) error {
				createRunGraph(t, repository, fixture)
				transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
				result := fixture.result
				result.EvidenceIDs = nil
				result.Metrics = map[string]float64{strings.Repeat("x", itemBudget+1): 1}
				return repository.AppendResult(context.Background(), result)
			},
		},
		{
			name: "report document",
			write: func(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) error {
				run := createRunningOutputs(t, repository, fixture)
				run = transitionRun(t, repository, run, domain.RunCompleted)
				report := fixture.report
				report.RunStatus = run.Status()
				item := json.RawMessage(`{"padding":"` + strings.Repeat("x", 7<<20) + `"}`)
				report.Timeline = []json.RawMessage{item, item, item, item, item}
				return repository.CreateReport(context.Background(), report)
			},
		},
		{
			name: "artifact item",
			write: func(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) error {
				run := createRunningOutputs(t, repository, fixture)
				run = transitionRun(t, repository, run, domain.RunCompleted)
				report := fixture.report
				report.RunStatus = run.Status()
				report.Attachments[0].Name = strings.Repeat("x", itemBudget+1)
				return repository.CreateReport(context.Background(), report)
			},
		},
		{
			name: "opaque item",
			write: func(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) error {
				run := createRunningOutputs(t, repository, fixture)
				run = transitionRun(t, repository, run, domain.RunCompleted)
				report := fixture.report
				report.RunStatus = run.Status()
				report.Timeline = []json.RawMessage{json.RawMessage(`{"padding":"` + strings.Repeat("x", itemBudget+1) + `"}`)}
				return repository.CreateReport(context.Background(), report)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := openRepository(t)
			defer repository.Close()
			fixture := newRepositoryFixture(t)
			if err := test.write(t, repository, fixture); err == nil {
				t.Fatal("oversized write unexpectedly succeeded")
			}
		})
	}
}

func TestProjectionDocumentByteBudgetsAccountForCanonicalExpansion(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	run := createRunningOutputs(t, repository, fixture)
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	report.Timeline = []json.RawMessage{
		json.RawMessage(`{"padding":"` + strings.Repeat("<", 5<<20) + `"}`),
	}

	if err := repository.CreateReport(context.Background(), report); err == nil {
		_, readErr := repository.ListReportProjections(context.Background())
		t.Fatalf("CreateReport() unexpectedly accepted canonical expansion; subsequent read error = %v", readErr)
	}
}

func cloneFixtureReports(t *testing.T, path string, fixture repositoryFixture, total int) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin report clone seed: %v", err)
	}
	defer tx.Rollback()
	for index := 2; index <= total; index++ {
		runID := fixtureUUID("8", index)
		evidenceID := fixtureUUID("9", index)
		resultID := fixtureUUID("a", index)
		reportID := fixtureUUID("b", index)
		artifactID := fixtureUUID("c", index)
		generatedAt := fixture.report.GeneratedAt.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		statements := []struct {
			query string
			args  []any
		}{
			{`INSERT INTO execution_runs(id, current_revision, created_at, sealed)
			  SELECT ?, current_revision, created_at, sealed FROM execution_runs WHERE id = ?`, []any{runID, fixture.run.Meta().ID}},
			{`INSERT INTO execution_run_revisions(
				run_id, schema_version, revision, created_at, updated_at,
				plan_id, plan_revision, status, snapshot_json, document_json
			  )
			  SELECT ?, schema_version, revision, created_at, updated_at,
			         plan_id, plan_revision, status, snapshot_json,
			         json_set(document_json, '$.id', ?)
			  FROM execution_run_revisions WHERE run_id = ? ORDER BY revision`, []any{runID, runID, fixture.run.Meta().ID}},
			{`INSERT INTO evidence(id, schema_version, revision, created_at, updated_at, run_id, document_json)
			  SELECT ?, schema_version, revision, created_at, updated_at, ?,
			         json_set(document_json, '$.id', ?, '$.run_id', ?)
			  FROM evidence WHERE id = ?`, []any{evidenceID, runID, evidenceID, runID, fixture.evidence.ID}},
			{`INSERT INTO case_results(id, schema_version, revision, created_at, updated_at, run_id, case_id, request_id, document_json)
			  SELECT ?, schema_version, revision, created_at, updated_at, ?, case_id, request_id,
			         json_set(document_json, '$.id', ?, '$.run_id', ?, '$.evidence_ids', json_array(?))
			  FROM case_results WHERE id = ?`, []any{resultID, runID, resultID, runID, evidenceID, fixture.result.ID}},
			{`INSERT INTO reports(id, schema_version, run_id, generated_at, document_json)
			  SELECT ?, schema_version, ?, ?, json_set(
			         document_json,
			         '$.id', ?, '$.run_id', ?, '$.generated_at', ?,
			         '$.case_results[0].id', ?, '$.case_results[0].run_id', ?,
			         '$.case_results[0].evidence_ids', json_array(?),
			         '$.evidence[0].id', ?, '$.evidence[0].run_id', ?,
			         '$.attachments[0].artifact_id', ?, '$.attachments[0].run_id', ?
			  ) FROM reports WHERE id = ?`, []any{
				reportID, runID, generatedAt, reportID, runID, generatedAt,
				resultID, runID, evidenceID, evidenceID, runID, artifactID, runID, fixture.report.ID,
			}},
			{`INSERT INTO artifacts(id, run_id, name, relative_path, sha256, media_type, redacted, document_json)
			  SELECT ?, ?, name, relative_path, sha256, media_type, redacted,
			         json_set(document_json, '$.artifact_id', ?, '$.run_id', ?)
			  FROM artifacts WHERE id = ?`, []any{artifactID, runID, artifactID, runID, fixture.report.Attachments[0].ArtifactID}},
			{`INSERT INTO report_attachments(report_id, artifact_id, position) VALUES(?, ?, 0)`, []any{reportID, artifactID}},
		}
		for _, statement := range statements {
			if _, err := tx.Exec(statement.query, statement.args...); err != nil {
				t.Fatalf("seed cloned report %d: %v", index, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit report clone seed: %v", err)
	}
}

func fixtureReportID(fixture repositoryFixture, index int) string {
	if index == 1 {
		return fixture.report.ID
	}
	return fixtureUUID("b", index)
}

func rewriteFixtureReportGeneratedTimes(t *testing.T, path string, generatedAt map[string]time.Time) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin report timestamp rewrite: %v", err)
	}
	defer tx.Rollback()
	for reportID, timestamp := range generatedAt {
		canonical := timestamp.UTC().Format(time.RFC3339Nano)
		if _, err := tx.Exec(`
			UPDATE reports
			SET generated_at = ?, document_json = json_set(document_json, '$.generated_at', ?)
			WHERE id = ?
		`, canonical, canonical, reportID); err != nil {
			t.Fatalf("rewrite report %s timestamp: %v", reportID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit report timestamp rewrite: %v", err)
	}
}

func fixtureUUID(prefix string, index int) string {
	return fmt.Sprintf("30000000-0000-4000-8000-%s%011x", prefix, index)
}

func restoreRunRevisionUpdateTrigger(t *testing.T, path string) {
	t.Helper()
	tamper(t, path, `
		CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END
	`)
}

func storeFixtureReport(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) {
	t.Helper()
	run := createRunningOutputs(t, repository, fixture)
	run = transitionRun(t, repository, run, domain.RunCompleted)
	report := fixture.report
	report.RunStatus = run.Status()
	if err := repository.CreateReport(context.Background(), report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}
}
