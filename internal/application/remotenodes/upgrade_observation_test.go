package remotenodes

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func observationIntent(t *testing.T, fixture *nodeFixture, node Node) Upgrade {
	t.Helper()
	request := runnerinstall.UpgradeRequest{
		Contract: jobcontract.CurrentContract, OperationID: fixtureID(t), NodeID: node.Binding.Identity.NodeID,
		InstallationID:  node.Binding.Identity.InstallationID,
		ExpectedBuildID: node.Observation.Runner.EngineBuildID, ArtifactSHA256: strings.Repeat("f", 64),
	}
	operation := runnerinstall.UpgradeOperation{Request: request, Stage: "backing_up", ChangedAt: fixture.now}
	attempt := Upgrade{Request: request, Release: "v-observation", Arch: "amd64",
		TemporaryDirectory: remotessh.UpgradeDirectory(request.OperationID), Stage: operation.Stage,
		Operation: &operation, ErrorCode: "remote_connection_failed", ChangedAt: fixture.now}
	storeObservationIntent(t, fixture, node.ID, attempt)
	return attempt
}

func storeObservationIntent(t *testing.T, fixture *nodeFixture, id string, attempt Upgrade) {
	t.Helper()
	if err := fixture.store.withNodeOperation(t.Context(), id, func() error {
		return fixture.store.writeUpgrade(t.Context(), id, attempt)
	}); err != nil {
		t.Fatal(err)
	}
}

func forbidObservationWork(t *testing.T, manager *Manager, remote *remoteFixture) {
	t.Helper()
	manager.artifacts = artifactFixtureSource{open: func(context.Context, string, string) (*runnerartifacts.Artifact, error) {
		t.Error("observation opened an executable artifact")
		return nil, runnerartifacts.ErrUnavailable
	}}
	remote.probe = func(context.Context) (remotessh.SystemInfo, error) {
		t.Error("upgrade observation fell through to daemon health or installation")
		return remotessh.SystemInfo{}, runnerinstall.ErrService
	}
	remote.provision = func(context.Context, runnerinstall.Request, remotessh.Artifact,
		func(runnerinstall.Progress)) (runnerinstall.Result, error) {
		t.Error("observation provisioned a runner")
		return runnerinstall.Result{}, runnerinstall.ErrService
	}
	deny := func(context.Context, runnerinstall.UpgradeRequest, remotessh.Artifact,
		func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		t.Error("observation executed an upgrade action")
		return runnerinstall.UpgradeOperation{}, runnerinstall.ErrService
	}
	remote.upgrade, remote.retryUpgrade, remote.recoverUpgrade = deny, deny, deny
}

func TestUpgradeObservationCheckAfterReopenConfirmsWithoutExecuting(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, original := upgradeManagerFixture(t, remote)
	attempt := observationIntent(t, fixture, node)
	if err := original.Close(); err != nil {
		t.Fatal(err)
	}
	manager := managerFixture(t, fixture, remote)
	forbidObservationWork(t, manager, remote)
	var queries atomic.Int32
	remote.upgradeOperation = func(ctx context.Context, id string) (runnerinstall.UpgradeOperation, error) {
		queries.Add(1)
		deadline, bounded := ctx.Deadline()
		if id != attempt.Request.OperationID || !bounded || time.Until(deadline) > 15*time.Second {
			t.Error("journal observation lost its stable ID or short read budget")
		}
		return completedUpgrade(node, attempt.Request, fixture.now.Add(time.Second)), nil
	}
	if err := manager.Check(t.Context(), node.ID, node.Revision); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || stored.Stage != "completed" || stored.ErrorCode != "" || queries.Load() != 1 {
		t.Fatalf("same-operation completion was not observed: %v", err)
	}
	if _, err := manager.GetUpgrade(t.Context(), node.ID); err != nil || queries.Load() != 1 {
		t.Fatal("GetUpgrade performed network work")
	}
}

