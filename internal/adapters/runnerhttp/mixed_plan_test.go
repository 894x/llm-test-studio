package runnerhttp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestHTTPMixedPlanExecutesAndDownloadsOneOrderedReport(t *testing.T) {
	caps := capabilities()
	caps.Features = append(caps.Features, jobcontract.FeatureMixedPlanItems)
	fixture := newHTTPFixture(t, false, caps)
	submission := fixture.runSubmission(t, false)
	snapshot := &submission.Job.Spec.Run.Snapshot
	for range 2 {
		id := httpMeta(t).ID
		profile := domain.PerformancePlanProfile{LoadMode: domain.LoadFixedConcurrency,
			ArrivalPattern: "constant", WorkloadMode: "fixed", RequestCount: 2,
			Concurrency: 1, TimeoutMS: 1000, InputTokens: 32, OutputTokens: 2}
		entry := domain.PlanEntry{EntryID: id, TargetID: id, TargetKind: domain.PlanTargetPerformance,
			Name: "K3 pressure", OnFailure: "continue", Performance: &profile, Load: profile.LoadProfile()}
		snapshot.PlanDocument.Entries = append(snapshot.PlanDocument.Entries, entry)
		snapshot.Entries = append(snapshot.Entries, domain.RunEntrySnapshot{
			EntryID: id, TargetID: id, TargetKind: entry.TargetKind, Name: entry.Name, Key: "performance",
			OnFailure: entry.OnFailure, Performance: &profile, Load: entry.Load,
			Cases: []domain.CaseRevisionRef{}, CaseDefinitions: []domain.TestCase{}, Parameters: map[string]json.RawMessage{}, CaseInputs: map[string]map[string]json.RawMessage{},
		})
	}
	if capabilities().SupportsJob(submission.Job) {
		t.Fatal("old node accepted mixed items")
	}
	client := httpManagementClient(t, fixture)
	if _, err := client.Submit(t.Context(), &submission); err != nil {
		t.Fatal(err)
	}
	final := waitHTTPJob(t, fixture, submission.Job.JobID, domain.RunCompleted)
	waitHTTPSettlement(t, client)
	reports, err := fixture.repository.ListReportsForRuns(t.Context(), final.Receipt.RunIDs)
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports: %v", err)
	}
	report := reports[0]
	if report.Conclusion.Passed {
		t.Fatal("unasserted basic item became a pass because pressure passed")
	}
	if len(report.EntryReports) != 3 || len(report.EntryReports[2].Performance) == 0 {
		t.Fatal("mixed report lost ordered performance items")
	}
	if err := quicktest.ValidatePlanReport(report); err != nil {
		t.Fatal(err)
	}
	response := fixture.request(t, http.MethodGet, "/jobs/"+submission.Job.JobID+"/reports/"+report.ID, nil)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("download: %d %s", response.StatusCode, raw)
	}
	var downloaded domain.Report
	if err := json.Unmarshal(raw, &downloaded); err != nil {
		t.Fatal(err)
	}
	if err := quicktest.ValidatePlanReport(downloaded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded.EntryReports[2].Performance, report.EntryReports[2].Performance) {
		t.Fatal("download changed performance measurements")
	}
	if _, err := reporting.New(fixture.repository).Export(t.Context(), report.ID, reporting.ExportHTML, "test"); err != nil {
		t.Fatalf("mixed HTML export: %v", err)
	}
}
