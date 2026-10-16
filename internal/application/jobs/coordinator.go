package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type Location struct {
	Kind            jobcontract.TargetKind `json:"kind"`
	ConfigurationID string                 `json:"configuration_id"`
}

func (location Location) Validate() error {
	switch location.Kind {
	case jobcontract.TargetLocal:
		if location.ConfigurationID == "" {
			return nil
		}
	case jobcontract.TargetRemote:
		if domain.IsUUID(location.ConfigurationID) {
			return nil
		}
	}
	return ErrInvalid
}

// Backend owns one management connection, never the lifetime of accepted work.
// GetJob must return ErrNotFound only for an authoritative current-node 404.
// Command returns ErrConflict only when the node explicitly refuses that
// instruction; transport uncertainty must retain its pending identity.
// Connectors return ErrTargetChanged for a replaced saved binding.
type Backend interface {
	Probe(context.Context) (jobcontract.RunnerInfo, error)
	Submit(context.Context, *jobcontract.Submission) (jobcontract.Receipt, error)
	GetJob(context.Context, string) (jobcontract.Snapshot, error)
	Command(context.Context, jobcontract.Command) (jobcontract.CommandConfirmation, error)
	Close() error
}

// Access separates new-submission policy from reading already accepted work.
// Disabling intake must not prevent recovery of that node's existing tasks.
type Access string

const (
	SubmissionAccess  Access = "submission"
	ObservationAccess Access = "observation"
)

type CoordinatorDependencies struct {
	Repository SubmissionRepository
	Open       func(context.Context, Location, Access) (Backend, error)
	Now        func() time.Time
}

type PrepareCommand struct {
	Location Location
	JobID    string
	Kind     jobcontract.Kind
	Payload  jobcontract.Payload
	Assets   []jobcontract.Asset
}

type Coordinator struct {
	repository SubmissionRepository
	open       func(context.Context, Location, Access) (Backend, error)
	now        func() time.Time
}

func NewCoordinator(dependencies CoordinatorDependencies) (*Coordinator, error) {
	validDependencies := !nilPort(dependencies.Repository) && dependencies.Open != nil
	if !validDependencies || dependencies.Now == nil {
		return nil, ErrInvalid
	}
	return &Coordinator{repository: dependencies.Repository, open: dependencies.Open, now: dependencies.Now}, nil
}

