package jobs

import (
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const MaxControlTargetBytes = 2048

// ControlAcceptance freezes known server admission facts. Null means that a
// local pending submission has not yet supplied an actual acceptance receipt.
type ControlAcceptance struct {
	Origin     domain.ExecutionOrigin `json:"origin"`
	AcceptedAt time.Time              `json:"accepted_at"`
}

type ControlTarget struct {
	JobID          string             `json:"job_id"`
	Location       Location           `json:"location"`
	NodeID         string             `json:"node_id"`
	InstallationID string             `json:"installation_id"`
	SpecSHA256     string             `json:"spec_sha256"`
	Acceptance     *ControlAcceptance `json:"acceptance"`
}

func (target ControlTarget) Validate() error {
	validIdentity := domain.IsUUID(target.JobID) && domain.IsUUID(target.NodeID) && domain.IsUUID(target.InstallationID)
	if !validIdentity || target.Location.Validate() != nil || !jobcontract.IsSHA256(target.SpecSHA256) {
		return ErrInvalid
	}
	if accepted := target.Acceptance; accepted != nil {
		origin := accepted.Origin
		matching := origin.Kind == string(target.Location.Kind) && origin.NodeID == target.NodeID &&
			origin.InstallationID == target.InstallationID
		if origin.Validate() != nil || !validTime(accepted.AcceptedAt) || !matching {
			return ErrInvalid
		}
	}
	return nil
}

func (target ControlTarget) SameBinding(other ControlTarget) bool {
	return target.JobID == other.JobID && target.Location == other.Location && target.NodeID == other.NodeID &&
		target.InstallationID == other.InstallationID && target.SpecSHA256 == other.SpecSHA256
}

func (target ControlTarget) Equal(other ControlTarget) bool {
	if !target.SameBinding(other) {
		return false
	}
	if target.Acceptance == nil || other.Acceptance == nil {
		return target.Acceptance == nil && other.Acceptance == nil
	}
	return target.Acceptance.Origin == other.Acceptance.Origin &&
		target.Acceptance.AcceptedAt.Equal(other.Acceptance.AcceptedAt)
}

func (read JobRead) ControlTarget() (ControlTarget, error) {
	if err := read.Validate(); err != nil {
		return ControlTarget{}, err
	}
	if read.Submission != nil && read.Submission.State == SubmissionPrepared {
		return ControlTarget{}, ErrInvalid
	}
	digest, err := read.Spec.Digest()
	if err != nil {
		return ControlTarget{}, ErrInvalid
	}
	target := ControlTarget{JobID: read.Spec.JobID, Location: read.Location, SpecSHA256: digest}
	if receipt := read.acceptanceReceipt(); receipt != nil {
		target.NodeID, target.InstallationID = receipt.Origin.NodeID, receipt.Origin.InstallationID
		target.Acceptance = &ControlAcceptance{Origin: receipt.Origin, AcceptedAt: receipt.AcceptedAt}
	} else {
		target.NodeID = read.Submission.Prepared.Runner.NodeID
		target.InstallationID = read.Submission.Prepared.Runner.NodeInstallationID
	}
	return target, target.Validate()
}

func (target ControlTarget) ValidateFor(read JobRead) error {
	if err := target.Validate(); err != nil {
		return err
	}
	current, err := read.ControlTarget()
	if err != nil {
		return err
	}
	if !target.SameBinding(current) {
		return ErrConflict
	}
	if target.Acceptance == nil && read.Submission == nil {
		return ErrConflict
	}
	if target.Acceptance != nil && !target.Equal(current) {
		return ErrConflict
	}
	return nil
}
