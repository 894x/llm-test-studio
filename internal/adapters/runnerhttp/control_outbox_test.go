package runnerhttp_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type controlHTTPConnection struct{ *coordinatorConnection }

func (connection controlHTTPConnection) Command(
	ctx context.Context,
	command jobcontract.Command,
) (jobcontract.CommandConfirmation, error) {
	confirmation, err := connection.Client.Command(ctx, command)
	var remote *runnerclient.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "job_conflict":
			return jobcontract.CommandConfirmation{}, jobs.ErrConflict
		case "not_found":
			return jobcontract.CommandConfirmation{}, jobs.ErrNotFound
		}
	}
	return confirmation, err
}

type controlHTTPFixture struct {
	node            *httpFixture
	repository      *sqlite.Repository
	path            string
	job             jobs.SubmissionRecord
	controller      *jobs.Controller
	open            func(context.Context, jobs.Location, jobs.Access) (jobs.Backend, error)
	online          atomic.Bool
	observationOnly atomic.Bool
	dropACK         atomic.Bool
	posts           atomic.Int64
	closed          atomic.Int64
	probe           func(*jobcontract.RunnerInfo)
}

func newControlHTTPFixture(t *testing.T, kind jobcontract.Kind) *controlHTTPFixture {
	t.Helper()
	fixture := &controlHTTPFixture{node: newHTTPFixture(t, true, capabilities())}
	fixture.online.Store(true)
	identity := fixture.node.host.Identity()
	client, err := runnerclient.New(runnerclient.Identity{NodeID: identity.Target.NodeID,
		InstallationID: identity.InstallationID},
		func(ctx context.Context, input remotessh.Request) (*http.Response, error) {
			request, err := http.NewRequestWithContext(
				ctx,
				input.Method,
				fixture.node.api.URL+input.Route,
				input.Body,
			)
			if err != nil {
				return nil, err
			}
			request.Header.Set("Authorization", "Bearer "+nodeToken)
			request.Header.Set(jobcontract.ContractHeader, "1")
			if input.Body != nil {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := fixture.node.api.Client().Do(request)
			if err == nil && input.Method == http.MethodPost && input.Route != "/jobs" {
				fixture.posts.Add(1)
				if fixture.dropACK.Load() {
					_ = response.Body.Close()
					response.Body = io.NopCloser(strings.NewReader("{"))
				}
			}
			return response, err
		})
	if err != nil {
		t.Fatal(err)
	}
	fixture.open = func(_ context.Context, location jobs.Location, access jobs.Access) (jobs.Backend, error) {
		if fixture.observationOnly.Load() && access != jobs.ObservationAccess {
			t.Error("control operation used new-submission intake policy")
			return nil, jobs.ErrInvalid
		}
		if location != coordinatorLocation {
			t.Error("control changed destination or fell back to local")
			return nil, jobs.ErrTargetChanged
		}
		if !fixture.online.Load() {
			return nil, errors.New("untrusted transport details")
		}
		return controlHTTPConnection{&coordinatorConnection{Client: client, closed: &fixture.closed,
			probe: fixture.probe}}, nil
	}
	fixture.repository, fixture.path = coordinatorRepository(t)
	coordinator, err := jobs.NewCoordinator(jobs.CoordinatorDependencies{
		Repository: fixture.repository, Open: fixture.open, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	submission := fixture.node.runSubmission(t, kind == jobcontract.KindComparison)
	if kind == jobcontract.KindPerformance {
		submission = fixture.node.submission(t, nil)
	}
	_ = prepareCoordinatedJob(t, coordinator, submission)
	fixture.job, err = coordinator.Submit(t.Context(), submission.Job.JobID, coordinatorLeases(t, submission))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fixture.node.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("control fixture did not issue its first request")
	}
	fixture.reopenController(t, fixture.repository)
	fixture.observationOnly.Store(true)
	return fixture
}

func (fixture *controlHTTPFixture) reopenController(t *testing.T, controls jobs.ControlRepository) {
	t.Helper()
	var err error
	fixture.controller, err = jobs.NewController(jobs.ControllerDependencies{
		Repository: controls, Submissions: fixture.repository, Open: fixture.open, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (fixture *controlHTTPFixture) input(t *testing.T, kind jobcontract.CommandKind) jobs.ControlInput {
	t.Helper()
	snapshot, err := fixture.node.repository.GetJob(t.Context(), fixture.job.Prepared.Spec.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return jobs.ControlInput{JobID: snapshot.Receipt.JobID, Kind: kind, ExpectedRevision: snapshot.Receipt.Revision}
}

func TestControlOutboxReplaysOriginalHTTPCommandAcrossOfflineLostACKAndReopen(t *testing.T) {
	for _, kind := range []jobcontract.Kind{jobcontract.KindRun, jobcontract.KindComparison, jobcontract.KindPerformance} {
		for _, operation := range []jobcontract.CommandKind{jobcontract.CommandStopSending, jobcontract.CommandCancel} {
			t.Run(string(kind)+"/"+string(operation), func(t *testing.T) {
				fixture := newControlHTTPFixture(t, kind)
				fixture.online.Store(false)
				input := fixture.input(t, operation)
				pending, err := fixture.controller.Request(t.Context(), input)
				safeError := errors.Is(err, jobs.ErrPending) && !strings.Contains(err.Error(), "untrusted transport details")
				unsent := pending.State == jobs.ControlPending && fixture.posts.Load() == 0
				if !safeError || !unsent {
					t.Fatalf("offline control was sent, leaked details or became confirmed: %v", err)
				}
				current, _ := fixture.node.repository.GetJob(t.Context(), input.JobID)
				if current.Receipt.Status != domain.RunRunning {
					t.Fatal("offline cancellation changed authoritative node state")
				}
				if err := fixture.repository.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.repository = reopenCoordinatorRepository(t, fixture.path)
				fixture.reopenController(t, fixture.repository)
				fixture.online.Store(true)
				fixture.dropACK.Store(true)
				uncertain, err := fixture.controller.Deliver(t.Context(), pending.Command.CommandID)
				validUncertainty := errors.Is(err, jobs.ErrPending) && uncertain.Command == pending.Command &&
					uncertain.State == jobs.ControlPending
				if !validUncertainty {
					t.Fatalf("truncated control ACK changed command identity or fabricated confirmation: %v", err)
				}
				original, err := fixture.node.repository.GetJobCommand(t.Context(), pending.Command.CommandID)
				if err != nil || original.Command != pending.Command {
					t.Fatalf("node did not durably apply the original command: %v", err)
				}
				want := domain.RunCancelled
				if operation == jobcontract.CommandStopSending {
					fixture.node.release()
					want = domain.RunCompleted
				}
				waitHTTPJob(
					t,
					fixture.node,
					input.JobID,
					want,
				)
				requests := fixture.node.requests.Load()
				if err := fixture.repository.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.repository = reopenCoordinatorRepository(t, fixture.path)
				fixture.reopenController(t, fixture.repository)
				fixture.dropACK.Store(false)
				confirmed, err := fixture.controller.Deliver(t.Context(), pending.Command.CommandID)
				validConfirmation := err == nil && confirmed.State == jobs.ControlConfirmed && confirmed.Confirmation != nil
				stableTraffic := fixture.node.requests.Load() == requests
				stableConfirmation := validConfirmation && reflect.DeepEqual(*confirmed.Confirmation, original)
				if !stableConfirmation || !stableTraffic {
					t.Fatalf("reopened command was not the original stable confirmation: %v", err)
				}
				if fixture.posts.Load() != 2 {
					t.Fatal("unexpected command delivery count")
				}
				_, err = fixture.controller.Deliver(t.Context(), pending.Command.CommandID)
				if err != nil || fixture.posts.Load() != 2 {
					t.Fatal("confirmed command was sent again")
				}
				page, err := fixture.node.repository.JobEvents(
					t.Context(),
					input.JobID,
					0,
					200,
				)
				if err != nil {
					t.Fatal(err)
				}
				var commandEvents int
				for _, event := range page.Events {
					if event.Kind == jobcontract.EventCommand {
						commandEvents++
					}
				}
				if commandEvents != 1 {
					t.Fatal("command replay created another decision or journal event")
				}
			})
		}
	}
}

func TestControlOutboxRejectsStaleRevisionWithoutReplacingTerminalWinner(t *testing.T) {
	fixture := newControlHTTPFixture(t, jobcontract.KindPerformance)
	input := fixture.input(t, jobcontract.CommandCancel)
	fixture.online.Store(false)
	pending, err := fixture.controller.Request(t.Context(), input)
	if !errors.Is(err, jobs.ErrPending) {
		t.Fatal(err)
	}
	fixture.node.release()
	waitHTTPJob(
		t,
		fixture.node,
		input.JobID,
		domain.RunCompleted,
	)
	fixture.online.Store(true)
	rejected, err := fixture.controller.Deliver(t.Context(), pending.Command.CommandID)
	validRejection := rejected.State == jobs.ControlRejected && rejected.Confirmation == nil
	if !errors.Is(err, jobs.ErrConflict) || !validRejection {
		t.Fatalf("stale control acquired a fabricated terminal confirmation: %v", err)
	}
	current, _ := fixture.node.repository.GetJob(t.Context(), input.JobID)
	if current.Receipt.Status != domain.RunCompleted {
		t.Fatal("late cancellation replaced the completed winner")
	}
	_, err = fixture.controller.Deliver(t.Context(), pending.Command.CommandID)
	if err != nil || fixture.posts.Load() != 1 {
		t.Fatal("permanently rejected instruction kept retrying")
	}
}

func TestControlOutboxUsesFixedInstallationButAllowsCompatibleBuildChangesAndMaintenance(t *testing.T) {
	for _, state := range []string{"identity changed", "unsupported", "missing origin capability", "compatible maintenance"} {
		t.Run(state, func(t *testing.T) {
			fixture := newControlHTTPFixture(t, jobcontract.KindPerformance)
			if state == "compatible maintenance" {
				if _, err := fixture.node.maintenance.Apply(t.Context(), jobcontract.MaintenanceCommand{
					Contract: jobcontract.CurrentContract, OperationID: coordinatorLocation.ConfigurationID,
					Action: jobcontract.MaintenancePause,
				}); err != nil {
					t.Fatal(err)
				}
			}
			fixture.probe = func(info *jobcontract.RunnerInfo) {
				switch state {
				case "identity changed":
					info.NodeInstallationID = coordinatorLocation.ConfigurationID
				case "unsupported":
					info.Contract++
				case "missing origin capability":
					info.Capabilities.Features = slices.DeleteFunc(info.Capabilities.Features, func(feature string) bool {
						return feature == jobcontract.FeatureExecutionOrigin
					})
				case "compatible maintenance":
					info.EngineBuildID = strings.Repeat("c", 64)
					info.RunnerVersion = "another-compatible-release"
				}
			}
			record, err := fixture.controller.Request(t.Context(), fixture.input(t, jobcontract.CommandCancel))
			if state == "compatible maintenance" {
				validDelivery := record.State == jobs.ControlConfirmed && fixture.posts.Load() == 1
				if err != nil || !validDelivery {
					t.Fatalf("compatible upgrade/maintenance blocked control of the original job: %v", err)
				}
				return
			}
			pendingWithoutDelivery := record.State == jobs.ControlPending && fixture.posts.Load() == 0
			if !errors.Is(err, jobs.ErrPending) || !pendingWithoutDelivery {
				t.Fatalf("changed installation or unsupported contract received a command: %v", err)
			}
		})
	}
}

type failingControlStore struct {
	*sqlite.Repository
	createFails       bool
	commitFirst       bool
	confirmationFails bool
}

func (store failingControlStore) CreateControl(
	ctx context.Context,
	record jobs.ControlRecord,
) (jobs.ControlRecord, error) {
	if !store.createFails {
		return store.Repository.CreateControl(ctx, record)
	}
	if store.commitFirst {
		if _, err := store.Repository.CreateControl(ctx, record); err != nil {
			return jobs.ControlRecord{}, err
		}
	}
	return jobs.ControlRecord{}, errors.New("injected intent persistence failure")
}

func (store failingControlStore) UpdateControl(ctx context.Context, next jobs.ControlRecord, expected uint64) error {
	if !store.confirmationFails || next.State != jobs.ControlConfirmed {
		return store.Repository.UpdateControl(ctx, next, expected)
	}
	if err := store.Repository.UpdateControl(ctx, next, expected); err != nil {
		return err
	}
	return errors.New("injected confirmation acknowledgement loss")
}

func TestControlOutboxDoesNotSendOnUnconfirmedPersistenceAndReadsLostStoredConfirmation(t *testing.T) {
	for _, commitFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed write", true: "lost commit acknowledgement"}[commitFirst], func(t *testing.T) {
			fixture := newControlHTTPFixture(t, jobcontract.KindPerformance)
			fixture.reopenController(t, failingControlStore{Repository: fixture.repository,
				createFails: true, commitFirst: commitFirst})
			record, err := fixture.controller.Request(t.Context(), fixture.input(t, jobcontract.CommandCancel))
			if err == nil || fixture.posts.Load() != 0 {
				t.Fatal("unconfirmed intent persistence sent a command")
			}
			stored, readErr := fixture.repository.GetControl(t.Context(), record.Command.CommandID)
			if !commitFirst {
				if !errors.Is(readErr, jobs.ErrNotFound) {
					t.Fatal("failed write left a control instruction")
				}
				return
			}
			originalIntent := stored.Command == record.Command && stored.State == jobs.ControlPending
			if readErr != nil || !originalIntent {
				t.Fatalf("lost commit did not retain the exact recoverable instruction: %v", readErr)
			}
			fixture.reopenController(t, failingControlStore{Repository: fixture.repository, confirmationFails: true})
			confirmed, err := fixture.controller.Deliver(t.Context(), record.Command.CommandID)
			validConfirmation := confirmed.State == jobs.ControlConfirmed && fixture.posts.Load() == 1
			if err != nil || !validConfirmation {
				t.Fatalf("lost stored confirmation was not recovered by a read: %v", err)
			}
		})
	}
}

func TestControlDeliveryWatcherResumesPersistedOfflineInstructionWithoutCreatingAnother(t *testing.T) {
	fixture := newControlHTTPFixture(t, jobcontract.KindPerformance)
	fixture.online.Store(false)
	pending, err := fixture.controller.Request(t.Context(), fixture.input(t, jobcontract.CommandCancel))
	if !errors.Is(err, jobs.ErrPending) {
		t.Fatal(err)
	}
	if err := fixture.repository.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.repository = reopenCoordinatorRepository(t, fixture.path)
	fixture.reopenController(t, fixture.repository)
	fixture.online.Store(true)
	watcher, err := jobs.NewControlDeliveryWatcher(t.Context(), jobs.ControlDeliveryDependencies{
		Repository: fixture.repository, Deliver: fixture.controller.Deliver,
	})
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- watcher.Run() }()
	defer func() { _ = watcher.Close(); <-finished }()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		record, err := fixture.repository.GetControl(ctx, pending.Command.CommandID)
		if err == nil && record.State == jobs.ControlConfirmed {
			if record.Command != pending.Command || fixture.posts.Load() != 1 {
				t.Fatal("recovery changed the original instruction or sent an extra command")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("background delivery did not resume the durable instruction")
		case <-time.After(10 * time.Millisecond):
		}
	}
	waitHTTPJob(
		t,
		fixture.node,
		pending.Command.JobID,
		domain.RunCancelled,
	)
}

func TestControlOutboxConcurrentClientsDeliverOneDurableDecision(t *testing.T) {
	fixture := newControlHTTPFixture(t, jobcontract.KindPerformance)
	fixture.online.Store(false)
	pending, err := fixture.controller.Request(t.Context(), fixture.input(t, jobcontract.CommandCancel))
	if !errors.Is(err, jobs.ErrPending) {
		t.Fatal(err)
	}
	fixture.online.Store(true)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	originalOpen := fixture.open
	open := func(ctx context.Context, location jobs.Location, access jobs.Access) (jobs.Backend, error) {
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return originalOpen(ctx, location, access)
		}
	}
	secondRepository := reopenCoordinatorRepository(t, fixture.path)
	results := make(chan error, 2)
	for _, repository := range []*sqlite.Repository{fixture.repository, secondRepository} {
		controller, err := jobs.NewController(jobs.ControllerDependencies{
			Repository: repository, Submissions: repository, Open: open, Now: time.Now,
		})
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			confirmed, err := controller.Deliver(t.Context(), pending.Command.CommandID)
			if err == nil && (confirmed.State != jobs.ControlConfirmed || confirmed.Command != pending.Command) {
				err = errors.New("concurrent delivery changed its command or confirmation")
			}
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("both clients did not read the pending instruction before delivery")
		}
	}
	unblock()
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	waitHTTPJob(
		t,
		fixture.node,
		pending.Command.JobID,
		domain.RunCancelled,
	)
	if fixture.posts.Load() != 2 {
		t.Fatal("concurrency fixture did not issue both same-ID command deliveries")
	}
	page, err := fixture.node.repository.JobEvents(
		t.Context(),
		pending.Command.JobID,
		0,
		200,
	)
	if err != nil {
		t.Fatal(err)
	}
	var decisions int
	for _, event := range page.Events {
		if event.Kind == jobcontract.EventCommand {
			decisions++
		}
	}
	if decisions != 1 {
		t.Fatal("shared command ID produced multiple durable decisions")
	}
}
