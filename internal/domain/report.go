package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
)

const CurrentReportSchemaVersion = 2

type ReportSubject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type EnvironmentSnapshot struct {
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	Region        string `json:"region"`
	NetworkEgress string `json:"network_egress"`
	AppVersion    string `json:"app_version"`
	EngineVersion string `json:"engine_version"`
}

func (environment EnvironmentSnapshot) Validate() error {
	if strings.TrimSpace(environment.OS) == "" || strings.TrimSpace(environment.Arch) == "" ||
		strings.TrimSpace(environment.AppVersion) == "" || strings.TrimSpace(environment.EngineVersion) == "" {
		return errors.New("environment requires os, arch, app version, and engine version")
	}
	return nil
}

type ReportConclusion struct {
	Passed  bool     `json:"passed"`
	Verdict string   `json:"verdict"`
	Issues  []string `json:"issues"`
}

func (conclusion ReportConclusion) Validate() error {
	if strings.TrimSpace(conclusion.Verdict) == "" {
		return errors.New("report conclusion requires a verdict")
	}
	for _, issue := range conclusion.Issues {
		if strings.TrimSpace(issue) == "" {
			return errors.New("report conclusion issues must not be blank")
		}
	}
	return nil
}

type MetricValue struct {
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
	Samples int     `json:"samples"`
}

func (metric MetricValue) Validate(name string) error {
	if strings.TrimSpace(name) == "" || strings.TrimSpace(metric.Unit) == "" || metric.Samples < 0 ||
		math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) {
		return fmt.Errorf("invalid report metric %q", name)
	}
	return nil
}

type ReportAttachment struct {
	ArtifactID   string `json:"artifact_id"`
	RunID        string `json:"run_id"`
	Name         string `json:"name"`
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	MediaType    string `json:"media_type"`
	Redacted     bool   `json:"redacted"`
}

type SuiteReportStatus string

const (
	SuiteReportCompleted  SuiteReportStatus = "completed"
	SuiteReportFailed     SuiteReportStatus = "failed"
	SuiteReportCancelled  SuiteReportStatus = "cancelled"
	SuiteReportNotStarted SuiteReportStatus = "not_started"
)

func (status SuiteReportStatus) Validate() error {
	switch status {
	case SuiteReportCompleted, SuiteReportFailed, SuiteReportCancelled, SuiteReportNotStarted:
		return nil
	default:
		return fmt.Errorf("unsupported suite report status %q", status)
	}
}

type SuiteReport struct {
	SuiteEntryID  string                 `json:"suite_entry_id"`
	SuiteID       string                 `json:"suite_id"`
	SuiteRevision uint64                 `json:"suite_revision"`
	SuiteKey      string                 `json:"suite_key"`
	SuiteName     string                 `json:"suite_name"`
	Status        SuiteReportStatus      `json:"status"`
	Conclusion    ReportConclusion       `json:"conclusion"`
	SLA           map[string]MetricValue `json:"sla"`
	Metrics       map[string]MetricValue `json:"metrics"`
	Timeline      []json.RawMessage      `json:"timeline"`
	Distributions []json.RawMessage      `json:"distributions"`
	CaseResults   []Result               `json:"case_results"`
}

func (attachment ReportAttachment) Validate(expectedRunID ...string) error {
	if !IsUUID(attachment.ArtifactID) {
		return errors.New("attachment artifact id must be a canonical UUID")
	}
	if strings.TrimSpace(attachment.Name) == "" {
		return errors.New("attachment name must not be empty")
	}
	if err := validateArtifactMetadata(
		attachment.RunID,
		attachment.RelativePath,
		attachment.SHA256,
		attachment.MediaType,
		attachment.Redacted,
		expectedRunID...,
	); err != nil {
		return fmt.Errorf("invalid attachment: %w", err)
	}
	return nil
}

type Report struct {
	SchemaVersion int                    `json:"schema_version"`
	ID            string                 `json:"id"`
	RunID         string                 `json:"run_id"`
	RunStatus     RunStatus              `json:"run_status"`
	GeneratedAt   time.Time              `json:"generated_at"`
	PlanSnapshot  RunSnapshot            `json:"plan_snapshot"`
	Model         ReportSubject          `json:"model"`
	Channel       ReportSubject          `json:"channel"`
	Environment   EnvironmentSnapshot    `json:"environment"`
	Conclusion    ReportConclusion       `json:"conclusion"`
	SLA           map[string]MetricValue `json:"sla"`
	Metrics       map[string]MetricValue `json:"metrics"`
	Timeline      []json.RawMessage      `json:"timeline"`
	Distributions []json.RawMessage      `json:"distributions"`
	CaseResults   []Result               `json:"case_results"`
	SuiteReports  []SuiteReport          `json:"suite_reports"`
	ErrorClusters []json.RawMessage      `json:"error_clusters"`
	Evidence      []Evidence             `json:"evidence"`
	Baseline      json.RawMessage        `json:"baseline"`
	Attachments   []ReportAttachment     `json:"attachments"`
}

