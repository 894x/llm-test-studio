package jobs

import (
	"context"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const MaxControlRecordBytes = jobcontract.MaxStateBytes + jobcontract.MaxCommandBytes + 2048

type ControlState string

const (
	ControlPending   ControlState = "pending"
	ControlConfirmed ControlState = "confirmed"
	ControlRejected  ControlState = "rejected"
)

// ControlRecord is a durable instruction, never a replacement execution state.
// Its destination comes only from the immutable submission identified by JobID.
type ControlRecord struct {
	SchemaVersion int                              `json:"schema_version"`
	Command       jobcontract.Command              `json:"command"`
	Revision      uint64                           `json:"revision"`
	State         ControlState                     `json:"state"`
	Observation   Observation                      `json:"observation"`
	CreatedAt     time.Time                        `json:"created_at"`
	UpdatedAt     time.Time                        `json:"updated_at"`
	Confirmation  *jobcontract.CommandConfirmation `json:"confirmation"`
}

type ControlPage struct {
	Records    []ControlRecord
	NextCursor string
	HasMore    bool
}

type ControlRepository interface {
	CreateControl(context.Context, ControlRecord) (ControlRecord, error)
	GetControl(context.Context, string) (ControlRecord, error)
	UpdateControl(context.Context, ControlRecord, uint64) error
	ListPendingControls(context.Context, string, uint32) (ControlPage, error)
}

func (record ControlRecord) Validate() error {
	validRevision := record.Revision > 0 && record.Revision <= math.MaxInt64
	validTimes := validTime(record.CreatedAt) && validTime(record.UpdatedAt) &&
		!record.UpdatedAt.Before(record.CreatedAt)
	validEnvelope := record.SchemaVersion == 1 && validRevision && validTimes && record.Command.Validate() == nil
	if !validEnvelope {
		return ErrInvalid
	}
	initial := record.State == ControlPending && record.Observation == ObservationNone &&
		record.CreatedAt.Equal(record.UpdatedAt)
	if record.Revision == 1 && !initial {
		return ErrInvalid
	}
	switch record.State {
	case ControlPending:
		if record.Confirmation == nil && validObservation(record.Observation) {
			return nil
		}
	case ControlRejected:
		if record.Revision > 1 && record.Confirmation == nil && record.Observation == ObservationRejected {
			return nil
		}
	case ControlConfirmed:
		validConfirmation := record.Revision > 1 && record.Confirmation != nil && record.Observation == ObservationNone
		if !validConfirmation {
			return ErrInvalid
		}
		if record.Confirmation.Validate() == nil && record.Confirmation.Command == record.Command {
			return nil
		}
	}
	return ErrInvalid
}

func (record ControlRecord) ValidateFor(submission SubmissionRecord) error {
	validOwner := submission.Prepared.Spec.JobID == record.Command.JobID && submission.State != SubmissionPrepared
	validDocuments := record.Validate() == nil && submission.Validate() == nil
	if !validDocuments || !validOwner {
		return ErrInvalid
	}
	if record.Confirmation == nil {
		return nil
	}
	snapshot := record.Confirmation.Snapshot
	if snapshot.Validate(submission.Prepared.Spec) != nil {
		return ErrConflict
	}
	if receipt := submission.Receipt; receipt != nil {
		sameAcceptance := receipt.AcceptedAt.Equal(snapshot.Receipt.AcceptedAt) && receipt.Origin == snapshot.Receipt.Origin
		if !sameAcceptance {
			return ErrConflict
		}
	}
	return nil
}

func (record ControlRecord) ValidateSuccessor(next ControlRecord) error {
	if record.Validate() != nil || next.Validate() != nil {
		return ErrInvalid
	}
	fixed := record.Command == next.Command && record.CreatedAt.Equal(next.CreatedAt) &&
		next.Revision == record.Revision+1 && !next.UpdatedAt.Before(record.UpdatedAt)
	if !fixed || record.State != ControlPending {
		return ErrConflict
	}
	return nil
}

type ControlInput struct {
	JobID            string                  `json:"job_id"`
	Kind             jobcontract.CommandKind `json:"kind"`
	ExpectedRevision uint64                  `json:"expected_revision"`
}

type ControllerDependencies struct {
	Repository  ControlRepository
	Submissions SubmissionRepository
	Open        func(context.Context, Location, Access) (Backend, error)
	Now         func() time.Time
	MetaFactory func(time.Time) (domain.EntityMeta, error)
}

type Controller struct {
	repository  ControlRepository
	submissions SubmissionRepository
	open        func(context.Context, Location, Access) (Backend, error)
	now         func() time.Time
	metaFactory func(time.Time) (domain.EntityMeta, error)
}

func NewController(dependencies ControllerDependencies) (*Controller, error) {
	validPorts := !nilPort(dependencies.Repository) && !nilPort(dependencies.Submissions) && dependencies.Open != nil
	if !validPorts || dependencies.Now == nil {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	return &Controller{repository: dependencies.Repository, submissions: dependencies.Submissions,
		open: dependencies.Open, now: dependencies.Now, metaFactory: factory}, nil
}

// Request saves the exact command before connecting, so offline cancellation
// survives process exit. A failed commit must never send a command.
func (controller *Controller) Request(ctx context.Context, input ControlInput) (ControlRecord, error) {
	if controller == nil || ctx == nil {
		return ControlRecord{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := controller.metaFactory(controller.now().UTC())
	if err != nil {
		return ControlRecord{}, err
	}
	command := jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: meta.ID,
		JobID: input.JobID, Kind: input.Kind, ExpectedRevision: input.ExpectedRevision}
	validMeta := meta.Validate() == nil && meta.Revision == 1
	if !validMeta || command.Validate() != nil {
		return ControlRecord{}, ErrInvalid
	}
	submission, err := controller.submissions.GetSubmission(ctx, input.JobID)
	if err != nil {
		return ControlRecord{}, err
	}
	record := ControlRecord{SchemaVersion: 1, Command: command, Revision: 1, State: ControlPending,
		Observation: ObservationNone, CreatedAt: meta.CreatedAt, UpdatedAt: meta.CreatedAt}
	if err := record.ValidateFor(submission); err != nil {
		return ControlRecord{}, err
	}
	stored, err := controller.repository.CreateControl(ctx, record)
	if err != nil {
		// An ambiguous commit can leave a durable pending instruction. Its worker
		// will deliver the stored ID; this call cannot assume it committed.
		return record, err
	}
	if stored.Command != record.Command || stored.ValidateFor(submission) != nil {
		return record, ErrConflict
	}
	return controller.Deliver(ctx, stored.Command.CommandID)
}

// Deliver may resend one persisted command. The runner's installation-wide
// command identity makes replay safe even after a terminal decision or lost ACK.
func (controller *Controller) Deliver(ctx context.Context, id string) (ControlRecord, error) {
	validContext := controller != nil && ctx != nil
	if !validContext || !domain.IsUUID(id) {
		return ControlRecord{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record, err := controller.repository.GetControl(ctx, id)
	if err != nil || record.State != ControlPending {
		return record, err
	}
	submission, err := controller.submissions.GetSubmission(ctx, record.Command.JobID)
	if err != nil {
		return record, err
	}
	if err := record.ValidateFor(submission); err != nil {
		return record, err
	}
	backend, err := controller.open(ctx, recordLocation(submission), ObservationAccess)
	if err != nil || nilPort(backend) {
		if err == nil {
			err = ErrUnavailable
		}
		return controller.failed(
			ctx,
			record,
			err,
			false,
		)
	}
	defer backend.Close()
	info, err := backend.Probe(ctx)
	if err != nil {
		return controller.failed(
			ctx,
			record,
			err,
			false,
		)
	}
	if info.Validate() != nil || !slices.Contains(info.Capabilities.Features, jobcontract.FeatureExecutionOrigin) {
		return controller.failed(
			ctx,
			record,
			ErrUpgradeRequired,
			false,
		)
	}
	if !sameInstallation(submission, info) {
		return controller.failed(
			ctx,
			record,
			ErrTargetChanged,
			false,
		)
	}
	confirmation, err := backend.Command(ctx, record.Command)
	if err != nil {
		return controller.failed(
			ctx,
			record,
			err,
			errors.Is(err, ErrConflict),
		)
	}
	next := record
	next.Revision++
	next.State, next.Observation, next.Confirmation = ControlConfirmed, ObservationNone, &confirmation
	next.UpdatedAt = controller.updatedAt(record)
	if next.ValidateFor(submission) != nil {
		return controller.failed(
			ctx,
			record,
			ErrInvalid,
			false,
		)
	}
	return controller.save(ctx, record, next)
}

func (controller *Controller) failed(
	ctx context.Context,
	previous ControlRecord,
	cause error,
	rejected bool,
) (ControlRecord, error) {
	safeCause := operationError(ctx, cause)
	if ctx.Err() == nil {
		switch {
		case errors.Is(cause, ErrConflict):
			safeCause = ErrConflict
		case errors.Is(cause, ErrNotFound):
			safeCause = ErrNotFound
		}
	}
	next := previous
	next.Revision++
	next.UpdatedAt, next.Observation = controller.updatedAt(previous), ObservationUnreachable
	switch {
	case errors.Is(safeCause, ErrConflict):
		next.Observation = ObservationRejected
	case errors.Is(safeCause, ErrTargetChanged):
		next.Observation = ObservationIdentityChanged
	case errors.Is(safeCause, ErrNotFound):
		next.Observation = ObservationNotFound
	case errors.Is(safeCause, ErrUpgradeRequired), errors.Is(safeCause, ErrInvalid):
		next.Observation = ObservationRejected
	}
	if rejected {
		next.State, next.Observation = ControlRejected, ObservationRejected
	}
	stored, err := controller.save(ctx, previous, next)
	if err != nil {
		return stored, err
	}
	if stored.State == ControlConfirmed {
		return stored, nil
	}
	if stored.State == ControlRejected {
		return stored, ErrConflict
	}
	return stored, errors.Join(ErrPending, safeCause)
}

func (controller *Controller) save(
	ctx context.Context,
	previous ControlRecord,
	next ControlRecord,
) (ControlRecord, error) {
	for range 3 {
		err := controller.repository.UpdateControl(ctx, next, previous.Revision)
		if err == nil {
			return next, nil
		}
		// Storage acknowledgement loss or another delivery can already have
		// committed the result. Read only; never infer success from the transport.
		current, readErr := controller.repository.GetControl(ctx, previous.Command.CommandID)
		matching := readErr == nil && current.Command == previous.Command && current.CreatedAt.Equal(previous.CreatedAt)
		if matching && current.State != ControlPending {
			return current, nil
		}
		if matching && errors.Is(err, ErrConflict) {
			previous = current
			next.Revision = current.Revision + 1
			if next.UpdatedAt.Before(current.UpdatedAt) {
				next.UpdatedAt = current.UpdatedAt
			}
			continue
		}
		return previous, err
	}
	return previous, errors.Join(ErrPending, ErrConflict)
}

func (controller *Controller) updatedAt(record ControlRecord) time.Time {
	at := controller.now().UTC()
	if at.Before(record.UpdatedAt) {
		return record.UpdatedAt
	}
	return at
}