func TestUpgradeObservationDisabledRequiresIntentAndExplicitCheck(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_intent", true: "pending"}[pending], func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			var attempt Upgrade
			if pending {
				attempt = observationIntent(t, fixture, node)
			}
			if err := fixture.store.withConfiguration(t.Context(), func(config *Configuration) error {
				config.Nodes[0].Enabled = false
				return fixture.store.persist(t.Context(), config)
			}); err != nil {
				t.Fatal(err)
			}
			forbidObservationWork(t, manager, remote)
			var connections, queries atomic.Int32
			manager.connect = func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
				connections.Add(1)
				return remote, nil
			}
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				queries.Add(1)
				return *attempt.Operation, nil
			}
			manager.monitorEvery = time.Millisecond
			running := make(chan error, 1)
			go func() { running <- manager.Run() }()
			// Multiple monitor ticks must still leave a disabled node alone.
			time.Sleep(20 * time.Millisecond)
			if connections.Load() != 0 {
				t.Fatal("background monitor connected to a disabled node")
			}
			if err := manager.Check(t.Context(), node.ID, node.Revision); err != nil {
				t.Fatal(err)
			}
			want := int32(0)
			if pending {
				want = 1
			}
			if connections.Load() != want || queries.Load() != want {
				t.Fatal("explicit disabled check did not require an existing unresolved intent")
			}
			_ = manager.Close()
			<-running
		})
	}
}

func TestUpgradeObservationReadFailuresAndBusyKeepIntentUnchanged(t *testing.T) {
	for _, cause := range []error{os.ErrNotExist, remotessh.ErrConnection, remotessh.ErrIntegrity} {
		t.Run(cause.Error(), func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			observationIntent(t, fixture, node)
			forbidObservationWork(t, manager, remote)
			before, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			var queries atomic.Int32
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				queries.Add(1)
				return runnerinstall.UpgradeOperation{}, cause
			}
			if err := manager.Check(t.Context(), node.ID, node.Revision); !errors.Is(err, cause) {
				t.Fatalf("read error: %v", err)
			}
			if err := fixture.store.withNodeOperation(t.Context(), node.ID, func() error {
				return manager.Check(t.Context(), node.ID, node.Revision)
			}); err != nil {
				t.Fatalf("busy observer did not yield: %v", err)
			}
			after, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			if !bytes.Equal(before, after) || queries.Load() != 1 {
				t.Fatal("failed or busy observation changed the intent or retried the read")
			}
		})
	}
}

func TestUpgradeObservationRejectsForeignRequestAndBinding(t *testing.T) {
	for _, changedBinding := range []bool{false, true} {
		t.Run(map[bool]string{false: "request", true: "binding"}[changedBinding], func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			attempt := observationIntent(t, fixture, node)
			forbidObservationWork(t, manager, remote)
			before, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			var queries atomic.Int32
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				queries.Add(1)
				operation := *attempt.Operation
				operation.Request.OperationID = fixtureID(t)
				return operation, nil
			}
			want := error(remotessh.ErrIntegrity)
			if changedBinding {
				want = ErrUpgradeIdentity
				if err := fixture.store.withConfiguration(t.Context(), func(config *Configuration) error {
					config.Nodes[0].Binding.Identity.InstallationID = fixtureID(t)
					config.Nodes[0].Observation = nil
					return fixture.store.persist(t.Context(), config)
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Check(t.Context(), node.ID, node.Revision); !errors.Is(err, want) {
				t.Fatalf("identity mismatch: %v", err)
			}
			after, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			if !bytes.Equal(before, after) || changedBinding && queries.Load() != 0 {
				t.Fatal("foreign observation replaced the local intent")
			}
		})
	}
}

func TestUpgradeObservationDoesNotSwallowExplicitActions(t *testing.T) {
	for _, recoverAction := range []bool{false, true} {
		t.Run(map[bool]string{false: "retry", true: "recover"}[recoverAction], func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			attempt := observationIntent(t, fixture, node)
			manager.artifacts = nil // The confirmed stage may use its retained executable.
			entered, release := make(chan struct{}), make(chan struct{})
			var queries, actions atomic.Int32
			remote.upgradeOperation = func(ctx context.Context, _ string) (runnerinstall.UpgradeOperation, error) {
				if queries.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return runnerinstall.UpgradeOperation{}, ctx.Err()
					}
				}
				return *attempt.Operation, nil
			}
			perform := func(context.Context, runnerinstall.UpgradeRequest, remotessh.Artifact,
				func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
				actions.Add(1)
				return completedUpgrade(node, attempt.Request, fixture.now.Add(time.Second)), nil
			}
			remote.retryUpgrade, remote.recoverUpgrade = perform, perform
			observed := make(chan error, 1)
			go func() { observed <- manager.Check(t.Context(), node.ID, node.Revision) }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("read observer did not begin")
			}
			action := UpgradeAction{ID: node.ID, ExpectedRevision: node.Revision, OperationID: attempt.Request.OperationID}
			var err error
			if recoverAction {
				_, err = manager.RecoverUpgrade(t.Context(), action)
			} else {
				_, err = manager.RetryUpgrade(t.Context(), action)
			}
			if err != nil {
				t.Fatal(err)
			}
			manager.mu.Lock()
			task := manager.upgrades[node.ID]
			manager.mu.Unlock()
			if task == nil || actions.Load() != 0 {
				t.Fatal("explicit action was discarded or bypassed the observer lock")
			}
			close(release)
			if err := <-observed; err != nil {
				t.Fatal(err)
			}
			waitUpgrade(t, manager, node.ID)
			if actions.Load() != 1 || queries.Load() != 2 {
				t.Fatal("explicit same-operation action did not run after observation")
			}
		})
	}
}

