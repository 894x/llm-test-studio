package jobs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type controlTargetReader struct{ read JobRead }

func (reader *controlTargetReader) ReadJob(context.Context, string) (JobRead, error) {
	return reader.read, nil
}

type targetControlRepository struct {
	record ControlRecord
	create error
}

func (repository *targetControlRepository) CreateControl(_ context.Context, record ControlRecord) (ControlRecord, error) {
	if repository.create != nil {
		return ControlRecord{}, repository.create
	}
	repository.record = record
	return record, nil
}

func (repository *targetControlRepository) GetControl(context.Context, string) (ControlRecord, error) {
	if repository.record.Command.CommandID == "" {
		return ControlRecord{}, ErrNotFound
	}
	return repository.record, nil
}

func (repository *targetControlRepository) UpdateControl(_ context.Context, next ControlRecord, expected uint64) error {
	if repository.record.Revision != expected {
		return ErrConflict
	}
	if err := repository.record.ValidateSuccessor(next); err != nil {
		return err
	}
	repository.record = next
	return nil
}

func (repository *targetControlRepository) ListPendingControls(context.Context, string, uint32) (ControlPage, error) {
	return ControlPage{Records: []ControlRecord{repository.record}}, nil
}

type targetControlBackend struct {
	Backend
	info       jobcontract.RunnerInfo
	snapshot   jobcontract.Snapshot
	commands   []jobcontract.Command
	commandErr error
	closed     int
}

func (backend *targetControlBackend) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return backend.info, nil
}

func (backend *targetControlBackend) Command(_ context.Context, command jobcontract.Command) (jobcontract.CommandConfirmation, error) {
	backend.commands = append(backend.commands, command)
	return jobcontract.CommandConfirmation{Command: command, AcceptedAt: backend.snapshot.UpdatedAt,
		Snapshot: backend.snapshot}, backend.commandErr
}

func (backend *targetControlBackend) Close() error {
	backend.closed++
	return nil
}

