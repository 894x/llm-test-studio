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

	"github.com/894x/llm-test-studio/internal/domain"
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
	snapshot := run.Snapshot()
	reportResults := make([]domain.Result, 0, len(results))
	for _, storedResult := range results {
		result := storedResult
		if snapshot.QuickTask != nil {
			if result.SuiteEntryID != "" || result.SuiteStatus != "" || result.CaseID == "" {
				return errors.New("quick-task result has invalid stored ownership")
			}
			result.SuiteEntryID = runID
		}
		reportResults = append(reportResults, result)
	}
	caseResults := make([]domain.Result, 0, len(reportResults))
	requestResults := make([]domain.Result, 0, len(reportResults))
	for _, result := range reportResults {
		if result.SuiteStatus != "" {
			continue
		}
		if result.RequestID == "" && result.CaseID != "" {
			caseResults = append(caseResults, result)
		} else if result.RequestID != "" {
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
	passed := run.Status() == domain.RunCompleted
	issues := make([]string, 0)
	metrics := aggregateMetrics(requestResults)
	slaMetrics, slaIssues := evaluateSLA(snapshot.SLA, requestResults, metrics)
	if len(slaIssues) != 0 {
		passed = false
		issues = append(issues, slaIssues...)
	}
	failedByCode := make(map[string]int)
	for _, result := range append(append([]domain.Result{}, requestResults...), caseResults...) {
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
	suiteReports, err := buildSuiteReports(snapshot, run.Status(), reportResults)
	if err != nil {
		return fmt.Errorf("build suite reports: %w", err)
	}
	for _, suiteReport := range suiteReports {
		if suiteReport.Conclusion.Passed {
			continue
		}
		passed = false
		for _, issue := range suiteReport.Conclusion.Issues {
			issues = append(issues, fmt.Sprintf("suite %s (%s): %s", suiteReport.SuiteKey, suiteReport.SuiteEntryID, issue))
		}
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
		Timeline: resultTimeline(requestResults), Distributions: append(resultDistributions(requestResults), probeDistributions(requestResults)...),
		CaseResults: append([]domain.Result{}, caseResults...), SuiteReports: suiteReports, ErrorClusters: errorClusters,
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

func buildSuiteReports(snapshot domain.RunSnapshot, runStatus domain.RunStatus, results []domain.Result) ([]domain.SuiteReport, error) {
	if len(snapshot.Suites) == 0 {
		if snapshot.QuickTask == nil {
			return nil, errors.New("flat report generation is reserved for quick tasks")
		}
		caseResults := make([]domain.Result, 0, len(snapshot.Cases))
		requestResults := make([]domain.Result, 0)
		for _, result := range results {
			if result.SuiteEntryID != snapshot.Plan.ID || result.SuiteStatus != "" {
				return nil, errors.New("quick-task report result has invalid synthetic ownership")
			}
			if result.RequestID != "" {
				requestResults = append(requestResults, result)
			} else if result.CaseID != "" {
				caseResults = append(caseResults, result)
			}
		}
		status := domain.SuiteReportFailed
		switch runStatus {
		case domain.RunCompleted:
			status = domain.SuiteReportCompleted
		case domain.RunCancelled:
			status = domain.SuiteReportCancelled
		case domain.RunFailed:
		default:
			return nil, errors.New("quick-task report requires a terminal run")
		}
		metrics := aggregateMetrics(requestResults)
		sla, issues := evaluateSLA(snapshot.SLA, requestResults, metrics)
		passed := status == domain.SuiteReportCompleted
		for _, result := range append(append([]domain.Result{}, requestResults...), caseResults...) {
			if !result.Success.Overall() {
				passed = false
			}
		}
		if len(issues) != 0 {
			passed = false
		}
		verdict := "pass"
		if status == domain.SuiteReportFailed {
			passed = false
			verdict = "fail"
			issues = append([]string{"suite execution failed"}, issues...)
		} else if status == domain.SuiteReportCancelled {
			passed = false
			verdict = "cancelled"
			issues = append([]string{"suite execution was cancelled"}, issues...)
		} else if !passed {
			verdict = "fail"
		}
		suite := snapshot.QuickTask.Suite
		return []domain.SuiteReport{{
			SuiteEntryID: snapshot.Plan.ID, SuiteID: suite.ID, SuiteRevision: suite.Revision,
			SuiteKey: suite.Key, SuiteName: suite.Name, Status: status,
			Conclusion: domain.ReportConclusion{Passed: passed, Verdict: verdict, Issues: issues},
			SLA:        sla, Metrics: metrics, Timeline: resultTimeline(requestResults),
			Distributions: append(resultDistributions(requestResults), probeDistributions(requestResults)...),
			CaseResults:   append([]domain.Result{}, caseResults...),
		}}, nil
	}
	markers := make(map[string]domain.SuiteExecutionStatus, len(snapshot.Suites))
	knownEntries := make(map[string]struct{}, len(snapshot.Suites))
	for _, suite := range snapshot.Suites {
		knownEntries[suite.EntryID] = struct{}{}
	}
	for _, result := range results {
		if result.SuiteStatus == "" {
			continue
		}
		if _, known := knownEntries[result.SuiteEntryID]; !known {
			return nil, fmt.Errorf("suite marker references unknown entry %q", result.SuiteEntryID)
		}
		if _, duplicate := markers[result.SuiteEntryID]; duplicate {
			return nil, fmt.Errorf("duplicate suite marker for entry %q", result.SuiteEntryID)
		}
		markers[result.SuiteEntryID] = result.SuiteStatus
	}

	reports := make([]domain.SuiteReport, 0, len(snapshot.Suites))
	terminalGap := false
	for _, suite := range snapshot.Suites {
		caseResults := make([]domain.Result, 0, len(suite.Cases))
		requestResults := make([]domain.Result, 0)
		for _, result := range results {
			if result.SuiteStatus != "" || result.SuiteEntryID != suite.EntryID {
				continue
			}
			if result.RequestID != "" {
				requestResults = append(requestResults, result)
			} else if result.CaseID != "" {
				caseResults = append(caseResults, result)
			}
		}

		status, marked := markers[suite.EntryID]
		reportStatus := domain.SuiteReportStatus("")
		if marked {
			switch status {
			case domain.SuiteExecutionCompleted:
				reportStatus = domain.SuiteReportCompleted
			case domain.SuiteExecutionFailed:
				reportStatus = domain.SuiteReportFailed
			default:
				return nil, fmt.Errorf("unsupported suite marker status %q", status)
			}
		} else if terminalGap {
			reportStatus = domain.SuiteReportNotStarted
		} else {
			terminalGap = true
			switch runStatus {
			case domain.RunCancelled:
				reportStatus = domain.SuiteReportCancelled
			case domain.RunFailed:
				reportStatus = domain.SuiteReportFailed
			default:
				return nil, fmt.Errorf("completed run is missing a suite marker for entry %q", suite.EntryID)
			}
		}
		if reportStatus == domain.SuiteReportNotStarted && (len(caseResults) != 0 || len(requestResults) != 0) {
			return nil, fmt.Errorf("not-started suite entry %q contains results", suite.EntryID)
		}

		metrics := aggregateMetrics(requestResults)
		sla, issues := evaluateSLA(suite.SLA, requestResults, metrics)
		passed := reportStatus == domain.SuiteReportCompleted
		for _, result := range append(append([]domain.Result{}, requestResults...), caseResults...) {
			if !result.Success.Overall() {
				passed = false
			}
		}
		if len(issues) != 0 {
			passed = false
		}
		switch reportStatus {
		case domain.SuiteReportFailed:
			issues = append([]string{"suite execution failed"}, issues...)
		case domain.SuiteReportCancelled:
			issues = append([]string{"suite execution was cancelled"}, issues...)
		case domain.SuiteReportNotStarted:
			issues = append([]string{"suite execution was not started"}, issues...)
		}
		verdict := "pass"
		if !passed {
			verdict = "fail"
		}
		if reportStatus == domain.SuiteReportCancelled {
			verdict = "cancelled"
		} else if reportStatus == domain.SuiteReportNotStarted {
			verdict = "not_started"
		}
		reports = append(reports, domain.SuiteReport{
			SuiteEntryID:  suite.EntryID,
			SuiteID:       suite.Suite.ID,
			SuiteRevision: suite.Suite.Revision,
			SuiteKey:      suite.Suite.Key,
			SuiteName:     suite.Suite.Name,
			Status:        reportStatus,
			Conclusion:    domain.ReportConclusion{Passed: passed, Verdict: verdict, Issues: issues},
			SLA:           sla,
			Metrics:       metrics,
			Timeline:      resultTimeline(requestResults),
			Distributions: append(
				resultDistributions(requestResults),
				probeDistributions(requestResults)...,
			),
			CaseResults: append([]domain.Result{}, caseResults...),
		})
	}
	return reports, nil
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
		type distributionMetric struct {
			name        string
			successOnly bool
			percentiles []int
		}
		distributionMetrics := []distributionMetric{
			{name: "schedule_lag_ms", percentiles: []int{50, 90, 95, 99}},
			{name: "e2e_ms", successOnly: true, percentiles: []int{50, 90, 95, 99}},
			{name: "ttft_ms", successOnly: true, percentiles: []int{50, 90, 95, 99}},
			{name: "tpot_ms", successOnly: true, percentiles: []int{50, 90, 95, 99}},
			{name: "ttfb_ms", successOnly: true, percentiles: []int{50, 95, 99}},
			{name: "ttft_any_ms", successOnly: true, percentiles: []int{50, 95, 99}},
			{name: "ttft_visible_ms", successOnly: true, percentiles: []int{50, 95, 99}},
			{name: "ttst_ms", successOnly: true, percentiles: []int{50, 95, 99}},
			{name: "observed_icl_ms", successOnly: true, percentiles: []int{50, 95, 99}},
			{name: "semantic_chunk_count", successOnly: true, percentiles: []int{50, 95, 99}},
		}
		for _, metric := range distributionMetrics {
			values := metricSamples(results, metric.name)
			if metric.successOnly {
				values = successfulMetricSamples(results, metric.name)
				delete(metrics, metric.name)
			}
			if len(values) == 0 {
				continue
			}
			sort.Float64s(values)
			stem, suffix := metric.name, ""
			if strings.HasSuffix(metric.name, "_ms") {
				stem, suffix = strings.TrimSuffix(metric.name, "_ms"), "_ms"
			}
			average := 0.0
			for _, value := range values {
				average += value
			}
			average /= float64(len(values))
			metrics[metric.name] = domain.MetricValue{Value: average, Unit: metricUnit(metric.name), Samples: len(values)}
			metrics[fmt.Sprintf("%s_average%s", stem, suffix)] = domain.MetricValue{Value: average, Unit: metricUnit(metric.name), Samples: len(values)}
			for _, percentile := range metric.percentiles {
				metrics[fmt.Sprintf("%s_p%d%s", stem, percentile, suffix)] = domain.MetricValue{
					Value: interpolatedPercentile(values, float64(percentile)/100), Unit: metricUnit(metric.name), Samples: len(values),
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

func successfulMetricSamples(results []domain.Result, name string) []float64 {
	values := make([]float64, 0, len(results))
	for _, result := range results {
		if !result.Success.Overall() {
			continue
		}
		if value, ok := result.Metrics[name]; ok {
			values = append(values, value)
		}
	}
	return values
}

func resultTimeline(results []domain.Result) []json.RawMessage {
	items := make([]json.RawMessage, 0, len(results))
	for _, result := range results {
		values := map[string]any{
			"result_id": result.ID, "request_id": result.RequestID,
			"case_id": result.CaseID, "success": result.Success.Overall(),
		}
		for _, name := range []string{
			"started_offset_ms", "finished_offset_ms", "schedule_lag_ms", "e2e_ms", "ttfb_ms", "ttft_ms", "ttft_any_ms",
			"ttft_visible_ms", "ttst_ms", "observed_icl_ms", "semantic_chunk_count", "tpot_ms", "http_status",
		} {
			if value, ok := result.Metrics[name]; ok {
				values[name] = value
			}
		}
		item, _ := json.Marshal(values)
		items = append(items, item)
	}
	return items
}

func resultDistributions(results []domain.Result) []json.RawMessage {
	distributions := make([]json.RawMessage, 0, 10)
	for _, name := range []string{
		"schedule_lag_ms", "e2e_ms", "ttft_ms", "tpot_ms", "ttfb_ms", "ttft_any_ms", "ttft_visible_ms", "ttst_ms",
		"observed_icl_ms", "semantic_chunk_count",
	} {
		values := metricSamples(results, name)
		if name != "schedule_lag_ms" {
			values = successfulMetricSamples(results, name)
		}
		if len(values) == 0 {
			continue
		}
		sort.Float64s(values)
		fields := map[string]any{
			"metric": name, "unit": metricUnit(name), "samples": len(values),
			"min": values[0], "max": values[len(values)-1],
			"average": averageSamples(values),
			"p50":     interpolatedPercentile(values, .50),
			"p95":     interpolatedPercentile(values, .95), "p99": interpolatedPercentile(values, .99),
		}
		if name == "schedule_lag_ms" || name == "e2e_ms" || name == "ttft_ms" || name == "tpot_ms" {
			fields["p90"] = interpolatedPercentile(values, .90)
		}
		item, _ := json.Marshal(fields)
		distributions = append(distributions, item)
	}
	return distributions
}

type probeDistributionKey struct {
	caseID         string
	bucket         string
	classification string
	format         string
	shape          string
}

func probeDistributions(results []domain.Result) []json.RawMessage {
	counts := make(map[probeDistributionKey]int)
	totals := make(map[string]int)
	for _, result := range results {
		classification := result.Dimensions["probe_classification"]
		bucket := result.Dimensions["probe_bucket"]
		caseID := result.Dimensions["probe_case_id"]
		if result.RequestID == "" || !domain.IsUUID(caseID) || classification == "" || bucket == "" {
			continue
		}
		key := probeDistributionKey{
			caseID: caseID, bucket: bucket, classification: classification,
			format: result.Dimensions["probe_format"], shape: result.Dimensions["probe_shape"],
		}
		counts[key]++
		totals[caseID]++
	}
	keys := make([]probeDistributionKey, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].caseID != keys[right].caseID {
			return keys[left].caseID < keys[right].caseID
		}
		if counts[keys[left]] != counts[keys[right]] {
			return counts[keys[left]] > counts[keys[right]]
		}
		if keys[left].bucket != keys[right].bucket {
			return keys[left].bucket < keys[right].bucket
		}
		return keys[left].shape < keys[right].shape
	})
	distributions := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		count := counts[key]
		item, _ := json.Marshal(map[string]any{
			"kind": "response_probe", "case_id": key.caseID,
			"bucket": key.bucket, "classification": key.classification,
			"format": key.format, "shape": key.shape,
			"count": count, "share_percent": float64(count) * 100 / float64(totals[key.caseID]),
		})
		distributions = append(distributions, item)
	}
	return distributions
}

func averageSamples(values []float64) float64 {
	average := 0.0
	for _, value := range values {
		average += value
	}
	return average / float64(len(values))
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