func TestUpgradeObservationRejectsRevisionChangedWhileConnecting(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	observationIntent(t, fixture, node)
	forbidObservationWork(t, manager, remote)
	manager.connect = func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
		if err := fixture.store.withConfiguration(t.Context(), func(config *Configuration) error {
			config.Nodes[0].Revision++
			return fixture.store.persist(t.Context(), config)
		}); err != nil {
			t.Error(err)
		}
		return remote, nil
	}
	remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
		t.Error("stale node revision read a remote journal")
		return runnerinstall.UpgradeOperation{}, os.ErrNotExist
	}
	before, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
	if err := manager.Check(t.Context(), node.ID, node.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale observer: %v", err)
	}
	after, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
	if !bytes.Equal(before, after) {
		t.Fatal("stale observation changed the intent")
	}
}

func TestUpgradeObservationQueuedActionRejectsExpiredRevision(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	attempt := observationIntent(t, fixture, node)
	forbidObservationWork(t, manager, remote)
	manager.connect = func(context.Context, remotessh.Endpoint, remotessh.Authentication) (RemoteSession, error) {
		t.Error("expired action acquired a remote session")
		return remote, nil
	}
	err := fixture.store.withNodeOperation(t.Context(), node.ID, func() error {
		if _, err := manager.RecoverUpgrade(t.Context(), UpgradeAction{ID: node.ID,
			ExpectedRevision: node.Revision, OperationID: attempt.Request.OperationID}); err != nil {
			return err
		}
		return fixture.store.withConfiguration(t.Context(), func(config *Configuration) error {
			config.Nodes[0].Revision++
			return fixture.store.persist(t.Context(), config)
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	stored, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || stored.Request != attempt.Request || stored.Operation.Stage != attempt.Operation.Stage {
		t.Fatal("expired queued action changed operation identity or server facts")
	}
}

func TestUpgradeObservationCallerCancellationAndManagerClose(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	observationIntent(t, fixture, node)
	forbidObservationWork(t, manager, remote)
	entered, canceled := make(chan struct{}), make(chan struct{})
	remote.upgradeOperation = func(ctx context.Context, _ string) (runnerinstall.UpgradeOperation, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		return runnerinstall.UpgradeOperation{}, ctx.Err()
	}
	ctx, cancel := context.WithCancel(t.Context())
	observed := make(chan error, 1)
	go func() { observed <- manager.Check(ctx, node.ID, node.Revision) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("observer did not begin")
	}
	before, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
	cancel()
	if err := <-observed; !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation did not stop waiting")
	}
	select {
	case <-canceled:
		t.Fatal("caller cancellation stopped the manager-owned observation")
	default:
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("manager close did not join the SSH observer")
	}
	after, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
	if !bytes.Equal(before, after) || remote.closed.Load() != 1 {
		t.Fatal("closing observation changed the server facts or leaked its session")
	}
}

func TestUpgradeObservationTimeoutDoesNotReplaceConfirmedServerError(t *testing.T) {
	remote := &remoteFixture{}
	fixture, node, manager := upgradeManagerFixture(t, remote)
	attempt := observationIntent(t, fixture, node)
	manager.artifacts = nil
	server := *attempt.Operation
	server.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrBusy)
	remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
		return server, nil
	}
	remote.retryUpgrade = func(context.Context, runnerinstall.UpgradeRequest, remotessh.Artifact,
		func(runnerinstall.UpgradeProgress)) (runnerinstall.UpgradeOperation, error) {
		return server, &remotessh.UpgradeError{Pending: true, Cause: context.DeadlineExceeded}
	}
	// Ensure this is a newer authoritative journal rather than an equal-clock conflict.
	server.ChangedAt = fixture.now.Add(time.Second)
	if _, err := manager.RetryUpgrade(t.Context(), UpgradeAction{ID: node.ID,
		ExpectedRevision: node.Revision, OperationID: attempt.Request.OperationID}); err != nil {
		t.Fatal(err)
	}
	waitUpgrade(t, manager, node.ID)
	stored, err := manager.GetUpgrade(t.Context(), node.ID)
	if err != nil || stored.ErrorCode != server.ErrorCode || stored.Stage != server.Stage {
		t.Fatal("transport timeout replaced an authoritative server failure")
	}
}

func TestUpgradeObservationExplicitFaultOverridesOldServerError(t *testing.T) {
	for _, cause := range []error{remotessh.ErrIntegrity, runnerinstall.ErrIntegrity, jobcontract.ErrUnsupported} {
		t.Run(cause.Error(), func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			attempt := observationIntent(t, fixture, node)
			attempt.Operation.ErrorCode = runnerinstall.ErrorCode(runnerinstall.ErrBusy)
			attempt.ErrorCode = attempt.Operation.ErrorCode
			storeObservationIntent(t, fixture, node.ID, attempt)
			forbidObservationWork(t, manager, remote)
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				return runnerinstall.UpgradeOperation{}, cause
			}
			if _, err := manager.RetryUpgrade(t.Context(), UpgradeAction{ID: node.ID,
				ExpectedRevision: node.Revision, OperationID: attempt.Request.OperationID}); err != nil {
				t.Fatal(err)
			}
			waitUpgrade(t, manager, node.ID)
			stored, err := manager.GetUpgrade(t.Context(), node.ID)
			if err != nil || stored.ErrorCode != installationCode(cause) ||
				stored.Operation.ErrorCode != attempt.Operation.ErrorCode {
				t.Fatal("an old server error concealed a new integrity or contract fault")
			}
		})
	}
}