func (report Report) Validate() error {
	if report.SchemaVersion != CurrentReportSchemaVersion {
		return fmt.Errorf("unsupported report schema version %d", report.SchemaVersion)
	}
	if !IsUUID(report.ID) || !IsUUID(report.RunID) {
		return errors.New("report and run ids must be canonical UUIDs")
	}
	switch report.RunStatus {
	case RunCompleted:
	case RunFailed, RunCancelled:
		if report.Conclusion.Passed {
			return fmt.Errorf("%s run cannot have a passing report conclusion", report.RunStatus)
		}
	default:
		return fmt.Errorf("report requires a terminal run status, got %q", report.RunStatus)
	}
	if report.GeneratedAt.IsZero() || !timestampIsUTC(report.GeneratedAt) {
		return errors.New("report generation timestamp must be non-zero UTC")
	}
	if err := report.PlanSnapshot.Validate(); err != nil {
		return fmt.Errorf("invalid report plan snapshot: %w", err)
	}
	if err := validateReportSubject("model", report.Model); err != nil {
		return err
	}
	if err := validateReportSubject("channel", report.Channel); err != nil {
		return err
	}
	if report.Model.ID != report.PlanSnapshot.Model.ID || report.Model.Name != report.PlanSnapshot.Model.Name ||
		report.Channel.ID != report.PlanSnapshot.Channel.ID || report.Channel.Name != report.PlanSnapshot.Channel.Name {
		return errors.New("report subjects do not match the plan snapshot")
	}
	if err := report.Environment.Validate(); err != nil {
		return fmt.Errorf("invalid report environment: %w", err)
	}
	if report.Environment != report.PlanSnapshot.Environment {
		return errors.New("report environment does not match the plan snapshot")
	}
	if err := report.Conclusion.Validate(); err != nil {
		return err
	}
	if err := validateMetricMap("report SLA", report.SLA); err != nil {
		return err
	}
	if err := validateMetricMap("report metrics", report.Metrics); err != nil {
		return err
	}
	if report.Timeline == nil || report.Distributions == nil || report.CaseResults == nil ||
		report.ErrorClusters == nil || report.Evidence == nil || report.Attachments == nil {
		return errors.New("report required collection sections must not be nil")
	}
	if err := validateJSONObject(report.Baseline); err != nil {
		return fmt.Errorf("invalid report baseline: %w", err)
	}
	for index, item := range report.Timeline {
		if err := validateJSONObject(item); err != nil {
			return fmt.Errorf("invalid report timeline item %d: %w", index, err)
		}
	}
	for index, item := range report.Distributions {
		if err := validateJSONObject(item); err != nil {
			return fmt.Errorf("invalid report distribution %d: %w", index, err)
		}
	}
	for index, item := range report.ErrorClusters {
		if err := validateJSONObject(item); err != nil {
			return fmt.Errorf("invalid report error cluster %d: %w", index, err)
		}
	}

	evidenceIDs := make(map[string]struct{}, len(report.Evidence))
	for index, evidence := range report.Evidence {
		if err := evidence.Validate(report.RunID); err != nil {
			return fmt.Errorf("invalid report evidence %d: %w", index, err)
		}
		if _, duplicate := evidenceIDs[evidence.ID]; duplicate {
			return fmt.Errorf("duplicate report evidence id %q", evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}
	}
	plannedResults := reportPlannedResults(report.PlanSnapshot, report.RunID)
	resultIDs := make(map[string]struct{}, len(report.CaseResults))
	completedResults := make(map[string]struct{}, len(plannedResults))
	for index, result := range report.CaseResults {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("invalid report case result %d: %w", index, err)
		}
		if result.RunID != report.RunID {
			return fmt.Errorf("report case result %d belongs to another run", index)
		}
		if result.SuiteStatus != "" || result.CaseID == "" ||
			(len(report.PlanSnapshot.Suites) != 0 && result.RequestID != "") {
			return fmt.Errorf("report case result %d is not a case summary", index)
		}
		if _, duplicate := resultIDs[result.ID]; duplicate {
			return fmt.Errorf("duplicate report result id %q", result.ID)
		}
		resultIDs[result.ID] = struct{}{}
		resultKey := reportResultKey(result.SuiteEntryID, result.CaseID)
		if _, planned := plannedResults[resultKey]; !planned {
			return fmt.Errorf("report case result %d references a suite or case outside the plan snapshot", index)
		}
		completedResults[resultKey] = struct{}{}
		for _, evidenceID := range result.EvidenceIDs {
			if _, exists := evidenceIDs[evidenceID]; !exists {
				return fmt.Errorf("report case result %d references missing evidence %q", index, evidenceID)
			}
		}
		if report.Conclusion.Passed && !result.Success.Overall() {
			return errors.New("passing report contains a failed case result")
		}
	}
	if report.RunStatus == RunCompleted {
		for plannedResult := range plannedResults {
			if _, completed := completedResults[plannedResult]; !completed {
				return fmt.Errorf("completed report has no result for planned suite case %q", plannedResult)
			}
		}
	}
	if err := report.validateSuiteReports(resultIDs); err != nil {
		return err
	}
	attachmentIDs := make(map[string]struct{}, len(report.Attachments))
	for index, attachment := range report.Attachments {
		if err := attachment.Validate(report.RunID); err != nil {
			return fmt.Errorf("invalid report attachment %d: %w", index, err)
		}
		if _, duplicate := attachmentIDs[attachment.ArtifactID]; duplicate {
			return fmt.Errorf("duplicate report attachment id %q", attachment.ArtifactID)
		}
		attachmentIDs[attachment.ArtifactID] = struct{}{}
	}
	return nil
}

