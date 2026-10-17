package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
)

type unusedRemoteNodeUsage struct{ err error }

func (reader unusedRemoteNodeUsage) RemoteNodeRemovalBlocked(context.Context, string, string, string) (bool, error) {
	return false, reader.err
}

type remoteNodeUsageFunc func(context.Context, string, string, string) (bool, error)

func (reader remoteNodeUsageFunc) RemoteNodeRemovalBlocked(ctx context.Context, configurationID, nodeID,
	installationID string) (bool, error) {
	return reader(ctx, configurationID, nodeID, installationID)
}

func TestRemoteNodeRemovalGuardUsesCapturedIdentityAndFailsClosed(t *testing.T) {
	node := remotenodes.Node{EntityMeta: domain.EntityMeta{ID: desktopJobID}, Binding: &remotenodes.Binding{}}
	node.Binding.Identity.NodeID = "83000000-0000-4000-8000-000000000001"
	node.Binding.Identity.InstallationID = "83000000-0000-4000-8000-000000000002"
	for _, outcome := range []struct {
		name    string
		blocked bool
		cause   error
		want    error
	}{
		{name: "complete"},
		{name: "referenced", blocked: true, want: remotenodes.ErrReferenced},
		{name: "usage_unknown", cause: errors.New("database read failed"), want: remotenodes.ErrUsageUnknown},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			calls := 0
			guard := newRemoteNodeRemovalGuard(remoteNodeUsageFunc(func(ctx context.Context, configurationID,
				nodeID, installationID string) (bool, error) {
				calls++
				if configurationID != node.ID || nodeID != node.Binding.Identity.NodeID ||
					installationID != node.Binding.Identity.InstallationID {
					t.Error("removal guard changed the captured configuration or executor identity")
				}
				if _, bounded := ctx.Deadline(); !bounded {
					t.Error("usage read had no deadline")
				}
				return outcome.blocked, outcome.cause
			}))
			err := guard(t.Context(), node)
			if !errors.Is(err, outcome.want) || calls != 1 {
				t.Fatalf("removal guard result: %v", err)
			}
		})
	}
	var typedNil *sqlite.Repository
	for _, reader := range []remoteNodeUsageReader{nil, typedNil} {
		if err := newRemoteNodeRemovalGuard(reader)(t.Context(), node); !errors.Is(err, remotenodes.ErrUsageUnknown) {
			t.Fatal("absent usage reader bypassed the removal guard")
		}
	}
}

func remoteNodeUsageRepository(t *testing.T) *sqlite.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "node-usage"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	return repository
}

func remoteNodeUsageServices(t *testing.T, reader remoteNodeUsageReader) *productionRemoteNodes {
	t.Helper()
	services, closeNodes, err := newProductionRemoteNodes(t.Context(), productionRemoteNodeDependencies{
		Directory: t.TempDir(), Credentials: credentials.NewMemoryStore(), Usage: reader,
		Options: productionOptions{appVersion: "node-usage", runnerSource: &unusedRemoteArtifactSource{},
			remoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
				return nil, remotessh.ErrConnection
			}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeNodes() })
	return services
}