func TestUpgradeObservationNeverRegressesIrreversibleFacts(t *testing.T) {
	for _, stage := range []string{"resume_attempted", "rolling_back", "completed"} {
		t.Run(stage, func(t *testing.T) {
			remote := &remoteFixture{}
			fixture, node, manager := upgradeManagerFixture(t, remote)
			attempt := observationIntent(t, fixture, node)
			operation := *attempt.Operation
			operation.Stage = stage
			operation.ResumeAttempted = stage == "resume_attempted"
			if stage == "completed" {
				operation = completedUpgrade(node, attempt.Request, fixture.now)
			}
			attempt.Stage, attempt.Operation = stage, &operation
			storeObservationIntent(t, fixture, node.ID, attempt)
			before, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			remote.upgradeOperation = func(context.Context, string) (runnerinstall.UpgradeOperation, error) {
				return runnerinstall.UpgradeOperation{Request: attempt.Request, Stage: "verifying",
					Restorable: true, ChangedAt: fixture.now.Add(time.Second)}, nil
			}
			// A terminal intent uses ordinary daemon health instead of reopening its journal.
			if stage == "completed" {
				remote.bound = func(context.Context, Binding, *credentials.Lease) (jobcontract.RunnerInfo, error) {
					return *node.Observation.Runner, nil
				}
			} else {
				forbidObservationWork(t, manager, remote)
			}
			if err := manager.Check(t.Context(), node.ID, node.Revision); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(fixture.store.upgradePath(node.ID))
			if !bytes.Equal(before, after) {
				t.Fatal("observation regressed an irreversible or terminal fact")
			}
		})
	}
}

func TestUpgradeObservationPendingTimeoutClassification(t *testing.T) {
	for _, scenario := range []struct {
		name string
		err  error
		want string
	}{
		{name: "deadline", err: &remotessh.UpgradeError{Pending: true, Cause: context.DeadlineExceeded},
			want: "remote_connection_failed"},
		{name: "canceled", err: &remotessh.UpgradeError{Pending: true, Cause: context.Canceled},
			want: "remote_connection_failed"},
		{name: "not_pending", err: &remotessh.UpgradeError{Cause: context.DeadlineExceeded},
			want: "runner_installation_failed"},
		{name: "plain_deadline", err: context.DeadlineExceeded, want: "runner_installation_failed"},
		{name: "integrity", want: "remote_file_integrity_failed",
			err: &remotessh.UpgradeError{Pending: true,
				Cause: errors.Join(remotessh.ErrIntegrity, context.DeadlineExceeded)}},
		{name: "contract", want: "runner_upgrade_required",
			err: &remotessh.UpgradeError{Pending: true,
				Cause: errors.Join(jobcontract.ErrUnsupported, context.DeadlineExceeded)}},
		{name: "installation_integrity", want: "installation_integrity_failed",
			err: &remotessh.UpgradeError{Pending: true,
				Cause: errors.Join(runnerinstall.ErrIntegrity, context.Canceled)}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := installationCode(scenario.err); got != scenario.want {
				t.Fatalf("classification = %s, want %s", got, scenario.want)
			}
		})
	}
}