func (report Report) validateSuiteReports(topLevelResultIDs map[string]struct{}) error {
	snapshots := report.PlanSnapshot.Suites
	if len(report.PlanSnapshot.Suites) == 0 {
		if report.PlanSnapshot.QuickTask == nil || len(report.SuiteReports) != 1 {
			return errors.New("quick-task report requires one synthetic suite report")
		}
		snapshots = []RunSuiteSnapshot{{
			EntryID: report.RunID,
			Suite:   report.PlanSnapshot.QuickTask.Suite,
			Cases:   report.PlanSnapshot.Cases,
		}}
	}
	if len(report.SuiteReports) != len(snapshots) {
		return errors.New("multi-suite report requires one ordered suite report per snapshot entry")
	}
	nested := make(map[string]Result, len(report.CaseResults))
	executionStopped := false
	for index, suiteReport := range report.SuiteReports {
		snapshot := snapshots[index]
		if suiteReport.SuiteEntryID != snapshot.EntryID || suiteReport.SuiteID != snapshot.Suite.ID ||
			suiteReport.SuiteRevision != snapshot.Suite.Revision || suiteReport.SuiteKey != snapshot.Suite.Key ||
			suiteReport.SuiteName != snapshot.Suite.Name {
			return fmt.Errorf("suite report %d does not match the ordered snapshot entry", index)
		}
		if err := validateSuiteReport(suiteReport, snapshot, report.RunID, report.RunStatus); err != nil {
			return fmt.Errorf("invalid suite report %d: %w", index, err)
		}
		if executionStopped && suiteReport.Status != SuiteReportNotStarted {
			return fmt.Errorf("suite report %d executes after cancellation stopped the run", index)
		}
		if suiteReport.Status == SuiteReportCancelled || suiteReport.Status == SuiteReportNotStarted {
			executionStopped = true
		}
		if report.Conclusion.Passed && !suiteReport.Conclusion.Passed {
			return errors.New("passing report contains a non-passing suite report")
		}
		for _, result := range suiteReport.CaseResults {
			if _, duplicate := nested[result.ID]; duplicate {
				return fmt.Errorf("duplicate nested suite result id %q", result.ID)
			}
			nested[result.ID] = result
		}
	}
	if len(nested) != len(report.CaseResults) {
		return errors.New("suite report case results do not match the top-level case results")
	}
	for _, result := range report.CaseResults {
		if _, exists := topLevelResultIDs[result.ID]; !exists || !reflect.DeepEqual(nested[result.ID], result) {
			return errors.New("suite report case result does not match its top-level result")
		}
	}
	return nil
}