func TestProductionRemoteNodeRemovalAllowsUnusedConfigurationAndRetainsUnknownUsage(t *testing.T) {
	for _, bound := range []bool{false, true} {
		t.Run(map[bool]string{false: "unbound", true: "bound"}[bound], func(t *testing.T) {
			repository := remoteNodeUsageRepository(t)
			services := remoteNodeUsageServices(t, repository)
			node := saveProductionObservedNode(t, services.configuration, false)
			if !bound {
				// Make a genuinely unbound configuration through the public service.
				meta, _ := domain.NewEntityMeta(time.Now().UTC())
				password, _ := credentials.NewTemporaryLease([]byte("unused-node-test-password"))
				defer password.Close()
				var err error
				node, err = services.configuration.Save(t.Context(), remotenodes.SaveCommand{ID: meta.ID,
					Name: "unbound", Host: "unused.example.test", Port: 22, User: "root",
					Authentication: remotenodes.AuthenticationPassword, Secrets: remotenodes.SecretUpdate{Password: password}})
				if err != nil {
					t.Fatal(err)
				}
			}
			removed := make(chan error, 1)
			go func() { removed <- services.configuration.Remove(t.Context(), node.ID, node.Revision) }()
			select {
			case err := <-removed:
				if err != nil {
					t.Fatalf("unused configuration removal failed or reentered its held configuration lock: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("usage guard deadlocked under the configuration lock")
			}
		})
	}
	repository := remoteNodeUsageRepository(t)
	services := remoteNodeUsageServices(t, repository)
	node := saveProductionObservedNode(t, services.configuration, false)
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}
	if err := services.configuration.Remove(t.Context(), node.ID, node.Revision); !errors.Is(err, remotenodes.ErrUsageUnknown) {
		t.Fatal("unavailable database bypassed production removal guard")
	}
}

func TestRemoteNodeRemovalWaitsForLaunchToPersistItsPendingSubmission(t *testing.T) {
	repository := remoteNodeUsageRepository(t)
	var checks atomic.Uint32
	reader := remoteNodeUsageFunc(func(ctx context.Context, configurationID, nodeID, installationID string) (bool, error) {
		checks.Add(1)
		return repository.RemoteNodeRemovalBlocked(ctx, configurationID, nodeID, installationID)
	})
	services := remoteNodeUsageServices(t, reader)
	node := saveProductionObservedNode(t, services.configuration, false)
	record := remoteNodePendingSubmission(t, node)
	entered, release := make(chan struct{}), make(chan struct{})
	launcher := &recordingJobLauncher{launch: func(ctx context.Context) (jobs.SubmissionRecord, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return jobs.SubmissionRecord{}, ctx.Err()
		}
		prepared := record
		prepared.State, prepared.Revision, prepared.Observation = jobs.SubmissionPrepared, 1, jobs.ObservationNone
		prepared.UpdatedAt = prepared.CreatedAt
		if _, err := repository.CreateSubmission(ctx, prepared); err != nil {
			return jobs.SubmissionRecord{}, err
		}
		if err := repository.UpdateSubmission(ctx, record, prepared.Revision); err != nil {
			return jobs.SubmissionRecord{}, err
		}
		return record, jobs.ErrPending
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{remoteNodes: services.manager, launcher: launcher}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	command := desktopLaunchFixture()
	command.Location = jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: node.ID}
	launched, removed := make(chan error, 1), make(chan error, 1)
	go func() { _, err := app.LaunchJob(command); launched <- err }()
	<-entered
	go func() {
		removed <- app.RemoveRemoteNode(RemoteNodeAction{ID: node.ID, ExpectedRevision: node.Revision})
	}()
	waitRemoteNodeActiveCalls(t, app, 2)
	if checks.Load() != 0 {
		t.Fatal("removal queried usage while a launch still owned an unpersisted destination")
	}
	close(release)
	if err := <-launched; err != nil {
		t.Fatal(err)
	}
	if err := <-removed; err == nil || err.Error() != desktopCodeRemoteNodeReferenced {
		t.Fatalf("removal ignored the pending submission created by its preceding launch: %v", err)
	}
	if checks.Load() != 1 {
		t.Fatal("removal did not check the newly durable usage")
	}
}

func TestLaunchAfterRemoteNodeRemovalFailsBeforeSending(t *testing.T) {
	repository := remoteNodeUsageRepository(t)
	entered, release := make(chan struct{}), make(chan struct{})
	reader := remoteNodeUsageFunc(func(ctx context.Context, configurationID, nodeID, installationID string) (bool, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
		return repository.RemoteNodeRemovalBlocked(ctx, configurationID, nodeID, installationID)
	})
	services := remoteNodeUsageServices(t, reader)
	node := saveProductionObservedNode(t, services.configuration, false)
	var launches, sends atomic.Uint32
	launcher := &recordingJobLauncher{launch: func(ctx context.Context) (jobs.SubmissionRecord, error) {
		launches.Add(1)
		authorization, err := services.configuration.AcquireSSH(ctx, node.ID, node.Revision)
		if err != nil {
			return jobs.SubmissionRecord{}, err
		}
		defer authorization.Close()
		sends.Add(1)
		return jobs.SubmissionRecord{}, jobs.ErrUnavailable
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{remoteNodes: services.manager, launcher: launcher}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	removed, launched := make(chan error, 1), make(chan error, 1)
	go func() {
		removed <- app.RemoveRemoteNode(RemoteNodeAction{ID: node.ID, ExpectedRevision: node.Revision})
	}()
	<-entered
	command := desktopLaunchFixture()
	command.Location = jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: node.ID}
	go func() { _, err := app.LaunchJob(command); launched <- err }()
	waitRemoteNodeActiveCalls(t, app, 2)
	if launches.Load() != 0 {
		t.Fatal("launch entered preparation while configuration removal held the destination gate")
	}
	close(release)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-launched; err == nil || err.Error() != desktopCodeRemoteNodeNotFound {
		t.Fatalf("launch after removal did not report the missing configuration: %v", err)
	}
	if sends.Load() != 0 {
		t.Fatal("launch sent a request after its configuration was removed")
	}
}

type remoteNodeControlWriter struct {
	request func(context.Context, jobs.ControlInput) (jobs.ControlRecord, error)
}

func (controller remoteNodeControlWriter) Request(ctx context.Context, input jobs.ControlInput) (jobs.ControlRecord, error) {
	return controller.request(ctx, input)
}

func (controller remoteNodeControlWriter) Deliver(context.Context, string) (jobs.ControlRecord, error) {
	return jobs.ControlRecord{}, jobs.ErrUnavailable
}

func TestRemoteNodeRemovalWaitsForNewControlIntentToBeSaved(t *testing.T) {
	repository := remoteNodeUsageRepository(t)
	var checks atomic.Uint32
	reader := remoteNodeUsageFunc(func(ctx context.Context, configurationID, nodeID, installationID string) (bool, error) {
		checks.Add(1)
		return repository.RemoteNodeRemovalBlocked(ctx, configurationID, nodeID, installationID)
	})
	services := remoteNodeUsageServices(t, reader)
	node := saveProductionObservedNode(t, services.configuration, false)
	pending := remoteNodePendingSubmission(t, node)
	prepared := pending
	prepared.State, prepared.Revision, prepared.UpdatedAt = jobs.SubmissionPrepared, 1, prepared.CreatedAt
	if _, err := repository.CreateSubmission(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateSubmission(t.Context(), pending, prepared.Revision); err != nil {
		t.Fatal(err)
	}
	runner := pending.Prepared.Runner
	origin := domain.ExecutionOrigin{Kind: "remote", NodeID: runner.NodeID,
		InstallationID: runner.NodeInstallationID, EngineBuildID: runner.EngineBuildID,
		RunnerVersion: runner.RunnerVersion, OS: runner.OS, Arch: runner.Arch}
	snapshot, err := pending.Prepared.Spec.InitialSnapshot(pending.UpdatedAt, origin)
	if err != nil {
		t.Fatal(err)
	}
	accepted := pending
	accepted.State, accepted.Revision, accepted.Receipt = jobs.SubmissionAccepted, 3, &snapshot.Receipt
	accepted.UpdatedAt = accepted.UpdatedAt.Add(time.Nanosecond)
	if err := repository.UpdateSubmission(t.Context(), accepted, pending.Revision); err != nil {
		t.Fatal(err)
	}
	meta, _ := domain.NewEntityMeta(time.Now().UTC())
	target, err := (jobs.JobRead{Spec: accepted.Prepared.Spec, Submission: &accepted,
		Location: jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: node.ID}}).ControlTarget()
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	controller := remoteNodeControlWriter{request: func(ctx context.Context, input jobs.ControlInput) (jobs.ControlRecord, error) {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return jobs.ControlRecord{}, ctx.Err()
		}
		control := jobs.ControlRecord{SchemaVersion: 1, Revision: 1, State: jobs.ControlPending,
			Target:    target,
			CreatedAt: meta.CreatedAt, UpdatedAt: meta.UpdatedAt,
			Command: jobcontract.Command{Contract: jobcontract.CurrentContract, CommandID: meta.ID,
				JobID: input.JobID, Kind: input.Kind, ExpectedRevision: input.ExpectedRevision}}
		stored, err := repository.CreateControl(ctx, control)
		if err != nil {
			return jobs.ControlRecord{}, err
		}
		return stored, jobs.ErrPending
	}}
	app := newDesktopApp(func(context.Context) (desktopDependencies, error) {
		return desktopDependencies{remoteNodes: services.manager, controls: controller, controlRecords: repository}, nil
	})
	app.onStartup(t.Context())
	t.Cleanup(func() { _ = app.shutdown() })
	controlled, removed := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := app.ControlJob(jobs.ControlInput{JobID: pending.Prepared.Spec.JobID,
			Kind: jobcontract.CommandCancel, ExpectedRevision: snapshot.Receipt.Revision})
		controlled <- err
	}()
	<-entered
	go func() {
		removed <- app.RemoveRemoteNode(RemoteNodeAction{ID: node.ID, ExpectedRevision: node.Revision})
	}()
	waitRemoteNodeActiveCalls(t, app, 2)
	if checks.Load() != 0 {
		t.Fatal("removal checked usage before the new control intent finished persistence")
	}
	close(release)
	if err := <-controlled; err != nil {
		t.Fatal(err)
	}
	if control, err := repository.GetControl(t.Context(), meta.ID); err != nil || control.State != jobs.ControlPending {
		t.Fatal("pending control was not durably saved before removal checked usage")
	}
	if err := <-removed; err == nil || err.Error() != desktopCodeRemoteNodeReferenced {
		t.Fatalf("removal ignored a newly saved control intent: %v", err)
	}
}

func remoteNodePendingSubmission(t *testing.T, node remotenodes.Node) jobs.SubmissionRecord {
	t.Helper()
	record := desktopSubmissionFixture(t)
	prepared := record.Prepared
	prepared.ConfigurationID = node.ID
	prepared.Spec.Target = jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: node.Binding.Identity.NodeID}
	prepared.Spec.NodeInstallationID = node.Binding.Identity.InstallationID
	prepared.Runner.NodeID = node.Binding.Identity.NodeID
	prepared.Runner.NodeInstallationID = node.Binding.Identity.InstallationID
	current, err := jobs.NewSubmission(prepared, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	current.State, current.Revision = jobs.SubmissionPending, 2
	current.Observation, current.UpdatedAt = jobs.ObservationNone, current.UpdatedAt.Add(time.Nanosecond)
	return current
}

func waitRemoteNodeActiveCalls(t *testing.T, app *DesktopApp, want int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		app.mu.Lock()
		active := app.active
		app.mu.Unlock()
		if active >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("concurrent operations did not acquire their lifecycle leases")
		}
	}
}
