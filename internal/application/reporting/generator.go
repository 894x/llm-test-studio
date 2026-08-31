package reporting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/894x/llm-studio/internal/domain"
)

var ErrGenerationInvalid = errors.New("reporting: invalid generation request")

type ReportRepository interface {
	GetRun(context.Context, string) (domain.Run, error)
	ListResults(context.Context, string) ([]domain.Result, error)
	ListEvidence(context.Context, string) ([]domain.Evidence, error)
	CreateReport(context.Context, domain.Report) error
}

type GeneratorClock interface {
	Now() time.Time
}

type ReportIDFactory func(time.Time) (string, error)

type GeneratorDependencies struct {
	Repository ReportRepository
	Clock      GeneratorClock
	IDFactory  ReportIDFactory
}

type Generator struct {
	repository ReportRepository
	clock      GeneratorClock
	idFactory  ReportIDFactory
}

func NewGenerator(dependencies GeneratorDependencies) (*Generator, error) {
	if nilValue(dependencies.Repository) || nilValue(dependencies.Clock) {
		return nil, ErrGenerationInvalid
	}
	factory := dependencies.IDFactory
	if factory == nil {
		factory = func(now time.Time) (string, error) {
			meta, err := domain.NewEntityMeta(now)
			return meta.ID, err
		}
	}
	return &Generator{repository: dependencies.Repository, clock: dependencies.Clock, idFactory: factory}, nil
}

func (generator *Generator) Generate(ctx context.Context, runID string) error {
	if generator == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrGenerationInvalid
	}
	run, err := generator.repository.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report run: %w", err)
	}
	switch run.Status() {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
	default:
		return ErrGenerationInvalid
	}
	results, err := generator.repository.ListResults(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report results: %w", err)
	}
	evidence, err := generator.repository.ListEvidence(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report evidence: %w", err)
	}
	caseResults := make([]domain.Result, 0, len(results))
	requestResults := make([]domain.Result, 0, len(results))
	for _, result := range results {
		if result.RequestID == "" {
			caseResults = append(caseResults, result)
		} else {
			requestResults = append(requestResults, result)
		}
	}
	generatedAt := generator.clock.Now().UTC()
	if generatedAt.Before(run.Meta().UpdatedAt) {
		return ErrGenerationInvalid
	}
	reportID, err := generator.idFactory(generatedAt)
	if err != nil || !domain.IsUUID(reportID) {
		return ErrGenerationInvalid
	}
	snapshot := run.Snapshot()
	passed := run.Status() == domain.RunCompleted
	issues := make([]string, 0)
	metrics := aggregateMetrics(requestResults)
	slaMetrics, slaIssues := evaluateSLA(snapshot.SLA, requestResults, metrics)
	if len(slaIssues) != 0 {
		passed = false
		issues = append(issues, slaIssues...)
	}
	failedByCode := make(map[string]int)
	for _, result := range results {
		if !result.Success.Overall() {
			passed = false
			code := string(result.ErrorCode)
			failedByCode[code]++
		}
	}
	if run.Status() == domain.RunCancelled {
		issues = append(issues, "run was cancelled before normal completion")
	} else if run.Status() == domain.RunFailed {
		issues = append(issues, "run failed before normal completion")
	}
	codes := make([]string, 0, len(failedByCode))
	for code := range failedByCode {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	errorClusters := make([]json.RawMessage, 0, len(codes))
	for _, code := range codes {
		issues = append(issues, fmt.Sprintf("%s: %d result(s)", code, failedByCode[code]))
		item, _ := json.Marshal(map[string]any{"error_code": code, "count": failedByCode[code]})
		errorClusters = append(errorClusters, item)
	}
	verdict := "pass"
	if !passed {
		verdict = "fail"
		if run.Status() == domain.RunCancelled {
			verdict = "cancelled"
		}
	}
	report := domain.Report{
		SchemaVersion: domain.CurrentReportSchemaVersion, ID: reportID, RunID: runID,
		RunStatus: run.Status(), GeneratedAt: generatedAt, PlanSnapshot: snapshot,
		Model:       domain.ReportSubject{ID: snapshot.Model.ID, Name: snapshot.Model.Name},
		Channel:     domain.ReportSubject{ID: snapshot.Channel.ID, Name: snapshot.Channel.Name},
		Environment: snapshot.Environment,
		Conclusion:  domain.ReportConclusion{Passed: passed, Verdict: verdict, Issues: issues},
		SLA:         slaMetrics, Metrics: metrics,
		Timeline: resultTimeline(requestResults), Distributions: resultDistributions(requestResults),
		CaseResults: append([]domain.Result{}, caseResults...), ErrorClusters: errorClusters,
		Evidence: append([]domain.Evidence{}, evidence...), Baseline: json.RawMessage(`{}`),
		Attachments: []domain.ReportAttachment{},
	}
	if err := report.Validate(); err != nil {
		return fmt.Errorf("build report: %w", err)
	}
	if err := generator.repository.CreateReport(ctx, report); err != nil {
		return fmt.Errorf("persist report: %w", err)
	}
	return nil
}

