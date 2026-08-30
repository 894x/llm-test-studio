package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
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

type SuccessDimensions struct {
	Transport bool `json:"transport"`
	Protocol  bool `json:"protocol"`
	Semantic  bool `json:"semantic"`
	SLA       bool `json:"sla"`
}

func (dimensions SuccessDimensions) Overall() bool {
	return dimensions.Transport && dimensions.Protocol && dimensions.Semantic && dimensions.SLA
}

func (dimensions SuccessDimensions) Validate() error {
	if dimensions.Protocol && !dimensions.Transport {
		return errors.New("protocol success requires transport success")
	}
	if dimensions.Semantic && !dimensions.Protocol {
		return errors.New("semantic success requires protocol success")
	}
	if dimensions.SLA && !dimensions.Semantic {
		return errors.New("SLA success requires semantic success")
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

func (kind FailureKind) validateDimensions(dimensions SuccessDimensions) error {
	consistent := false
	switch kind {
	case FailureNetwork, FailureTimeout, FailureCancelled:
		consistent = !dimensions.Transport
	case FailureHTTP, FailureProtocol, FailureRateLimit:
		consistent = dimensions.Transport && !dimensions.Protocol
	case FailureSemantic:
		consistent = dimensions.Transport && dimensions.Protocol && !dimensions.Semantic
	case FailureSLA:
		consistent = dimensions.Transport && dimensions.Protocol && dimensions.Semantic && !dimensions.SLA
	}
	if !consistent {
		return fmt.Errorf("failure kind %q does not match the failed success dimension", kind)
	}
	return nil
}

type Result struct {
	EntityMeta
	RunID       string             `json:"run_id"`
	CaseID      string             `json:"case_id,omitempty"`
	RequestID   string             `json:"request_id,omitempty"`
	Success     SuccessDimensions  `json:"success"`
	Failure     FailureKind        `json:"failure_kind,omitempty"`
	ErrorCode   ErrorCode          `json:"error_code,omitempty"`
	Detail      *ProviderDetail    `json:"detail,omitempty"`
	Metrics     map[string]float64 `json:"metrics,omitempty"`
	EvidenceIDs []string           `json:"evidence_ids,omitempty"`
}

func (result Result) Validate() error {
	if err := result.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid result metadata: %w", err)
	}
	if !IsUUID(result.RunID) {
		return errors.New("result run id must be a canonical UUID")
	}
	if result.CaseID == "" && strings.TrimSpace(result.RequestID) == "" {
		return errors.New("result requires a case id or request id")
	}
	if result.CaseID != "" && !IsUUID(result.CaseID) {
		return errors.New("result case id must be a canonical UUID")
	}
	if err := result.Success.Validate(); err != nil {
		return err
	}
	if result.Success.Overall() {
		if result.Failure != "" || result.ErrorCode != "" || result.Detail != nil {
			return errors.New("successful result must not contain failure details")
		}
	} else {
		if err := result.Failure.Validate(); err != nil {
			return err
		}
		if err := result.Failure.validateDimensions(result.Success); err != nil {
			return err
		}
		if err := result.ErrorCode.Validate(); err != nil {
			return fmt.Errorf("failed result requires a stable error code: %w", err)
		}
		if result.Detail != nil {
			if err := result.Detail.Validate(); err != nil {
				return err
			}
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
