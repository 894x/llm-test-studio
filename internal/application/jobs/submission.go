// Package jobs coordinates complete jobs without owning provider execution.
package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

var (
	ErrInvalid  = errors.New("invalid_submission_record")
	ErrConflict = errors.New("submission_record_conflict")
	ErrNotFound = errors.New("submission_record_not_found")
)

const (
	MaxSubmissionPageSize  = 25
	MaxSubmissionPageBytes = 16 << 20
)

type SubmissionState string

const (
	SubmissionPrepared SubmissionState = "prepared"
	SubmissionPending  SubmissionState = "pending"
	SubmissionAccepted SubmissionState = "accepted"
)

type Observation string

const (
	ObservationNone            Observation = ""
	ObservationUnreachable     Observation = "unreachable"
	ObservationNotFound        Observation = "not_found"
	ObservationIdentityChanged Observation = "identity_changed"
	ObservationRejected        Observation = "rejected"
)

// PreparedJob contains no submission secrets. Runner is the actual probed
// executor, independent of the client release or build.
type PreparedJob struct {
	ConfigurationID string                 `json:"configuration_id"`
	Runner          jobcontract.RunnerInfo `json:"runner"`
	Spec            jobcontract.JobSpec    `json:"spec"`
}

func (prepared PreparedJob) Validate() error {
	if prepared.Spec.Validate() != nil || prepared.Runner.Validate() != nil {
		return ErrInvalid
	}
	spec, runner := prepared.Spec, prepared.Runner
	sameExecutor := spec.NodeInstallationID == runner.NodeInstallationID && spec.EngineBuildID == runner.EngineBuildID
	if !sameExecutor {
		return ErrInvalid
	}
	switch spec.Target.Kind {
	case jobcontract.TargetLocal:
		if prepared.ConfigurationID != "" {
			return ErrInvalid
		}
	case jobcontract.TargetRemote:
		if !domain.IsUUID(prepared.ConfigurationID) || spec.Target.NodeID != runner.NodeID {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// SubmissionRecord is a desktop delivery fact, separate from the executor's
// RunStatus. Pending never implies that the executor rejected or stopped work.
type SubmissionRecord struct {
	Prepared    PreparedJob          `json:"prepared"`
	SpecSHA256  string               `json:"spec_sha256"`
	Revision    uint64               `json:"revision"`
	State       SubmissionState      `json:"state"`
	Observation Observation          `json:"observation"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
	Receipt     *jobcontract.Receipt `json:"receipt"`
}

type SubmissionPage struct {
	Records    []SubmissionRecord `json:"records"`
	NextCursor string             `json:"next_cursor"`
	HasMore    bool               `json:"has_more"`
}

type SubmissionRepository interface {
	CreateSubmission(context.Context, SubmissionRecord) (SubmissionRecord, error)
	GetSubmission(context.Context, string) (SubmissionRecord, error)
	UpdateSubmission(context.Context, SubmissionRecord, uint64) error
	ListPendingSubmissions(context.Context, string, uint32) (SubmissionPage, error)
}

func NewSubmission(prepared PreparedJob, at time.Time) (SubmissionRecord, error) {
	if err := prepared.Validate(); err != nil {
		return SubmissionRecord{}, err
	}
	spec, err := prepared.Spec.Freeze()
	if err != nil {
		return SubmissionRecord{}, err
	}
	prepared.Spec, prepared.Runner = spec, prepared.Runner.Clone()
	digest, err := spec.Digest()
	if err != nil {
		return SubmissionRecord{}, err
	}
	record := SubmissionRecord{Prepared: prepared, SpecSHA256: digest, Revision: 1,
		State: SubmissionPrepared, Observation: ObservationNone, CreatedAt: at, UpdatedAt: at}
	return record, record.Validate()
}

func (record SubmissionRecord) Validate() error {
	validRevision := record.Revision > 0 && record.Revision <= math.MaxInt64
	if record.Prepared.Validate() != nil || !validRevision {
		return ErrInvalid
	}
	digest, err := record.Prepared.Spec.Digest()
	if err != nil || digest != record.SpecSHA256 {
		return ErrInvalid
	}
	validTimes := validTime(record.CreatedAt) && validTime(record.UpdatedAt) &&
		!record.UpdatedAt.Before(record.CreatedAt)
	if !validTimes {
		return ErrInvalid
	}
	switch record.State {
	case SubmissionPrepared:
		initial := record.Revision == 1 && record.CreatedAt.Equal(record.UpdatedAt) &&
			record.Receipt == nil && record.Observation == ObservationNone
		if !initial {
			return ErrInvalid
		}
	case SubmissionPending:
		validPending := record.Revision >= 2 && record.Receipt == nil && validObservation(record.Observation)
		if !validPending {
			return ErrInvalid
		}
	case SubmissionAccepted:
		validReceipt := record.Receipt != nil && receiptMatches(*record.Receipt, record.Prepared.Spec)
		validAccepted := record.Revision >= 3 && record.Observation == ObservationNone && validReceipt
		if !validAccepted {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// ValidateSuccessor freezes the destination and all prepared input. A rejected
// observation, including GET not_found, cannot make pending work resubmittable.
func (record SubmissionRecord) ValidateSuccessor(next SubmissionRecord) error {
	if record.Validate() != nil || next.Validate() != nil {
		return ErrInvalid
	}
	fixed := record.CreatedAt.Equal(next.CreatedAt) && record.SpecSHA256 == next.SpecSHA256 &&
		next.Revision == record.Revision+1 && !next.UpdatedAt.Before(record.UpdatedAt)
	if !fixed || !record.SamePreparation(next) {
		return ErrConflict
	}
	switch record.State {
	case SubmissionPrepared:
		if next.State == SubmissionPending && next.Observation == ObservationNone {
			return nil
		}
	case SubmissionPending:
		if next.State == SubmissionPending || next.State == SubmissionAccepted {
			return nil
		}
	}
	return ErrConflict
}

func (record SubmissionRecord) SamePreparation(other SubmissionRecord) bool {
	left, leftErr := json.Marshal(record.Prepared.Runner)
	right, rightErr := json.Marshal(other.Prepared.Runner)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right) &&
		record.SpecSHA256 == other.SpecSHA256 && record.Prepared.ConfigurationID == other.Prepared.ConfigurationID
}

func validTime(value time.Time) bool {
	_, offset := value.Zone()
	return !value.IsZero() && offset == 0
}

func validObservation(observation Observation) bool {
	switch observation {
	case ObservationNone, ObservationUnreachable, ObservationNotFound, ObservationIdentityChanged, ObservationRejected:
		return true
	}
	return false
}

func receiptMatches(receipt jobcontract.Receipt, spec jobcontract.JobSpec) bool {
	if receipt.Validate() != nil {
		return false
	}
	expected, err := spec.InitialSnapshot(receipt.AcceptedAt)
	if err != nil {
		return false
	}
	want := expected.Receipt
	identity := receipt.JobID == want.JobID && receipt.Kind == want.Kind && receipt.Contract == want.Contract
	executor := receipt.NodeInstallationID == want.NodeInstallationID && receipt.EngineBuildID == want.EngineBuildID
	children := slices.Equal(receipt.RunIDs, want.RunIDs) && slices.Equal(receipt.ReportIDs, want.ReportIDs) &&
		receipt.ComparisonID == want.ComparisonID
	return identity && executor && children
}
