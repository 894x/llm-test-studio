package sqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/reporting"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type mixedPlanClock struct{}

func (mixedPlanClock) Now() time.Time { return time.Now().UTC() }

type mixedPlanSLAExecutor struct {
	runs.Executor
	e2eMS  float64
	legacy bool
}

func (executor mixedPlanSLAExecutor) Execute(
	ctx context.Context,
	request runs.ExecutionRequest,
	emit func(runs.ResultDraft) error,
) error {
	return executor.Executor.Execute(ctx, request, func(draft runs.ResultDraft) error {
		// Keep real protocol execution, but make SLA policy tests independent of
		// clock resolution: a fast local HTTP request can measure zero ms.
		if request.Entry.Performance == nil && draft.Dimensions["phase"] == "measured" {
			draft.Metrics["e2e_ms"] = executor.e2eMS
			if executor.legacy {
				delete(draft.Dimensions, domain.SLAPrecisionDimension)
			}
			if draft.Observation != nil {
				draft.Observation.Metrics["e2e_ms"] = executor.e2eMS
			}
		}
		return emit(draft)
	})
}

func TestMixedPlanExecutesAndPersistsPerformanceItems(t *testing.T) {
	for _, scenario := range []struct {
		name                                     string
		basicFail, pressureFail, slaFail, cancel bool
		firstPolicy, pressurePolicy              string
		requests                                 int32
		stoppedAt                                int
		slaE2EMS                                 float64
		slaZero, legacyPrecision                 bool
	}{
		{name: "cancel_during_pressure", cancel: true, firstPolicy: "continue", pressurePolicy: "continue", requests: 2, stoppedAt: 2},
		{name: "mixed_success", firstPolicy: "stop", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{name: "basic_stop", basicFail: true, firstPolicy: "stop", pressurePolicy: "continue", requests: 1, stoppedAt: 1},
		{name: "basic_continue", basicFail: true, firstPolicy: "continue", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{name: "pressure_stop", pressureFail: true, firstPolicy: "continue", pressurePolicy: "stop", requests: 3, stoppedAt: 2},
		{name: "pressure_continue", pressureFail: true, firstPolicy: "continue", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{
			name: "sla_stop", slaE2EMS: 100, slaFail: true,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 1, stoppedAt: 1,
		},
		{
			name: "sla_continue", slaE2EMS: 100, slaFail: true,
			firstPolicy: "continue", pressurePolicy: "continue", requests: 5, stoppedAt: -1,
		},
		{
			name: "sla_at_limit", slaE2EMS: 50,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 5, stoppedAt: -1,
		},
		{
			name: "sla_below_limit", slaE2EMS: 25,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 5, stoppedAt: -1,
		},
		{name: "sla_zero", slaZero: true,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{name: "sla_round_down", slaE2EMS: 50.004,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{name: "sla_round_up_stop", slaE2EMS: 50.005, slaFail: true,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 1, stoppedAt: 1},
		{name: "sla_round_up_continue", slaE2EMS: 50.005, slaFail: true,
			firstPolicy: "continue", pressurePolicy: "continue", requests: 5, stoppedAt: -1},
		{name: "sla_historical_precision", slaE2EMS: 50.004, slaFail: true, legacyPrecision: true,
			firstPolicy: "stop", pressurePolicy: "continue", requests: 1, stoppedAt: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			var requests atomic.Int32
			pressureStarted := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["stream"] == true {
					if scenario.cancel {
						close(pressureStarted)
						<-r.Context().Done()
						return
					}
					if scenario.pressureFail {
						w.WriteHeader(400)
						fmt.Fprint(w, `{ "error": { "message": "test failure" } }`)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":8,\"completion_tokens\":1,\"total_tokens\":9}}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if scenario.basicFail {
					w.WriteHeader(400)
				}
				fmt.Fprint(w, `{"id":"test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}]}`)
			}))
			defer server.Close()
			repository := openOperationalRepository(t)
			defer repository.Close()
			base := runWithCompleteSnapshot(t)
			snapshot := base.Snapshot()
			snapshot.Channel.BaseURL = server.URL + "/v1"
			snapshot.Entries[0].OnFailure = scenario.firstPolicy
			snapshot.PlanDocument.Entries[0].OnFailure = scenario.firstPolicy
			if scenario.slaE2EMS > 0 || scenario.slaZero {
				snapshot.Entries[0].SLA.Thresholds = map[string]float64{"e2e_p95_ms": 50}
				snapshot.PlanDocument.Entries[0].SLA = snapshot.Entries[0].SLA
			}
			profile := domain.PerformancePlanProfile{LoadMode: domain.LoadFixedConcurrency, ArrivalPattern: "constant", WorkloadMode: "fixed", RequestCount: 2, Concurrency: 1, TimeoutMS: 1000, InputTokens: 8, OutputTokens: 1}
			for i := 1; i <= 2; i++ {
				id := fmt.Sprintf("86000000-0000-4000-8000-%012d", 20+i)
				load := domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 1, RequestCount: 2, RequestTimeoutMS: 1000}
				item := domain.PlanEntry{EntryID: id, TargetID: id, Name: fmt.Sprintf("pressure %d", i), TargetKind: domain.PlanTargetPerformance, OnFailure: scenario.pressurePolicy, Performance: &profile, Load: load}
				snapshot.PlanDocument.Entries = append(snapshot.PlanDocument.Entries, item)
				snapshot.Entries = append(snapshot.Entries, domain.RunEntrySnapshot{EntryID: id, TargetID: id, Name: item.Name, Key: "performance", TargetKind: item.TargetKind, OnFailure: item.OnFailure, Performance: &profile, Load: load, Cases: []domain.CaseRevisionRef{}, CaseDefinitions: []domain.TestCase{}, Parameters: map[string]json.RawMessage{}, CaseInputs: map[string]map[string]json.RawMessage{}})
			}
			run, err := domain.NewRun(base.Meta(), base.PlanID(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if err := repository.CreateRun(t.Context(), run); err != nil {
				t.Fatal(err)
			}
			var executor runs.Executor = runs.NewProtocolExecutor(server.Client().Transport)
			if scenario.slaE2EMS > 0 || scenario.slaZero {
				executor = mixedPlanSLAExecutor{Executor: executor, e2eMS: scenario.slaE2EMS, legacy: scenario.legacyPrecision}
			}
			host, err := runs.NewHost(runs.HostDependencies{
				Repository: repository, Executor: executor, Clock: mixedPlanClock{},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer host.Close()
			lease, _ := credentials.NewTemporaryLease([]byte("test-only"))
			if err := host.AdoptQueued(t.Context(), run.Meta().ID, lease); err != nil {
				t.Fatal(err)
			}
			if err := host.ActivateRun(context.Background(), run.Meta().ID); err != nil {
				t.Fatal(err)
			}
			if scenario.cancel {
				select {
				case <-pressureStarted:
				case <-time.After(5 * time.Second):
					t.Fatal("pressure item did not start")
				}
				if err := host.CancelRun(t.Context(), run.Meta().ID); err != nil {
					t.Fatal(err)
				}
			}
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				stored, err := repository.GetRun(t.Context(), run.Meta().ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.Status() == domain.RunCompleted || stored.Status() == domain.RunFailed || stored.Status() == domain.RunCancelled {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if err := host.Close(); err != nil {
				t.Fatal(err)
			}
			generator, err := reporting.NewGenerator(reporting.GeneratorDependencies{Repository: repository, Clock: mixedPlanClock{}})
			if err != nil {
				t.Fatal(err)
			}
			if err := generator.Generate(t.Context(), run.Meta().ID); err != nil {
				t.Fatal(err)
			}
			projections, err := repository.ListReportProjections(t.Context())
			if err != nil || len(projections) != 1 {
				t.Fatalf("report list: %v %v", projections, err)
			}
			report, err := repository.GetReport(t.Context(), projections[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.EntryReports) != 3 {
				t.Fatalf("items: %d", len(report.EntryReports))
			}
			if scenario.slaE2EMS > 0 || scenario.slaZero {
				entry := report.EntryReports[0]
				wantMetric := domain.MetricValue{Value: scenario.slaE2EMS, Unit: "ms", Samples: 1}
				if got := entry.SLA["e2e_p95_ms"]; got != wantMetric {
					t.Fatalf("persisted SLA = %+v, want %+v", got, wantMetric)
				}
				if entry.Verification.Status != testspec.VerdictPassed {
					t.Fatalf("functional assertions must pass independently of SLA: %+v", entry.Verification)
				}
				if entry.Conclusion.Passed == scenario.slaFail {
					t.Fatalf("entry conclusion = %+v, want SLA failure %t", entry.Conclusion, scenario.slaFail)
				}
				if report.Conclusion.Passed == scenario.slaFail {
					t.Fatalf("plan conclusion = %+v, want SLA failure %t", report.Conclusion, scenario.slaFail)
				}
				if scenario.slaFail {
					wantValue := "100.00"
					if scenario.slaE2EMS == 50.005 {
						wantValue = "50.01"
					}
					wantIssue := "SLA e2e_p95_ms failed: observed " + wantValue + ", threshold 50.00 (2 decimal places)"
					if scenario.legacyPrecision {
						wantIssue = "SLA e2e_p95_ms failed: observed 50, threshold 50"
					}
					if len(entry.Conclusion.Issues) != 1 || entry.Conclusion.Issues[0] != wantIssue {
						t.Fatalf("unexpected failure cause: %+v", entry.Conclusion.Issues)
					}
				}
			}
			if requests.Load() != scenario.requests {
				t.Fatalf("requests = %d, want %d", requests.Load(), scenario.requests)
			}
			if scenario.stoppedAt >= 0 {
				for _, item := range report.EntryReports[scenario.stoppedAt:] {
					if item.Status != domain.EntryReportNotStarted {
						t.Fatalf("later item was not skipped: %+v", item)
					}
				}
			} else if len(report.EntryReports[2].Performance) == 0 {
				t.Fatal("missing pressure report")
			}
			detail, err := reporting.New(repository).Detail(t.Context(), report.ID)
			if err != nil || len(detail.Entries) != 3 || len(detail.RequestResults) != 1 {
				t.Fatalf("report detail: %v", err)
			}
			if _, err := jobcontract.EncodeRunReport(report); err != nil {
				t.Fatalf("remote report encoding: %v", err)
			}
			progress, err := repository.ListRunProjections(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if scenario.cancel {
				if report.EntryReports[1].Status != domain.EntryReportCancelled || len(report.EntryReports[1].Performance) == 0 {
					t.Fatal("cancelled item lost its persisted checkpoint")
				}
				if progress[0].Completed != 1 {
					t.Fatalf("cancelled progress = %d", progress[0].Completed)
				}
			} else if progress[0].Completed != uint64(scenario.requests) {
				t.Fatalf("progress counted checkpoint envelopes: %d, want %d", progress[0].Completed, scenario.requests)
			}

		})
	}
}