func (coordinator *Coordinator) Prepare(ctx context.Context, command PrepareCommand) (SubmissionRecord, error) {
	validCommand := command.Location.Validate() == nil && domain.IsUUID(command.JobID)
	if ctx == nil || !validCommand {
		return SubmissionRecord{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	backend, err := coordinator.open(ctx, command.Location, SubmissionAccess)
	if err != nil {
		return SubmissionRecord{}, operationError(ctx, err)
	}
	if nilPort(backend) {
		return SubmissionRecord{}, ErrUnavailable
	}
	defer backend.Close()
	info, err := backend.Probe(ctx)
	if err != nil {
		return SubmissionRecord{}, operationError(ctx, err)
	}
	if info.Validate() != nil {
		return SubmissionRecord{}, ErrUpgradeRequired
	}
	if !info.Ready {
		return SubmissionRecord{}, ErrUnavailable
	}
	target := jobcontract.ExecutionTarget{Kind: command.Location.Kind}
	if target.Kind == jobcontract.TargetRemote {
		target.NodeID = info.NodeID
	}
	spec, err := (jobcontract.JobSpec{Contract: jobcontract.CurrentContract, JobID: command.JobID,
		NodeInstallationID: info.NodeInstallationID, EngineBuildID: info.EngineBuildID, Target: target,
		Kind: command.Kind, Spec: command.Payload, Assets: command.Assets}).Freeze()
	if err != nil {
		return SubmissionRecord{}, err
	}
	if !supportsSubmission(info, spec) {
		return SubmissionRecord{}, ErrUpgradeRequired
	}
	record, err := NewSubmission(PreparedJob{ConfigurationID: command.Location.ConfigurationID,
		Runner: info, Spec: spec}, coordinator.now().UTC())
	if err != nil {
		return SubmissionRecord{}, err
	}
	return coordinator.repository.CreateSubmission(ctx, record)
}

// Submit takes ownership of every supplied lease, including error paths. Only
// the process that durably advances prepared to pending may send the request.
// A later caller must reconcile pending work, not retry a new destination/ID.
func (coordinator *Coordinator) Submit(
	ctx context.Context,
	id string,
	leases map[string]*credentials.Lease,
) (SubmissionRecord, error) {
	defer closeLeases(leases)
	if ctx == nil || !domain.IsUUID(id) {
		return SubmissionRecord{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record, err := coordinator.repository.GetSubmission(ctx, id)
	if err != nil {
		return SubmissionRecord{}, err
	}
	if record.State == SubmissionAccepted {
		return record, nil
	}
	if record.State != SubmissionPrepared {
		return record, ErrPending
	}
	submission, err := leasedSubmission(record.Prepared.Spec, leases)
	if err != nil {
		return record, err
	}
	defer submission.Clear()
	backend, err := coordinator.open(ctx, recordLocation(record), SubmissionAccess)
	if err != nil {
		return record, operationError(ctx, err)
	}
	if nilPort(backend) {
		return record, ErrUnavailable
	}
	defer backend.Close()
	info, err := backend.Probe(ctx)
	if err != nil {
		return record, operationError(ctx, err)
	}
	if info.Validate() != nil {
		return record, ErrUpgradeRequired
	}
	if !sameInstallation(record, info) || info.EngineBuildID != record.Prepared.Spec.EngineBuildID {
		return record, ErrTargetChanged
	}
	if !info.Ready {
		return record, ErrUnavailable
	}
	if !supportsSubmission(info, record.Prepared.Spec) {
		return record, ErrUpgradeRequired
	}
	previous := record.Revision
	pending := record
	pending.Revision++
	pending.State = SubmissionPending
	pending.UpdatedAt = coordinator.updatedAt(pending)
	if err := coordinator.repository.UpdateSubmission(ctx, pending, previous); err != nil {
		// An ambiguous database commit never authorizes a POST. Reconciliation
		// may discover a pending intent, but it cannot infer this caller won.
		return record, err
	}
	record = pending
	receipt, err := backend.Submit(ctx, submission)
	if err != nil {
		observation := ObservationUnreachable
		if errors.Is(err, ErrInvalid) || errors.Is(err, ErrUpgradeRequired) {
			observation = ObservationRejected
		}
		observed, _ := coordinator.observe(ctx, record, observation)
		if observed.State == SubmissionAccepted {
			return observed, nil
		}
		return observed, ErrPending
	}
	if !receiptMatches(receipt, record.Prepared.Spec) {
		observed, _ := coordinator.observe(ctx, record, ObservationRejected)
		if observed.State == SubmissionAccepted {
			return observed, nil
		}
		return observed, ErrPending
	}
	return coordinator.confirm(ctx, record, receipt)
}

// Reconcile only reads the original destination. A current 404 is an
// observation, not proof that an earlier HTTP handler cannot still accept it.
func (coordinator *Coordinator) Reconcile(ctx context.Context, id string) (SubmissionRecord, error) {
	if ctx == nil || !domain.IsUUID(id) {
		return SubmissionRecord{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	record, err := coordinator.repository.GetSubmission(ctx, id)
	if err != nil || record.State != SubmissionPending {
		return record, err
	}
	backend, err := coordinator.open(ctx, recordLocation(record), ObservationAccess)
	if err != nil {
		if errors.Is(operationError(ctx, err), ErrUpgradeRequired) {
			return coordinator.rejectUnsupportedRunner(ctx, record)
		}
		observation := ObservationUnreachable
		if errors.Is(err, ErrTargetChanged) {
			observation = ObservationIdentityChanged
		}
		return coordinator.observe(ctx, record, observation)
	}
	if nilPort(backend) {
		return coordinator.observe(ctx, record, ObservationUnreachable)
	}
	defer backend.Close()
	info, err := backend.Probe(ctx)
	unsupportedProtocol := errors.Is(operationError(ctx, err), ErrUpgradeRequired)
	unsupportedMetadata := err == nil && errors.Is(info.Validate(), jobcontract.ErrUnsupported)
	if unsupportedProtocol || unsupportedMetadata {
		return coordinator.rejectUnsupportedRunner(ctx, record)
	}
	if err != nil || info.Validate() != nil {
		return coordinator.observe(ctx, record, ObservationUnreachable)
	}
	if !sameInstallation(record, info) {
		return coordinator.observe(ctx, record, ObservationIdentityChanged)
	}
	// Querying accepted work remains valid during maintenance and after a
	// compatible executor upgrade. No new submission uses the changed build.
	snapshot, err := backend.GetJob(ctx, id)
	if errors.Is(operationError(ctx, err), ErrUpgradeRequired) {
		return coordinator.rejectUnsupportedRunner(ctx, record)
	}
	if errors.Is(err, ErrNotFound) {
		return coordinator.observe(ctx, record, ObservationNotFound)
	}
	if err != nil {
		return coordinator.observe(ctx, record, ObservationUnreachable)
	}
	if snapshot.Validate(record.Prepared.Spec) != nil {
		return coordinator.observe(ctx, record, ObservationRejected)
	}
	return coordinator.confirm(ctx, record, snapshot.Receipt)
}

func (coordinator *Coordinator) rejectUnsupportedRunner(
	ctx context.Context,
	record SubmissionRecord,
) (SubmissionRecord, error) {
	observed, err := coordinator.observe(ctx, record, ObservationRejected)
	if err != nil || observed.State == SubmissionAccepted {
		return observed, err
	}
	return observed, ErrUpgradeRequired
}

func (coordinator *Coordinator) confirm(
	ctx context.Context,
	prepared SubmissionRecord,
	receipt jobcontract.Receipt,
) (SubmissionRecord, error) {
	for range 3 {
		current, err := coordinator.repository.GetSubmission(ctx, prepared.Prepared.Spec.JobID)
		if err != nil {
			return prepared, err
		}
		if !current.SamePreparation(prepared) {
			return current, ErrConflict
		}
		if current.State == SubmissionAccepted {
			if !current.Receipt.AcceptedAt.Equal(receipt.AcceptedAt) {
				return current, ErrConflict
			}
			return current, nil
		}
		if current.State != SubmissionPending {
			return current, ErrConflict
		}
		previous := current.Revision
		current.Revision++
		current.State, current.Observation = SubmissionAccepted, ObservationNone
		current.Receipt, current.UpdatedAt = &receipt, coordinator.updatedAt(current)
		err = coordinator.repository.UpdateSubmission(ctx, current, previous)
		if errors.Is(err, ErrConflict) {
			continue
		}
		if err != nil {
			return prepared, err
		}
		return current, nil
	}
	return prepared, ErrConflict
}

func (coordinator *Coordinator) observe(
	ctx context.Context,
	previous SubmissionRecord,
	observation Observation,
) (SubmissionRecord, error) {
	next := previous
	next.Revision++
	next.Observation, next.UpdatedAt = observation, coordinator.updatedAt(next)
	if err := coordinator.repository.UpdateSubmission(ctx, next, previous.Revision); err != nil {
		current, readErr := coordinator.repository.GetSubmission(ctx, previous.Prepared.Spec.JobID)
		if readErr == nil && current.State == SubmissionAccepted && current.SamePreparation(previous) {
			return current, nil
		}
		return previous, err
	}
	return next, nil
}

func (coordinator *Coordinator) updatedAt(record SubmissionRecord) time.Time {
	now := coordinator.now().UTC()
	if now.Before(record.UpdatedAt) {
		return record.UpdatedAt
	}
	return now
}

func leasedSubmission(spec jobcontract.JobSpec, leases map[string]*credentials.Lease) (*jobcontract.Submission, error) {
	slots := spec.SecretSlots()
	if len(slots) != len(leases) {
		return nil, ErrInvalid
	}
	submission := &jobcontract.Submission{Job: spec, Secrets: []jobcontract.Secret{}}
	for _, slot := range slots {
		value, err := leases[slot].Bytes()
		if err != nil {
			submission.Clear()
			return nil, ErrInvalid
		}
		submission.Secrets = append(submission.Secrets, jobcontract.Secret{Slot: slot, Value: value})
	}
	if err := submission.Validate(); err != nil {
		submission.Clear()
		return nil, ErrInvalid
	}
	raw, err := json.Marshal(submission)
	tooLarge := len(raw) > jobcontract.MaxSubmissionBytes
	clear(raw)
	if err != nil || tooLarge {
		submission.Clear()
		return nil, ErrInvalid
	}
	return submission, nil
}

func closeLeases(leases map[string]*credentials.Lease) {
	for _, lease := range leases {
		_ = lease.Close()
	}
}

func recordLocation(record SubmissionRecord) Location {
	return Location{Kind: record.Prepared.Spec.Target.Kind, ConfigurationID: record.Prepared.ConfigurationID}
}

func sameInstallation(record SubmissionRecord, info jobcontract.RunnerInfo) bool {
	return record.Prepared.Runner.NodeID == info.NodeID &&
		record.Prepared.Spec.NodeInstallationID == info.NodeInstallationID
}

func supportsSubmission(info jobcontract.RunnerInfo, spec jobcontract.JobSpec) bool {
	if !info.Capabilities.SupportsJob(spec) {
		return false
	}
	return spec.Kind != jobcontract.KindPerformance ||
		slices.Contains(info.Capabilities.Features, jobcontract.FeaturePerformanceArchives)
}

func operationError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, jobcontract.ErrUnsupported) {
		return ErrUpgradeRequired
	}
	for _, known := range []error{ErrInvalid, ErrUnavailable, ErrTargetChanged, ErrUpgradeRequired} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}

func nilPort(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	}
	return false
}
