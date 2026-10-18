package reporting

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func TestHistoricalSLAPrecisionPreservesSavedAndDeferredReports(t *testing.T) {
	data, err := os.ReadFile("testdata/legacy-sla-precision-c561055d.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Run     domain.Run
		Results []domain.Result
		Report  domain.Report
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if err := fixture.Report.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, result := range fixture.Results {
		if err := result.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, exists := result.Dimensions[domain.SLAPrecisionDimension]; exists {
			t.Fatal("historical producer unexpectedly emitted the new precision policy")
		}
	}
	if fixture.Report.Conclusion.Passed || fixture.Results[0].Metrics["e2e_ms"] != 1000.004 {
		t.Fatal("fixture must preserve a historical failure within the rounding boundary")
	}
	repository := &fakeReportRepository{run: fixture.Run, results: fixture.Results, evidence: []domain.Evidence{}}
	generator, err := NewGenerator(GeneratorDependencies{
		Repository: repository, Clock: fixedReportClock{now: fixture.Report.GeneratedAt},
		IDFactory: func(time.Time) (string, error) { return fixture.Report.ID, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := generator.Generate(t.Context(), fixture.Run.Meta().ID); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(fixture.Report)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(repository.report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("generating a deferred historical report changed its original meaning")
	}
	unknown := fixture.Results[0]
	unknown.Dimensions = map[string]string{domain.SLAPrecisionDimension: "future_precision"}
	if err := unknown.Validate(); err == nil {
		t.Fatal("unknown precision policy must be rejected")
	}
}

func TestRoundLatencyMatchesDecimalDisplayPrecision(t *testing.T) {
	for _, test := range []struct{ value, want float64 }{
		{value: 0, want: 0}, {value: 0.004, want: 0}, {value: 0.005, want: 0.01},
		{value: 1.005, want: 1.01}, {value: 2.675, want: 2.68},
		{value: 50.004, want: 50}, {value: 50.005, want: 50.01},
		{value: -1.005, want: -1.01}, {value: math.MaxFloat64, want: math.MaxFloat64},
	} {
		if got := roundLatencyMS(test.value); got != test.want {
			t.Fatalf("round(%v) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestSLALatencyUsesTwoDecimalPlacesWithoutChangingRawValues(t *testing.T) {
	for _, test := range []struct {
		name, metric, threshold string
		value, limit            float64
		legacy, fail            bool
	}{
		{name: "zero", metric: "e2e_ms", threshold: "e2e_p95_ms", limit: 50},
		{name: "subprecision limit", metric: "e2e_ms", threshold: "p95_ms", limit: 0.000001},
		{name: "equal", metric: "e2e_ms", threshold: "e2e_p95_ms", value: 50, limit: 50},
		{name: "round down", metric: "e2e_ms", threshold: "e2e_p95_ms", value: 50.004, limit: 50},
		{name: "round up", metric: "e2e_ms", threshold: "e2e_p95_ms", value: 50.005, limit: 50, fail: true},
		{name: "round threshold", metric: "e2e_ms", threshold: "p100_ms", value: 50.006, limit: 50.005},
		{name: "max alias", metric: "e2e_ms", threshold: "max_e2e_ms", value: 50.004, limit: 50.001},
		{name: "min alias", metric: "e2e_ms", threshold: "min_e2e_ms", value: 50.001, limit: 50.004},
		{name: "min breach", metric: "e2e_ms", threshold: "min_e2e_ms", value: 50.004, limit: 50.005, fail: true},
		{name: "decimal tie", metric: "ttft_ms", threshold: "ttft_p100_ms", value: 1.005, limit: 1.01},
		{name: "tpot", metric: "tpot_ms", threshold: "tpot_p95_ms", value: 2.675, limit: 2.68},
		{name: "legacy remains exact", metric: "e2e_ms", threshold: "e2e_p95_ms",
			value: 50.004, limit: 50, legacy: true, fail: true},
		{name: "non latency remains exact", metric: "request_qps", threshold: "request_qps",
			value: 50.004, limit: 50, fail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := domain.Result{
				Dimensions: map[string]string{}, Metrics: map[string]float64{test.metric: test.value},
				Verification: testspec.Verdict{Status: testspec.VerdictPassed},
			}
			if !test.legacy {
				result.Dimensions[domain.SLAPrecisionDimension] = domain.SLALatencyTwoDecimalsV1
			}
			profile := domain.SLAProfile{Thresholds: map[string]float64{test.threshold: test.limit}}
			results := []domain.Result{result}
			observed, issues := evaluateSLA(profile, results, aggregateMetrics(results))
			if (len(issues) > 0) != test.fail {
				t.Fatalf("unexpected SLA verdict: %v", issues)
			}
			if EntryFailed(domain.RunEntrySnapshot{SLA: profile}, results) != test.fail {
				t.Fatal("host and report SLA rules disagree")
			}
			if observed[test.threshold].Value != test.value || result.Metrics[test.metric] != test.value {
				t.Fatal("comparison precision changed the stored measurements")
			}
			if profile.Thresholds[test.threshold] != test.limit {
				t.Fatal("comparison precision changed the authored threshold")
			}
		})
	}
}

func TestSLAPrecisionIsAppliedAfterAggregationAndOnlyToNewMeasurements(t *testing.T) {
	results := []domain.Result{}
	for _, value := range []float64{0.004, 0.004, 0.014} {
		results = append(results, domain.Result{
			Dimensions: map[string]string{domain.SLAPrecisionDimension: domain.SLALatencyTwoDecimalsV1},
			Metrics:    map[string]float64{"e2e_ms": value},
		})
	}
	profile := domain.SLAProfile{Thresholds: map[string]float64{"e2e_ms": 0.004}}
	if _, issues := evaluateSLA(profile, results, aggregateMetrics(results)); len(issues) != 1 {
		t.Fatalf("averaging rounded samples changed the SLA: %v", issues)
	}
	results[0].Dimensions = map[string]string{}
	if usesRoundedLatencySLA(results) {
		t.Fatal("mixed historical measurements were silently reinterpreted")
	}
	results[0].Dimensions["phase"] = "warmup"
	if !usesRoundedLatencySLA(measuredResults(results)) {
		t.Fatal("warmup affected the measured precision policy")
	}
}
