package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/execution/load"
)

func upgradeQuickPerformanceDocument(raw []byte) ([]byte, error) {
	var envelope struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	var report quicktest.PerformanceReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	switch envelope.SchemaVersion {
	case 1:
		if report.Profile.LoadMode == "" {
			upgradeLegacyV1Performance(&report)
		}
	case 2, 3:
	default:
		return nil, fmt.Errorf("unsupported quick performance schema version %d", envelope.SchemaVersion)
	}
	upgradePerformanceSampleTelemetry(&report)
	if report.Progress.Offered == 0 {
		report.Progress.Offered = report.Progress.Launched + report.Progress.Rejected
	}
	if report.Profile.LoadMode == "" {
		report.Profile.LoadMode = domain.LoadFixedConcurrency
	}
	report.SchemaVersion = quicktest.PerformanceSchemaVersion
	if generatedAt, err := time.Parse(time.RFC3339Nano, report.GeneratedAt); err == nil {
		report.GeneratedAt = generatedAt.UTC().Format(time.RFC3339Nano)
	}
	quicktest.RecomputePerformanceDerivedFields(&report)
	upgradeCapacityFineMetrics(&report)
	if _, err := quicktest.ValidateArchivedPerformanceReport(report); err != nil {
		return nil, err
	}
	return marshalCanonical(report)
}

func upgradeLegacyV1Performance(report *quicktest.PerformanceReport) {
	report.Profile.LoadMode = domain.LoadFixedConcurrency
	report.Profile.ArrivalPattern = load.ArrivalConstant
	report.Profile.WorkloadMode = quicktest.PerformanceWorkloadFixed
	report.Progress.Offered = report.Progress.Launched + report.Progress.Rejected
}

func upgradePerformanceSampleTelemetry(report *quicktest.PerformanceReport) {
	for index := range report.Samples {
		sample := &report.Samples[index]
		if sample.TTFTAnyMS <= 0 {
			sample.TTFTAnyMS = sample.TTFTMS
		}
		sample.TTFTMS = sample.TTFTAnyMS
		if sample.SemanticChunkCount == 0 && sample.TTFTAnyMS > 0 {
			sample.SemanticChunkCount = 1
		}
	}
}

func upgradeCapacityFineMetrics(report *quicktest.PerformanceReport) {
	if report.CapacityResult == nil {
		return
	}
	for index := range report.CapacityResult.Rungs {
		fillFineMetricsAlgebra(&report.CapacityResult.Rungs[index].Metrics)
	}
	if report.CapacityResult.SelectedRungIndex == nil {
		return
	}
	selected := int(*report.CapacityResult.SelectedRungIndex)
	if selected < 0 || selected >= len(report.CapacityResult.Rungs) {
		return
	}
	report.CapacityResult.Rungs[selected].Metrics = report.Metrics
	report.CapacityResult.Rungs[selected].Progress = report.Progress
	if report.SLOAssessment != nil {
		report.CapacityResult.Rungs[selected].SLOAssessment = *report.SLOAssessment
	}
}

func fillFineMetricsAlgebra(metrics *load.Metrics) {
	if metrics.Succeeded == 0 {
		return
	}
	if metrics.TTFTAnySamples == 0 && metrics.TTFTP50 > 0 {
		metrics.TTFTAnySamples = metrics.Succeeded
		metrics.TTFTAnyP50 = metrics.TTFTP50
		metrics.TTFTAnyP95 = metrics.TTFTP95
		metrics.TTFTAnyP99 = metrics.TTFTP99
		metrics.TTFTAnyAverage = metrics.TTFTAverage
	}
	metrics.TTFTSamples = metrics.TTFTAnySamples
	metrics.TTFTP50 = metrics.TTFTAnyP50
	metrics.TTFTP95 = metrics.TTFTAnyP95
	metrics.TTFTP99 = metrics.TTFTAnyP99
	metrics.TTFTAverage = metrics.TTFTAnyAverage
	if metrics.SemanticChunkCountSamples == 0 {
		metrics.SemanticChunkCountSamples = metrics.Succeeded
		metrics.SemanticChunkCountP50 = 1
		metrics.SemanticChunkCountP95 = 1
		metrics.SemanticChunkCountP99 = 1
		metrics.SemanticChunkCountAverage = 1
	}
}

func marshalCanonical(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func rewriteContractVersions(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	rewriteContractVersionValue(value)
	return json.Marshal(value)
}

func rewriteContractVersionValue(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "schema_version" || key == "file_schema_version" {
				switch number := child.(type) {
				case json.Number:
					parsed, err := number.Int64()
					if err == nil && (parsed == 2 || parsed == 3 || parsed == 4) {
						typed[key] = json.Number("1")
					}
				case float64:
					if number == 2 || number == 3 || number == 4 {
						typed[key] = float64(1)
					}
				}
				continue
			}
			rewriteContractVersionValue(child)
		}
	case []any:
		for _, child := range typed {
			rewriteContractVersionValue(child)
		}
	}
}