func aggregateMetrics(results []domain.Result) map[string]domain.MetricValue {
	totals := make(map[string]float64)
	counts := make(map[string]int)
	for _, result := range results {
		for name, value := range result.Metrics {
			totals[name] += value
			counts[name]++
		}
	}
	metrics := make(map[string]domain.MetricValue, len(totals)+1)
	for name, total := range totals {
		metrics[name] = domain.MetricValue{Value: total / float64(counts[name]), Unit: metricUnit(name), Samples: counts[name]}
	}
	if len(results) != 0 {
		succeeded := 0
		for _, result := range results {
			if result.Success.Overall() {
				succeeded++
			}
		}
		metrics["success_rate"] = domain.MetricValue{Value: float64(succeeded) / float64(len(results)), Unit: "ratio", Samples: len(results)}
		metrics["completed_count"] = domain.MetricValue{Value: float64(len(results)), Unit: "count", Samples: len(results)}
		metrics["succeeded_count"] = domain.MetricValue{Value: float64(succeeded), Unit: "count", Samples: len(results)}
		metrics["failed_count"] = domain.MetricValue{Value: float64(len(results) - succeeded), Unit: "count", Samples: len(results)}
		elapsedMS := maximumSample(results, "finished_offset_ms")
		if elapsedMS <= 0 {
			for _, result := range results {
				elapsedMS += result.Metrics["e2e_ms"]
			}
		}
		if elapsedMS > 0 {
			seconds := elapsedMS / 1000
			minutes := seconds / 60
			metrics["elapsed_seconds"] = domain.MetricValue{Value: seconds, Unit: "seconds", Samples: len(results)}
			metrics["request_qps"] = domain.MetricValue{Value: float64(len(results)) / seconds, Unit: "requests/second", Samples: len(results)}
			metrics["rpm"] = domain.MetricValue{Value: float64(len(results)) / minutes, Unit: "requests/minute", Samples: len(results)}
			prompt, completion, cached := sumMetric(results, "prompt_tokens"), sumMetric(results, "completion_tokens"), sumMetric(results, "cached_tokens")
			metrics["input_tpm"] = domain.MetricValue{Value: prompt / minutes, Unit: "tokens/minute", Samples: len(results)}
			metrics["output_tpm"] = domain.MetricValue{Value: completion / minutes, Unit: "tokens/minute", Samples: len(results)}
			metrics["total_tpm"] = domain.MetricValue{Value: (prompt + completion) / minutes, Unit: "tokens/minute", Samples: len(results)}
			if prompt > 0 {
				metrics["cache_rate_percent"] = domain.MetricValue{Value: cached / prompt * 100, Unit: "percent", Samples: len(results)}
			}
			generationSeconds := 0.0
			for _, result := range results {
				generationSeconds += math.Max(0, result.Metrics["e2e_ms"]-result.Metrics["ttft_ms"]) / 1000
			}
			if generationSeconds > 0 {
				metrics["generation_tps"] = domain.MetricValue{Value: completion / generationSeconds, Unit: "tokens/second", Samples: len(results)}
			}
		}
		for _, metricName := range []string{"schedule_lag_ms", "e2e_ms", "ttft_ms", "tpot_ms"} {
			values := metricSamples(results, metricName)
			if len(values) == 0 {
				continue
			}
			sort.Float64s(values)
			stem, suffix := metricName, ""
			if strings.HasSuffix(metricName, "_ms") {
				stem, suffix = strings.TrimSuffix(metricName, "_ms"), "_ms"
			}
			for _, percentile := range []int{50, 90, 95, 99} {
				metrics[fmt.Sprintf("%s_p%d%s", stem, percentile, suffix)] = domain.MetricValue{
					Value: interpolatedPercentile(values, float64(percentile)/100), Unit: metricUnit(metricName), Samples: len(values),
				}
			}
		}
	}
	return metrics
}

