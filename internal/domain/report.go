package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const CurrentReportSchemaVersion = 1

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
	plannedCaseIDs := make(map[string]struct{}, len(report.PlanSnapshot.Cases))
	for _, plannedCase := range report.PlanSnapshot.Cases {
		plannedCaseIDs[plannedCase.CaseID] = struct{}{}
	}
	resultIDs := make(map[string]struct{}, len(report.CaseResults))
	completedCaseIDs := make(map[string]struct{}, len(report.PlanSnapshot.Cases))
	for index, result := range report.CaseResults {
		if err := result.Validate(); err != nil {
			return fmt.Errorf("invalid report case result %d: %w", index, err)
		}
		if result.RunID != report.RunID {
			return fmt.Errorf("report case result %d belongs to another run", index)
		}
		if _, duplicate := resultIDs[result.ID]; duplicate {
			return fmt.Errorf("duplicate report result id %q", result.ID)
		}
		resultIDs[result.ID] = struct{}{}
		if _, planned := plannedCaseIDs[result.CaseID]; !planned {
			return fmt.Errorf("report case result %d references case %q outside the plan snapshot", index, result.CaseID)
		}
		completedCaseIDs[result.CaseID] = struct{}{}
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
		for plannedCaseID := range plannedCaseIDs {
			if _, completed := completedCaseIDs[plannedCaseID]; !completed {
				return fmt.Errorf("completed report has no result for planned case %q", plannedCaseID)
			}
		}
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
