package quicktest

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"

	"github.com/894x/llm-test-studio/internal/domain"
)

// DecodePlanResult validates the same checkpoint contract used by standalone
// performance jobs, including partial results retained after interruption.
func DecodePlanResult(result domain.Result) (PerformanceReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(result.Performance))
	decoder.DisallowUnknownFields()
	var report PerformanceReport
	if err := decoder.Decode(&report); err != nil {
		return report, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return report, ErrPerformanceCheckpoint
	}
	checkpoint := PerformanceCheckpoint{Report: report, Stage: PerformanceStage(result.Dimensions["performance_stage"]), Boundary: PerformanceBoundary(result.Dimensions["performance_boundary"])}
	if raw, ok := result.Dimensions["performance_rung"]; ok {
		index, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return report, err
		}
		value := uint32(index)
		checkpoint.RungIndex = &value
	}
	if report.ReportID != result.EntryID {
		return report, ErrPerformanceCheckpoint
	}
	return report, checkpoint.Validate()
}

func PlanResultMatches(report PerformanceReport, entry domain.RunEntrySnapshot, channel domain.ChannelSnapshot) bool {
	if entry.Performance == nil {
		return false
	}
	expected, err := PreparePlanEntry(entry.EntryID, channel, *entry.Performance)
	return err == nil && reflect.DeepEqual(expected, preparedPerformance(report, report.ReportID))
}

// ValidatePlanReport checks nested measurements at storage and wire boundaries.
func ValidatePlanReport(report domain.Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	for index, entry := range report.EntryReports {
		if len(entry.Performance) == 0 {
			continue
		}
		result := domain.Result{EntryID: entry.EntryID, Performance: entry.Performance, Dimensions: entry.PerformanceContext}
		performance, err := DecodePlanResult(result)
		if err != nil || !PlanResultMatches(performance, report.PlanSnapshot.Entries[index], report.PlanSnapshot.Channel) {
			return ErrPerformanceCheckpoint
		}
		if entry.Status == domain.EntryReportCompleted && entry.PerformanceContext["phase"] != "measured" {
			return ErrPerformanceCheckpoint
		}
	}
	return nil
}