func newTargetController(t *testing.T, read JobRead) (*Controller, *controlTargetReader, *targetControlRepository, *targetControlBackend, *bool) {
	t.Helper()
	reader := &controlTargetReader{read: read}
	repository := &targetControlRepository{}
	prepared := ownJobFixture(t, read, SubmissionAccepted).Submission.Prepared
	backend := &targetControlBackend{info: prepared.Runner, snapshot: *read.Execution}
	backend.info.EngineBuildID, backend.info.RunnerVersion = strings.Repeat("b", 64), "current-r7"
	backend.snapshot.Receipt.Status, backend.snapshot.Receipt.Revision = domain.RunCancelled, 2
	backend.snapshot.LatestSequence, backend.snapshot.UpdatedAt = 2, backend.snapshot.UpdatedAt.Add(time.Second)
	online := true
	controller, err := NewController(ControllerDependencies{Repository: repository, Targets: reader,
		Now: func() time.Time { return read.Execution.UpdatedAt.Add(2 * time.Second) },
		Open: func(_ context.Context, location Location, access Access) (Backend, error) {
			if location != read.Location || access != ObservationAccess {
				t.Error("control changed scope or used submission access")
			}
			if !online {
				return nil, errors.New("private connection details")
			}
			return backend, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	return controller, reader, repository, backend, &online
}

func TestForeignControlOfflineReplayUsesFrozenTargetAndOriginalCommand(t *testing.T) {
	read := sharedJobFixture(t)
	controller, _, repository, backend, online := newTargetController(t, read)
	*online = false
	pending, err := controller.Request(t.Context(), ControlInput{JobID: read.Spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
	if !errors.Is(err, ErrPending) || pending.Target.Acceptance == nil || pending.State != ControlPending || len(backend.commands) != 0 {
		t.Fatal("offline foreign control was not durably pending")
	}
	if pending.Target.Acceptance.Origin.RunnerVersion != "original-r4" || repository.record.Command != pending.Command {
		t.Fatal("foreign control invented submission facts or replaced the historical executor")
	}
	*online = true
	confirmed, err := controller.Deliver(t.Context(), pending.Command.CommandID)
	if err != nil || confirmed.State != ControlConfirmed || !confirmed.Target.Equal(pending.Target) {
		t.Fatalf("replayed foreign control failed against a compatible newer daemon: %v", err)
	}
	if len(backend.commands) != 1 || backend.commands[0] != pending.Command || backend.closed != 1 {
		t.Fatal("delivery minted a new command, changed expected revision, or leaked its connection")
	}
}

func TestControlDoesNotSendWithoutPersistedTargetOrAfterScopeChange(t *testing.T) {
	read := sharedJobFixture(t)
	controller, reader, repository, backend, online := newTargetController(t, read)
	repository.create = errors.New("commit not acknowledged")
	proposed, err := controller.Request(t.Context(), ControlInput{JobID: read.Spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
	if err == nil || proposed.Validate() != nil || len(backend.commands) != 0 {
		t.Fatal("ambiguous create sent an unproven instruction")
	}
	repository.create = nil
	*online = false
	pending, _ := controller.Request(t.Context(), ControlInput{JobID: read.Spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
	*online = true
	reader.read.Location.ConfigurationID = "83000000-0000-4000-8000-000000000015"
	if _, err := controller.Deliver(t.Context(), pending.Command.CommandID); !errors.Is(err, ErrConflict) || len(backend.commands) != 0 {
		t.Fatal("changed source scope sent the frozen command to a replacement binding")
	}
}

func TestControlTargetFreezesAcceptanceAndKeepsPendingUnknownAcceptance(t *testing.T) {
	read := sharedJobFixture(t)
	target, err := read.ControlTarget()
	if err != nil || target.Acceptance == nil {
		t.Fatal("foreign target lacks actual acceptance")
	}
	changed := target
	acceptance := *target.Acceptance
	acceptance.Origin.RunnerVersion = "another-executor"
	changed.Acceptance = &acceptance
	if err := changed.ValidateFor(read); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign acceptance was replaceable")
	}
	changed = target
	changed.Acceptance = nil
	if err := changed.ValidateFor(read); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign command admitted a missing acceptance")
	}
	own := ownJobFixture(t, read, SubmissionPending)
	own.Execution = nil
	unknown, err := own.ControlTarget()
	if err != nil || unknown.Acceptance != nil {
		t.Fatal("pending own submission invented an admission")
	}
	own = ownJobFixture(t, read, SubmissionAccepted)
	if err := unknown.ValidateFor(own); err != nil {
		t.Fatal("later acceptance invalidated an already durable own pending target")
	}
	record := pendingControlFixture(1)
	record.Command.JobID, record.Target = read.Spec.JobID, unknown
	next := record
	next.Revision++
	next.Target = target
	if err := record.ValidateSuccessor(next); !errors.Is(err, ErrConflict) {
		t.Fatal("successor rewrote a frozen nullable acceptance")
	}
}

func TestControlRejectsReplacementInstallationAndChangedConfirmationOrigin(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*targetControlBackend)
		posts  int
	}{
		{name: "installation", change: func(backend *targetControlBackend) {
			backend.info.NodeInstallationID = "83000000-0000-4000-8000-000000000015"
		}},
		{name: "missing origin capability", change: func(backend *targetControlBackend) { backend.info.Capabilities.Features = []string{} }},
		{name: "confirmation origin", change: func(backend *targetControlBackend) {
			backend.snapshot.Receipt.Origin.RunnerVersion = "replacement"
		}, posts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			read := sharedJobFixture(t)
			controller, _, _, backend, _ := newTargetController(t, read)
			test.change(backend)
			record, err := controller.Request(t.Context(), ControlInput{JobID: read.Spec.JobID,
				Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
			if !errors.Is(err, ErrPending) || record.State != ControlPending || len(backend.commands) != test.posts {
				t.Fatal("invalid remote identity or confirmation was accepted")
			}
		})
	}
}

func TestPendingLocalControlConfirmationMustMatchFrozenNodeBeforeAdmissionIsKnown(t *testing.T) {
	read := sharedJobFixture(t)
	read.Spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetLocal}
	read.Location = Location{Kind: jobcontract.TargetLocal}
	origin := read.Execution.Receipt.Origin
	origin.Kind = "local"
	snapshot, err := read.Spec.InitialSnapshot(read.Execution.Receipt.AcceptedAt, origin)
	if err != nil {
		t.Fatal(err)
	}
	read.Execution = &snapshot
	controller, reader, _, backend, _ := newTargetController(t, read)
	reader.read = ownJobFixture(t, read, SubmissionPending)
	reader.read.Execution = nil
	backend.snapshot.Receipt.Origin.NodeID = "83000000-0000-4000-8000-000000000015"
	record, err := controller.Request(t.Context(), ControlInput{JobID: read.Spec.JobID,
		Kind: jobcontract.CommandCancel, ExpectedRevision: 1})
	if !errors.Is(err, ErrPending) || record.State != ControlPending || record.Confirmation != nil || len(backend.commands) != 1 {
		t.Fatal("unknown local admission accepted a confirmation from another physical node")
	}
}
