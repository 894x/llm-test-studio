package reporting

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

// MaxComparisonReports bounds detail and evidence reads for an interactive view.
const MaxComparisonReports = 16

type Comparison struct {
	SchemaVersion int             `json:"schema_version"`
	BaselineID    string          `json:"baseline_id"`
	Reports       []Detail        `json:"reports"`
	Rows          []ComparisonRow `json:"rows"`
}

type ComparisonRow struct {
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	EntryName string           `json:"entry_name"`
	Different bool             `json:"different"`
	Cells     []ComparisonCell `json:"cells"`
}

type ComparisonCell struct {
	ReportID string             `json:"report_id"`
	EntryID  string             `json:"entry_id"`
	CaseID   string             `json:"case_id"`
	Status   string             `json:"status"`
	Change   string             `json:"change"`
	Metrics  []ComparisonMetric `json:"metrics"`
}

type ComparisonMetric struct {
	Key     string   `json:"key"`
	Value   float64  `json:"value"`
	Unit    string   `json:"unit"`
	Samples int      `json:"samples"`
	Delta   *float64 `json:"delta"`
}

type comparisonObservation struct {
	cell       ComparisonCell
	conditions string
}

// Compare projects sealed reports without rewriting history or evaluating old
// assertions with current rules. Model and channel are comparison dimensions.
func Compare(details []Detail, baselineID string) (Comparison, error) {
	if len(details) < 2 || len(details) > MaxComparisonReports {
		return Comparison{}, fmt.Errorf("comparison requires 2 to %d reports", MaxComparisonReports)
	}
	result := Comparison{SchemaVersion: 1, BaselineID: baselineID, Reports: details, Rows: []ComparisonRow{}}
	seen := make(map[string]bool, len(details))
	observations := make(map[string]map[string]comparisonObservation)
	entryKeys := make(map[string]string)
	for _, detail := range details {
		id := comparisonReportID(detail)
		if detail.SchemaVersion != 1 || !domain.IsUUID(id) || seen[id] {
			return Comparison{}, fmt.Errorf("invalid or duplicate comparison report")
		}
		seen[id] = true
		if err := appendComparisonReport(
			&result,
			observations,
			entryKeys,
			detail,
		); err != nil {
			return Comparison{}, err
		}
	}
	if !seen[baselineID] {
		return Comparison{}, fmt.Errorf("baseline must belong to the comparison")
	}
	for index := range result.Rows {
		row := &result.Rows[index]
		values := observations[row.Key]
		baseline, hasBaseline := values[baselineID]
		for _, detail := range details {
			id := comparisonReportID(detail)
			observation, exists := values[id]
			cell := ComparisonCell{ReportID: id, Status: "not_run", Change: "missing", Metrics: []ComparisonMetric{}}
			if exists {
				cell = observation.cell
				cell.Metrics = append([]ComparisonMetric{}, cell.Metrics...)
				cell.Change = comparisonChange(observation, baseline, hasBaseline)
				if id == baselineID {
					cell.Change = "baseline"
				}
				if id != baselineID && hasBaseline && observation.conditions == baseline.conditions {
					addComparisonDeltas(&cell, baseline.cell)
				}
			}
			if id != baselineID && cell.Change != "same" {
				row.Different = true
			}
			row.Cells = append(row.Cells, cell)
		}
	}
	return result, nil
}

func comparisonReportID(detail Detail) string {
	if detail.Source == SourceQuickPerformance && detail.Performance != nil {
		return detail.Performance.ReportID
	}
	return detail.Report.ID
}