func validateSuiteReport(report SuiteReport, snapshot RunSuiteSnapshot, runID string, runStatus RunStatus) error {
	if !IsUUID(report.SuiteEntryID) || !IsUUID(report.SuiteID) || report.SuiteRevision < 1 ||
		strings.TrimSpace(report.SuiteKey) == "" || strings.TrimSpace(report.SuiteName) == "" {
		return errors.New("suite report requires its pinned suite identity")
	}
	if err := report.Status.Validate(); err != nil {
		return err
	}
	if err := report.Conclusion.Validate(); err != nil {
		return err
	}
	wantVerdict := ""
	switch report.Status {
	case SuiteReportCompleted:
		wantVerdict = "fail"
		if report.Conclusion.Passed {
			wantVerdict = "pass"
		}
	case SuiteReportFailed:
		wantVerdict = "fail"
	case SuiteReportCancelled:
		wantVerdict = "cancelled"
	case SuiteReportNotStarted:
		wantVerdict = "not_started"
	}
	if report.Conclusion.Verdict != wantVerdict || (report.Status != SuiteReportCompleted && report.Conclusion.Passed) {
		return errors.New("suite report conclusion does not match its execution status")
	}
	if runStatus == RunCompleted && report.Status != SuiteReportCompleted {
		return errors.New("completed run contains an incomplete suite report")
	}
	if err := validateMetricMap("suite report SLA", report.SLA); err != nil {
		return err
	}
	if err := validateMetricMap("suite report metrics", report.Metrics); err != nil {
		return err
	}
	if report.Timeline == nil || report.Distributions == nil || report.CaseResults == nil {
		return errors.New("suite report collection sections must not be nil")
	}
	if report.Status == SuiteReportNotStarted && len(report.CaseResults) != 0 {
		return errors.New("not-started suite report cannot contain case results")
	}
	for index, item := range report.Timeline {
		if err := validateJSONObject(item); err != nil {
			return fmt.Errorf("invalid suite report timeline item %d: %w", index, err)
		}
	}
	for index, item := range report.Distributions {
		if err := validateJSONObject(item); err != nil {
			return fmt.Errorf("invalid suite report distribution %d: %w", index, err)
		}
	}
	planned := make(map[string]struct{}, len(snapshot.Cases))
	for _, ref := range snapshot.Cases {
		planned[ref.CaseID] = struct{}{}
	}
	seenCases := make(map[string]struct{}, len(report.CaseResults))
	for index, result := range report.CaseResults {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("invalid case result %d: %w", index, err)
		}
		if result.RunID != runID || result.SuiteEntryID != report.SuiteEntryID || result.RequestID != "" || result.SuiteStatus != "" {
			return fmt.Errorf("case result %d has invalid suite ownership", index)
		}
		if _, exists := planned[result.CaseID]; !exists {
			return fmt.Errorf("case result %d is outside its suite snapshot", index)
		}
		if _, duplicate := seenCases[result.CaseID]; duplicate {
			return fmt.Errorf("duplicate suite case result %q", result.CaseID)
		}
		seenCases[result.CaseID] = struct{}{}
		if report.Conclusion.Passed && !result.Success.Overall() {
			return errors.New("passing suite report contains a failed case result")
		}
	}
	if report.Status == SuiteReportCompleted {
		for _, ref := range snapshot.Cases {
			if _, exists := seenCases[ref.CaseID]; !exists {
				return fmt.Errorf("completed suite report has no result for case %q", ref.CaseID)
			}
		}
	}
	return nil
}

func reportPlannedResults(snapshot RunSnapshot, quickTaskEntryID string) map[string]struct{} {
	if len(snapshot.Suites) == 0 {
		planned := make(map[string]struct{}, len(snapshot.Cases))
		for _, ref := range snapshot.Cases {
			planned[reportResultKey(quickTaskEntryID, ref.CaseID)] = struct{}{}
		}
		return planned
	}
	planned := make(map[string]struct{})
	for _, suite := range snapshot.Suites {
		for _, ref := range suite.Cases {
			planned[reportResultKey(suite.EntryID, ref.CaseID)] = struct{}{}
		}
	}
	return planned
}

func reportResultKey(suiteEntryID, caseID string) string {
	return suiteEntryID + "\x00" + caseID
}

func validateReportSubject(kind string, subject ReportSubject) error {
	if !IsUUID(subject.ID) || strings.TrimSpace(subject.Name) == "" {
		return fmt.Errorf("report %s requires a UUID and name", kind)
	}
	return nil
}

func validateMetricMap(kind string, metrics map[string]MetricValue) error {
	if metrics == nil {
		return fmt.Errorf("%s must not be nil", kind)
	}
	for name, metric := range metrics {
		if err := metric.Validate(name); err != nil {
			return err
		}
	}
	return nil
}

func validateJSONObject(raw json.RawMessage) error {
	if len(raw) == 0 || !json.Valid(raw) {
		return errors.New("value must be valid JSON")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return errors.New("value must be a JSON object")
	}
	return nil
}