func sumMetric(results []domain.Result, name string) float64 {
	total := 0.0
	for _, result := range results {
		total += result.Metrics[name]
	}
	return total
}

func maximumSample(results []domain.Result, name string) float64 {
	maximum := 0.0
	for _, result := range results {
		maximum = math.Max(maximum, result.Metrics[name])
	}
	return maximum
}

func interpolatedPercentile(sortedValues []float64, percentile float64) float64 {
	if len(sortedValues) == 0 {
		return 0
	}
	if len(sortedValues) == 1 {
		return sortedValues[0]
	}
	position := percentile * float64(len(sortedValues)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sortedValues[lower]
	}
	weight := position - float64(lower)
	return sortedValues[lower]*(1-weight) + sortedValues[upper]*weight
}

func evaluateSLA(profile domain.SLAProfile, results []domain.Result, aggregates map[string]domain.MetricValue) (map[string]domain.MetricValue, []string) {
	observed := make(map[string]domain.MetricValue, len(profile.Thresholds))
	issues := make([]string, 0)
	names := make([]string, 0, len(profile.Thresholds))
	for name := range profile.Thresholds {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		threshold := profile.Thresholds[name]
		metric, comparison, supported := slaObservation(name, results, aggregates)
		if !supported {
			observed[name] = domain.MetricValue{Value: 0, Unit: metricUnit(name), Samples: 0}
			issues = append(issues, fmt.Sprintf("unsupported SLA threshold %s", name))
			continue
		}
		observed[name] = metric
		if metric.Samples == 0 {
			issues = append(issues, fmt.Sprintf("SLA threshold %s has no samples", name))
			continue
		}
		failed := comparison == "min" && metric.Value < threshold || comparison == "max" && metric.Value > threshold
		if failed {
			issues = append(issues, fmt.Sprintf("SLA %s failed: observed %.4g, threshold %.4g", name, metric.Value, threshold))
		}
	}
	return observed, issues
}

