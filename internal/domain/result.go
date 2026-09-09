package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/testspec"
)

const (
	MaxErrorCodeLength      = 64
	MaxProviderDetailLength = 2048
)

type ErrorCode string

func (code ErrorCode) Validate() error {
	value := string(code)
	if len(value) == 0 || len(value) > MaxErrorCodeLength {
		return fmt.Errorf("error code length must be between 1 and %d bytes", MaxErrorCodeLength)
	}
	if value[0] < 'a' || value[0] > 'z' {
		return errors.New("error code must begin with a lowercase ASCII letter")
	}
	previousUnderscore := false
	for _, character := range value {
		if character == '_' {
			if previousUnderscore {
				return errors.New("error code must not contain repeated underscores")
			}
			previousUnderscore = true
			continue
		}
		if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')) {
			return errors.New("error code must be a lowercase snake_case identifier")
		}
		previousUnderscore = false
	}
	if previousUnderscore {
		return errors.New("error code must not end with an underscore")
	}
	return nil
}

type ProviderDetail struct {
	Value    string `json:"value"`
	Redacted bool   `json:"redacted"`
}

func (detail ProviderDetail) Validate() error {
	if !detail.Redacted {
		return errors.New("provider detail must be marked redacted")
	}
	if !utf8.ValidString(detail.Value) || strings.TrimSpace(detail.Value) == "" || len(detail.Value) > MaxProviderDetailLength {
		return fmt.Errorf("provider detail must contain between 1 and %d valid UTF-8 bytes", MaxProviderDetailLength)
	}
	return nil
}

type FailureKind string

const (
	FailureNetwork   FailureKind = "network"
	FailureHTTP      FailureKind = "http"
	FailureProtocol  FailureKind = "protocol"
	FailureSemantic  FailureKind = "semantic"
	FailureRateLimit FailureKind = "rate_limit"
	FailureTimeout   FailureKind = "timeout"
	FailureCancelled FailureKind = "cancelled"
	FailureSLA       FailureKind = "sla"
)

func (kind FailureKind) Validate() error {
	switch kind {
	case FailureNetwork, FailureHTTP, FailureProtocol, FailureSemantic,
		FailureRateLimit, FailureTimeout, FailureCancelled, FailureSLA:
		return nil
	default:
		return fmt.Errorf("unsupported failure kind %q", kind)
	}
}

type Result struct {
	EntityMeta
	RunID           string                `json:"run_id"`
	EntryID         string                `json:"entry_id,omitempty"`
	EntryStatus     EntryExecutionStatus  `json:"entry_status,omitempty"`
	CaseID          string                `json:"case_id,omitempty"`
	RequestID       string                `json:"request_id,omitempty"`
	ExecutionStatus ExecutionStatus       `json:"execution_status"`
	Verification    testspec.Verdict      `json:"verification"`
	Observation     *testspec.Observation `json:"observation,omitempty"`
	Failure         FailureKind           `json:"failure_kind,omitempty"`
	ErrorCode       ErrorCode             `json:"error_code,omitempty"`
	Detail          *ProviderDetail       `json:"detail,omitempty"`
	Dimensions      map[string]string     `json:"dimensions,omitempty"`
	Metrics         map[string]float64    `json:"metrics,omitempty"`
	EvidenceIDs     []string              `json:"evidence_ids,omitempty"`
}

type EntryExecutionStatus string

const (
	EntryExecutionCompleted EntryExecutionStatus = "completed"
	EntryExecutionFailed    EntryExecutionStatus = "failed"
)

func (status EntryExecutionStatus) Validate() error {
	switch status {
	case EntryExecutionCompleted, EntryExecutionFailed:
		return nil
	default:
		return fmt.Errorf("unsupported suite execution status %q", status)
	}
}

