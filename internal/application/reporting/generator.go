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
	"sync"
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
	Timing     func(GenerationTiming)
}

type Generator struct {
	repository    ReportRepository
	clock         GeneratorClock
	idFactory     ReportIDFactory
	timing        func(GenerationTiming)
	progressMu    sync.Mutex
	progress      map[string]GenerationProgress
	progressOrder []string
	observers     map[uint64]func(GenerationProgress)
	nextObserver  uint64
	nextProgress  uint64
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
	return &Generator{
		repository: dependencies.Repository, clock: dependencies.Clock, idFactory: factory,
		timing: dependencies.Timing, progress: map[string]GenerationProgress{},
		progressOrder: []string{}, observers: map[uint64]func(GenerationProgress){},
	}, nil
}

func (generator *Generator) Generate(ctx context.Context, runID string) (generationErr error) {
	if generator == nil || ctx == nil || !domain.IsUUID(runID) {
		return ErrGenerationInvalid
	}
	started := time.Now()
	phaseStarted := started
	phase := ""
	advance := func(next string, processed, total uint64, reportID string) {
		if next != phase && phase != "" && generator.timing != nil {
			generator.timing(GenerationTiming{RunID: runID, Phase: phase, Duration: time.Since(phaseStarted)})
		}
		if next != phase {
			phaseStarted = time.Now()
		}
		phase = next
		generator.publishProgress(GenerationProgress{
			RunID: runID, Phase: next, Processed: processed, Total: total,
			ElapsedMS: uint64(time.Since(started).Milliseconds()), ReportID: reportID,
		})
	}
	defer func() {
		if generationErr != nil {
			advance("failed", 0, 0, "")
		}
	}()
	advance("loading_run", 0, 0, "")
	run, err := generator.repository.GetRun(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report run: %w", err)
	}
	switch run.Status() {
	case domain.RunCompleted, domain.RunFailed, domain.RunCancelled:
	default:
		return ErrGenerationInvalid
	}
	advance("loading_results", 0, 0, "")
	results, err := generator.repository.ListResults(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report results: %w", err)
	}
	advance("loading_evidence", 0, 0, "")
	evidence, err := generator.repository.ListEvidence(ctx, runID)
	if err != nil {
		return fmt.Errorf("load report evidence: %w", err)
	}
	snapshot := run.Snapshot()
	advance("building", 0, uint64(len(results)), "")
	caseResults, requestResults := []domain.Result{}, []domain.Result{}
	for index, result := range results {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := result.Validate(); err != nil {
			return fmt.Errorf("invalid stored result: %w", err)
		}
		if (index+1)%64 == 0 || index+1 == len(results) {
			advance("building", uint64(index+1), uint64(len(results)), "")
		}
		if result.EntryStatus != "" {
			continue
		}
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
	entries, err := buildEntryReports(snapshot, run.Status(), results)
	if err != nil {
		return err
	}
	verification := domain.SummarizeVerification(requestResults)
	issues := []string{}
	for _, entry := range entries {
		for _, issue := range entry.Conclusion.Issues {
			issues = append(issues, fmt.Sprintf("%s: %s", entry.Name, issue))
		}
	}
	conclusion := verificationConclusion(verification, run.Status() == domain.RunCompleted, issues)
	if run.Status() == domain.RunCancelled {
		conclusion.Verdict = "cancelled"
	}
	report := domain.Report{
		SchemaVersion: domain.CurrentReportSchemaVersion, Protocol: snapshot.Model.Protocol, Verification: verification,
		ID: reportID, RunID: runID, RunStatus: run.Status(), GeneratedAt: generatedAt, PlanSnapshot: snapshot,
		Model: domain.ReportSubject{ID: snapshot.Model.ID, Name: snapshot.Model.Name}, Channel: domain.ReportSubject{ID: snapshot.Channel.ID, Name: snapshot.Channel.Name}, Environment: snapshot.Environment,
		Conclusion: conclusion, SLA: map[string]domain.MetricValue{}, Metrics: aggregateMetrics(requestResults), Timeline: resultTimeline(requestResults), Distributions: resultDistributions(requestResults),
		CaseResults: caseResults, EntryReports: entries, ErrorClusters: []json.RawMessage{}, Evidence: append([]domain.Evidence{}, evidence...), Baseline: json.RawMessage(`{}`), Attachments: []domain.ReportAttachment{},
	}
	// The storage boundary validates the complete report before any write.
	advance("persisting", 0, 0, "")
	if err := generator.repository.CreateReport(ctx, report); err != nil {
		return fmt.Errorf("persist report: %w", err)
	}
	advance("ready", 0, 0, report.ID)
	return nil
}

func verificationConclusion(summary domain.VerificationSummary, completed bool, issues []string) domain.ReportConclusion {
	verdict := string(summary.Status)
	if verdict == "passed" {
		verdict = "pass"
	}
	if verdict == "failed" {
		verdict = "fail"
	}
	if !completed || len(issues) > 0 {
		verdict = "fail"
	}
	return domain.ReportConclusion{Passed: verdict == "pass", Verdict: verdict, Issues: issues}
}

func buildEntryReports(snapshot domain.RunSnapshot, runStatus domain.RunStatus, results []domain.Result) ([]domain.EntryReport, error) {
	known := map[string]bool{}
	byEntry := make(map[string][]domain.Result, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		known[entry.EntryID] = true
	}
	for _, result := range results {
		if !known[result.EntryID] {
			return nil, errors.New("result refers to unknown plan entry")
		}
		byEntry[result.EntryID] = append(byEntry[result.EntryID], result)
	}
	reports := make([]domain.EntryReport, 0, len(snapshot.Entries))
	stopped := false
	for _, entry := range snapshot.Entries {
		summaries, requests := []domain.Result{}, []domain.Result{}
		status := domain.EntryReportNotStarted
		for _, result := range byEntry[entry.EntryID] {
			if result.EntryStatus == domain.EntryExecutionCompleted {
				status = domain.EntryReportCompleted
				continue
			}
			if result.EntryStatus == domain.EntryExecutionFailed {
				status = domain.EntryReportFailed
				continue
			}
			if result.RequestID == "" {
				summaries = append(summaries, result)
			} else {
				requests = append(requests, result)
			}
		}
		if status == domain.EntryReportNotStarted && !stopped {
			if runStatus == domain.RunCancelled {
				status = domain.EntryReportCancelled
			} else {
				status = domain.EntryReportFailed
			}
			stopped = true
		}
		if status == domain.EntryReportNotStarted && (len(requests) > 0 || len(summaries) > 0) {
			return nil, errors.New("not-started entry contains results")
		}
		verification := domain.SummarizeVerification(requests)
		metrics := aggregateMetrics(requests)
		sla, issues := evaluateSLA(entry.SLA, requests, metrics)
		conclusion := verificationConclusion(verification, status == domain.EntryReportCompleted, issues)
		if status == domain.EntryReportCancelled || status == domain.EntryReportNotStarted {
			conclusion.Verdict = string(status)
		}
		if status == domain.EntryReportFailed {
			conclusion.Issues = append(conclusion.Issues, "entry execution failed")
		}
		reports = append(reports, domain.EntryReport{EntryID: entry.EntryID, WarmupCount: entry.WarmupCount, Settings: entry.Settings, TargetKind: entry.TargetKind, TargetID: entry.TargetID, Name: entry.Name, Key: entry.Key, Protocol: snapshot.Model.Protocol,
			Parameters: entry.Parameters, Load: entry.Load, Seed: snapshot.PlanDocument.Seed, Status: status, Conclusion: conclusion, Verification: verification, SLA: sla, Metrics: metrics,
			Timeline: resultTimeline(requests), Distributions: resultDistributions(requests), CaseResults: summaries})
	}
	return reports, nil
}

func aggregateMetrics(results []domain.Result) map[string]domain.MetricValue {
	results = measuredResults(results)
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
		verification := domain.SummarizeVerification(results)
		verified := verification.Passed + verification.Failed
		if verified > 0 {
			metrics["success_rate"] = domain.MetricValue{Value: float64(verification.Passed) / float64(verified), Unit: "ratio", Samples: int(verified)}
		}
		for name, count := range map[string]uint64{"completed_count": uint64(len(results)), "succeeded_count": verification.Passed, "failed_count": verification.Failed, "observed_count": verification.Observed, "indeterminate_count": verification.Indeterminate} {
			metrics[name] = domain.MetricValue{Value: float64(count), Unit: "count", Samples: len(results)}
		}

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
				values = metricSamples(results, metric.name)
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
	results = measuredResults(results)
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
	results = measuredResults(results)
	items := make([]json.RawMessage, 0, len(results))
	for _, result := range results {
		values := map[string]any{
			"result_id": result.ID, "request_id": result.RequestID,
			"case_id": result.CaseID, "verification": result.Verification.Status,
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
	results = measuredResults(results)
	distributions := make([]json.RawMessage, 0, 10)
	for _, name := range []string{
		"schedule_lag_ms", "e2e_ms", "ttft_ms", "tpot_ms", "ttfb_ms", "ttft_any_ms", "ttft_visible_ms", "ttst_ms",
		"observed_icl_ms", "semantic_chunk_count",
	} {
		values := metricSamples(results, name)
		if name != "schedule_lag_ms" {
			values = metricSamples(results, name)
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

func averageSamples(values []float64) float64 {
	average := 0.0
	for _, value := range values {
		average += value
	}
	return average / float64(len(values))
}

func metricUnit(name string) string {
	switch {
	case strings.HasPrefix(name, "tpot_") && strings.HasSuffix(name, "_ms"):
		return "ms/token"
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

func measuredResults(results []domain.Result) []domain.Result {
	filtered := make([]domain.Result, 0, len(results))
	for _, result := range results {
		if result.Dimensions["phase"] != "warmup" {
			filtered = append(filtered, result)
		}
	}
	return filtered
}