func slaObservation(name string, results []domain.Result, aggregates map[string]domain.MetricValue) (domain.MetricValue, string, bool) {
	if name == "success_rate" {
		metric, ok := aggregates[name]
		return metric, "min", ok
	}
	if name == "success_rate_percent" {
		metric, ok := aggregates["success_rate"]
		metric.Value *= 100
		metric.Unit = "percent"
		return metric, "min", ok
	}
	if strings.HasPrefix(name, "p") && strings.HasSuffix(name, "_ms") {
		percentile, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "p"), "_ms"))
		if err == nil && percentile > 0 && percentile <= 100 {
			values := metricSamples(results, "e2e_ms")
			if len(values) == 0 {
				return domain.MetricValue{Unit: "ms"}, "max", true
			}
			sort.Float64s(values)
			index := int(math.Ceil(float64(percentile)*float64(len(values))/100.0)) - 1
			return domain.MetricValue{Value: values[index], Unit: "ms", Samples: len(values)}, "max", true
		}
	}
	if strings.HasPrefix(name, "min_") {
		metric, ok := aggregates[strings.TrimPrefix(name, "min_")]
		return metric, "min", ok
	}
	if strings.HasPrefix(name, "max_") {
		metricName := strings.TrimPrefix(name, "max_")
		values := metricSamples(results, metricName)
		if len(values) == 0 {
			return domain.MetricValue{Unit: metricUnit(metricName)}, "max", true
		}
		value := values[0]
		for _, candidate := range values[1:] {
			value = math.Max(value, candidate)
		}
		return domain.MetricValue{Value: value, Unit: metricUnit(metricName), Samples: len(values)}, "max", true
	}
	percentileMarker := strings.LastIndex(name, "_p")
	if percentileMarker > 0 {
		rest := name[percentileMarker+2:]
		separator := strings.IndexByte(rest, '_')
		if separator > 0 {
			percentile, err := strconv.Atoi(rest[:separator])
			metricName := name[:percentileMarker] + rest[separator:]
			if err == nil && percentile > 0 && percentile <= 100 {
				values := metricSamples(results, metricName)
				if len(values) == 0 {
					return domain.MetricValue{Unit: metricUnit(metricName)}, "max", true
				}
				sort.Float64s(values)
				index := int(math.Ceil(float64(percentile)*float64(len(values))/100.0)) - 1
				return domain.MetricValue{Value: values[index], Unit: metricUnit(metricName), Samples: len(values)}, "max", true
			}
		}
	}
	metric, ok := aggregates[name]
	return metric, "max", ok
}

func metricSamples(results []domain.Result, name string) []float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		if value, ok := result.Metrics[name]; ok {
			values = append(values, value)
		}
	}
	return values
}

func resultTimeline(results []domain.Result) []json.RawMessage {
	items := make([]json.RawMessage, 0, len(results))
	for _, result := range results {
		item, _ := json.Marshal(map[string]any{
			"result_id": result.ID, "request_id": result.RequestID,
			"case_id": result.CaseID, "success": result.Success.Overall(),
			"started_offset_ms":  result.Metrics["started_offset_ms"],
			"finished_offset_ms": result.Metrics["finished_offset_ms"],
			"schedule_lag_ms":    result.Metrics["schedule_lag_ms"],
			"e2e_ms":             result.Metrics["e2e_ms"], "ttft_ms": result.Metrics["ttft_ms"],
			"tpot_ms": result.Metrics["tpot_ms"], "http_status": result.Metrics["http_status"],
		})
		items = append(items, item)
	}
	return items
}

func resultDistributions(results []domain.Result) []json.RawMessage {
	distributions := make([]json.RawMessage, 0, 4)
	for _, name := range []string{"schedule_lag_ms", "e2e_ms", "ttft_ms", "tpot_ms"} {
		values := metricSamples(results, name)
		if len(values) == 0 {
			continue
		}
		sort.Float64s(values)
		item, _ := json.Marshal(map[string]any{
			"metric": name, "unit": metricUnit(name), "samples": len(values),
			"min": values[0], "max": values[len(values)-1],
			"p50": interpolatedPercentile(values, .50), "p90": interpolatedPercentile(values, .90),
			"p95": interpolatedPercentile(values, .95), "p99": interpolatedPercentile(values, .99),
		})
		distributions = append(distributions, item)
	}
	return distributions
}

func metricUnit(name string) string {
	switch {
	case strings.HasSuffix(name, "_ms"):
		return "ms"
	case strings.Contains(name, "token"):
		return "tokens"
	case strings.Contains(name, "rate") || strings.Contains(name, "ratio"):
		return "ratio"
	case strings.HasSuffix(name, "_seconds"):
		return "seconds"
	case strings.Contains(name, "status") || strings.Contains(name, "count"):
		return "count"
	default:
		return "value"
	}
}

func nilValue(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