func (result Result) Validate() error {
	if err := result.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid result metadata: %w", err)
	}
	if !IsUUID(result.RunID) {
		return errors.New("result run id must be a canonical UUID")
	}
	if result.EntryID != "" && !IsUUID(result.EntryID) {
		return errors.New("result suite entry id must be a canonical UUID")
	}
	if result.EntryStatus != "" {
		if result.EntryID == "" {
			return errors.New("suite marker requires a suite entry id")
		}
		if err := result.EntryStatus.Validate(); err != nil {
			return err
		}
		if result.CaseID != "" || strings.TrimSpace(result.RequestID) != "" {
			return errors.New("suite marker must not identify a case or request")
		}
		if result.Failure != "" || result.ErrorCode != "" || result.Detail != nil || len(result.Dimensions) != 0 || len(result.Metrics) != 0 || len(result.EvidenceIDs) != 0 {
			return errors.New("suite marker must not contain request outcome data")
		}
		return nil
	}
	if result.CaseID == "" && strings.TrimSpace(result.RequestID) == "" {
		return errors.New("result requires a case id or request id")
	}
	if result.CaseID != "" && !IsUUID(result.CaseID) {
		return errors.New("result case id must be a canonical UUID")
	}
	if err := result.ExecutionStatus.Validate(); err != nil {
		return err
	}
	switch result.Verification.Status {
	case testspec.VerdictPassed, testspec.VerdictFailed, testspec.VerdictNotApplicable, testspec.VerdictIndeterminate:
	default:
		return errors.New("result requires an explicit verification status")
	}
	if result.Verification.Assertions == nil {
		return errors.New("result assertions must be present")
	}
	if result.Verification.Status == testspec.VerdictNotApplicable && len(result.Verification.Assertions) != 0 {
		return errors.New("observation-only result must not claim evaluated assertions")
	}
	if result.Failure != "" {
		if err := result.Failure.Validate(); err != nil {
			return err
		}
	}
	if result.ErrorCode != "" {
		if err := result.ErrorCode.Validate(); err != nil {
			return err
		}
	}
	if result.Detail != nil {
		if err := result.Detail.Validate(); err != nil {
			return err
		}
	}
	for name, value := range result.Dimensions {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name || strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || len(name) > 64 || len(value) > 256 {
			return fmt.Errorf("invalid result dimension %q", name)
		}
	}
	for name, value := range result.Metrics {
		if strings.TrimSpace(name) == "" || math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("invalid result metric %q", name)
		}
	}
	seenEvidence := make(map[string]struct{}, len(result.EvidenceIDs))
	for _, evidenceID := range result.EvidenceIDs {
		if !IsUUID(evidenceID) {
			return errors.New("result evidence id must be a canonical UUID")
		}
		if _, exists := seenEvidence[evidenceID]; exists {
			return fmt.Errorf("duplicate result evidence id %q", evidenceID)
		}
		seenEvidence[evidenceID] = struct{}{}
	}
	return nil
}

// ExecutionStatus records transport/workflow completion independently of verification.
type ExecutionStatus string

const (
	ExecutionCompleted ExecutionStatus = "completed"
	ExecutionFailed    ExecutionStatus = "failed"
	ExecutionCancelled ExecutionStatus = "cancelled"
)

func (status ExecutionStatus) Validate() error {
	switch status {
	case ExecutionCompleted, ExecutionFailed, ExecutionCancelled:
		return nil
	}
	return fmt.Errorf("unsupported execution status %q", status)
}

func (result Result) Passed() bool { return result.Verification.Status == testspec.VerdictPassed }

type VerificationSummary struct {
	Status        testspec.VerdictStatus `json:"status"`
	Passed        uint64                 `json:"passed"`
	Failed        uint64                 `json:"failed"`
	Observed      uint64                 `json:"observed"`
	Indeterminate uint64                 `json:"indeterminate"`
}

func SummarizeVerification(results []Result) VerificationSummary {
	summary := VerificationSummary{Status: testspec.VerdictNotApplicable}
	for _, result := range results {
		if result.EntryStatus != "" || result.Dimensions["phase"] == "warmup" {
			continue
		}
		switch result.Verification.Status {
		case testspec.VerdictPassed:
			summary.Passed++
		case testspec.VerdictFailed:
			summary.Failed++
		case testspec.VerdictNotApplicable:
			summary.Observed++
		case testspec.VerdictIndeterminate:
			summary.Indeterminate++
		}
	}
	switch {
	case summary.Failed > 0:
		summary.Status = testspec.VerdictFailed
	case summary.Indeterminate > 0:
		summary.Status = testspec.VerdictIndeterminate
	case summary.Passed > 0:
		summary.Status = testspec.VerdictPassed
	}
	return summary
}