func appendComparisonReport(
	result *Comparison,
	values map[string]map[string]comparisonObservation,
	entryKeys map[string]string,
	detail Detail,
) error {
	id := comparisonReportID(detail)
	appendValue := func(key, name, entryName string, value comparisonObservation) {
		if values[key] == nil {
			values[key] = make(map[string]comparisonObservation)
			result.Rows = append(result.Rows, ComparisonRow{Key: key, Name: name, EntryName: entryName, Cells: []ComparisonCell{}})
		}
		values[key][id] = value
	}
	if detail.Source == SourceQuickPerformance && detail.Performance != nil {
		value, err := performanceComparison(*detail.Performance, id, "")
		if err != nil {
			return err
		}
		appendValue("quick_performance", "", "", value)
		return nil
	}
	if detail.Source != SourceRun {
		return fmt.Errorf("unsupported comparison report source")
	}
	occurrences := make(map[string]int)
	reserved := make(map[string]bool)
	used := make(map[string]bool)
	for _, entry := range detail.Entries {
		identity := string(entry.TargetKind) + ":" + entry.TargetID + ":" + entry.EntryID
		if key, exists := entryKeys[identity]; exists {
			reserved[key] = true
		}
	}
	for index, entry := range detail.Entries {
		target := string(entry.TargetKind) + ":" + entry.TargetID
		occurrences[target]++
		entryKey := target + ":" + strconv.Itoa(occurrences[target])
		identity := target + ":" + entry.EntryID
		if known, exists := entryKeys[identity]; exists && !used[known] {
			entryKey = known
		} else {
			for reserved[entryKey] || used[entryKey] {
				occurrences[target]++
				entryKey = target + ":" + strconv.Itoa(occurrences[target])
			}
			entryKeys[identity] = entryKey
		}
		used[entryKey] = true
		if len(entry.Performance) > 0 {
			var performance quicktest.PerformanceReport
			if err := json.Unmarshal(entry.Performance, &performance); err != nil {
				return err
			}
			value, err := performanceComparison(performance, id, entry.EntryID)
			if err != nil {
				return err
			}
			appendValue(entryKey, entry.Name, entry.Name, value)
			continue
		}
		for caseIndex, testCase := range entry.Cases {
			if index >= len(detail.Report.PlanSnapshot.Entries) {
				return fmt.Errorf("comparison requires the frozen entry snapshot")
			}
			snapshot := detail.Report.PlanSnapshot.Entries[index]
			if caseIndex >= len(snapshot.CaseDefinitions) {
				return fmt.Errorf("comparison requires the frozen case definition")
			}
			conditions, err := json.Marshal(struct {
				Protocol   domain.Protocol
				Definition domain.TestCase
				Parameters map[string]json.RawMessage
				Inputs     map[string]json.RawMessage
				Load       domain.LoadProfile
				Settings   testspec.RunSettings
				SLA        domain.SLAProfile
				Warmup     uint32
				Seed       uint64
			}{
				Protocol: entry.Protocol, Definition: snapshot.CaseDefinitions[caseIndex],
				Parameters: entry.Parameters, Inputs: snapshot.CaseInputs[testCase.CaseID],
				Load: entry.Load, Settings: entry.Settings, SLA: snapshot.SLA, Warmup: entry.WarmupCount, Seed: entry.Seed,
			})
			if err != nil {
				return err
			}
			status := string(testCase.Verification.Status)
			if testCase.SummaryResult == nil && len(testCase.RequestResults) == 0 {
				status = "not_run"
			}
			appendValue(entryKey+":"+testCase.CaseID, testCase.Name, entry.Name, comparisonObservation{
				conditions: string(conditions),
				cell: ComparisonCell{ReportID: id, EntryID: entry.EntryID, CaseID: testCase.CaseID,
					Status: status, Metrics: comparisonMetrics(testCase.Metrics)},
			})
		}
	}
	return nil
}

func performanceComparison(report quicktest.PerformanceReport, reportID, entryID string) (comparisonObservation, error) {
	conditions, err := json.Marshal(struct {
		Protocol domain.Protocol
		Profile  quicktest.PerformanceProfile
	}{Protocol: report.Protocol, Profile: report.Profile})
	if err != nil {
		return comparisonObservation{}, err
	}
	status := "failed"
	if report.Success {
		status = "passed"
	}
	if report.Progress.Stopped || report.Progress.Phase != "completed" {
		status = "indeterminate"
	}
	metrics := []ComparisonMetric{}
	for _, metric := range performanceOverview(&report).Metrics {
		samples := report.Metrics.Completed
		switch metric.Key {
		case "ttft_p95":
			samples = report.Metrics.TTFTSamples
		case "tpot_p95":
			samples = performanceTPOTSamples(report.Samples)
		case "e2e_p95":
			samples = report.Metrics.Succeeded
		case "slo_rate":
			if report.SLOAssessment != nil {
				samples = report.SLOAssessment.TotalRequests
			}
		}
		if metric.Value != nil && samples > 0 {
			metrics = append(metrics, ComparisonMetric{Key: metric.Key, Value: *metric.Value, Unit: metric.Unit,
				Samples: int(samples)})
		}
	}
	return comparisonObservation{conditions: string(conditions), cell: ComparisonCell{
		ReportID: reportID, EntryID: entryID, Status: status, Metrics: metrics,
	}}, nil
}

func comparisonMetrics(values map[string]domain.MetricValue) []ComparisonMetric {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	metrics := make([]ComparisonMetric, 0, len(keys))
	for _, key := range keys {
		metric := values[key]
		if metric.Samples > 0 {
			metrics = append(metrics, ComparisonMetric{Key: key, Value: metric.Value, Unit: metric.Unit, Samples: metric.Samples})
		}
	}
	return metrics
}

func comparisonChange(value, baseline comparisonObservation, hasBaseline bool) string {
	if !hasBaseline {
		return "added"
	}
	if value.conditions != baseline.conditions {
		return "conditions_changed"
	}
	if value.cell.Status != baseline.cell.Status {
		switch {
		case baseline.cell.Status == "passed" && value.cell.Status == "failed":
			return "regressed"
		case baseline.cell.Status == "failed" && value.cell.Status == "passed":
			return "recovered"
		default:
			return "changed"
		}
	}
	if !reflect.DeepEqual(value.cell.Metrics, baseline.cell.Metrics) {
		return "changed"
	}
	return "same"
}

func addComparisonDeltas(cell *ComparisonCell, baseline ComparisonCell) {
	for index := range cell.Metrics {
		metric := &cell.Metrics[index]
		for _, other := range baseline.Metrics {
			if metric.Key == other.Key && metric.Unit == other.Unit {
				delta := metric.Value - other.Value
				metric.Delta = &delta
				break
			}
		}
	}
}
