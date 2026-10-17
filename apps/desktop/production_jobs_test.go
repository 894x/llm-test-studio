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
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	runnerclient "github.com/894x/llm-test-studio/internal/remote/client"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestProductionJobBackendObservesDisabledNodesAndNeverFallsBack(t *testing.T) {
	services, closeNodes, err := newProductionRemoteNodes(
		t.Context(),
		productionRemoteNodeDependencies{Directory: t.TempDir(), Credentials: credentials.NewMemoryStore(),
			Usage: unusedRemoteNodeUsage{}, Options: productionOptions{appVersion: "v-test", runnerSource: &unusedRemoteArtifactSource{},
				remoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
					return nil, remotessh.ErrConnection
				}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNodes()
	node := saveProductionObservedNode(t, services.configuration, false)
	var remoteCalls, localCalls atomic.Int64
	connection := &productionObservation{closed: make(chan struct{})}
	backends := productionJobBackends{
		configuration: services.configuration,
		local: func(context.Context) (jobs.Backend, error) {
			localCalls.Add(1)
			return nil, jobs.ErrUnavailable
		},
		connect: func(
			ctx context.Context,
			configuration *remotenodes.Service,
			requested remotesync.Node,
			access jobs.Access,
		) (runnerConnection, error) {
			remoteCalls.Add(1)
			expected := remoteresults.Origin{ConfigurationID: node.ID,
				NodeID: node.Binding.Identity.NodeID, InstallationID: node.Binding.Identity.InstallationID}
			if requested.Origin != expected || requested.Revision != node.Revision || access != jobs.ObservationAccess {
				return nil, jobs.ErrTargetChanged
			}
			auth, err := configuration.AcquireSSH(ctx, node.ID, node.Revision)
			if err != nil {
				return nil, err
			}
			defer auth.Close()
			token, err := configuration.AcquireToken(ctx, node.ID, node.Revision)
			if err != nil {
				return nil, err
			}
			defer token.Close()
			return connection, nil
		},
	}
	location := jobs.Location{Kind: jobcontract.TargetRemote, ConfigurationID: node.ID}
	if _, err := backends.Open(t.Context(), location, jobs.SubmissionAccess); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("disabled node allowed a new submission")
	}
	if remoteCalls.Load() != 0 || localCalls.Load() != 0 {
		t.Fatal("disabled remote opened a connection or fell back to local")
	}
	opened, err := backends.Open(t.Context(), location, jobs.ObservationAccess)
	if err != nil || opened != connection {
		t.Fatalf("disabled node observation: %v", err)
	}
	if err := opened.Close(); err != nil {
		t.Fatal(err)
	}
	requested := remotesync.Node{Revision: node.Revision, Origin: remoteresults.Origin{
		ConfigurationID: node.ID, NodeID: node.Binding.Identity.NodeID, InstallationID: node.Binding.Identity.InstallationID,
	}}
	if _, err := openProductionRunner(
		t.Context(),
		services.configuration,
		requested,
		jobs.SubmissionAccess,
	); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal("SSH opener bypassed the disabled-intake policy")
	}
	backends.connect = func(
		context.Context,
		*remotenodes.Service,
		remotesync.Node,
		jobs.Access,
	) (runnerConnection, error) {
		return nil, jobs.ErrUnavailable
	}
	if _, err := backends.Open(t.Context(), location, jobs.ObservationAccess); !errors.Is(err, jobs.ErrUnavailable) {
		t.Fatal(err)
	}
	if localCalls.Load() != 0 {
		t.Fatal("unavailable remote fell back to local")
	}
	if _, err := backends.Open(t.Context(), jobs.Location{}, jobs.SubmissionAccess); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal("missing execution location was defaulted")
	}
	if _, err := backends.Open(t.Context(), location, ""); !errors.Is(err, jobs.ErrInvalid) {
		t.Fatal("missing access policy was defaulted")
	}
}

func TestProductionJobsOwnsConfirmationWorkerButNotSharedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "desktop.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "production-jobs"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	services, err := newProductionJobs(t.Context(), repository, productionJobBackends{})
	if err != nil {
		t.Fatal(err)
	}
	defer services.close()
	watcher := services.confirmations
	validCommands := services.coordinator != nil && services.controller != nil
	validWorkers := watcher != nil && services.deliveries != nil
	if !validCommands || !validWorkers {
		t.Fatal("production submission services were not wired")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	for watcher.Snapshot().CheckedAt.IsZero() || services.deliveries.Snapshot().CheckedAt.IsZero() {
		select {
		case <-ctx.Done():
			t.Fatal("production startup did not query pending submissions")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !watcher.Snapshot().Running {
		t.Fatal("confirmation worker stopped before shutdown")
	}
	for range 2 {
		if err := services.close(); err != nil {
			t.Fatal(err)
		}
	}
	if watcher.Snapshot().Running {
		t.Fatal("production closer did not join the confirmation worker")
	}
	if services.deliveries.Snapshot().Running {
		t.Fatal("production closer did not join the control delivery worker")
	}
	if _, err := repository.ListPendingSubmissions(t.Context(), "", 1); err != nil {
		t.Fatal("submission closer closed the shared database")
	}
}

func TestProductionRunnerErrorsRetainCurrentUpgradeAndNotFoundMeaning(t *testing.T) {
	for _, source := range []error{
		runnerinstall.ErrUpgradeRequired, jobcontract.ErrUnsupported,
		&runnerclient.RemoteError{Code: "unsupported_contract"}, &runnerclient.RemoteError{Code: "capability_missing"},
	} {
		if !errors.Is(runnerBackendError(source), jobs.ErrUpgradeRequired) {
			t.Fatal("current protocol or capability failure lost its upgrade requirement")
		}
	}
	if !errors.Is(runnerBackendError(&runnerclient.RemoteError{Code: "not_found"}), jobs.ErrNotFound) {
		t.Fatal("authoritative 404 lost its observation meaning")
	}
	if !errors.Is(runnerBackendError(&runnerclient.RemoteError{Code: "job_conflict"}), jobs.ErrConflict) {
		t.Fatal("authoritative control refusal lost its conflict meaning")
	}
	source := &runnerclient.RemoteError{Code: "unauthorized"}
	if runnerBackendError(source) != source || runnerBackendError(nil) != nil {
		t.Fatal("unrelated errors or success were reinterpreted")
	}
}
