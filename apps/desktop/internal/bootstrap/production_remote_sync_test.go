package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type productionObservation struct {
	RunnerConnection
	origin remoteresults.Origin
	listed chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (connection *productionObservation) Probe(context.Context) (jobcontract.RunnerInfo, error) {
	return jobcontract.RunnerInfo{NodeID: connection.origin.NodeID,
		NodeInstallationID: connection.origin.InstallationID}, nil
}

func (connection *productionObservation) ListJobs(
	ctx context.Context,
	_ uint64,
	_ uint32,
) (jobhost.JobPage, error) {
	close(connection.listed)
	<-ctx.Done()
	return jobhost.JobPage{}, ctx.Err()
}

func (connection *productionObservation) Close() error {
	connection.once.Do(func() { close(connection.closed) })
	return nil
}

func saveProductionObservedNode(t *testing.T, configuration *remotenodes.Service, enabled bool) remotenodes.Node {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	password, err := credentials.NewTemporaryLease([]byte("production-observer-test-password"))
	if err != nil {
		t.Fatal(err)
	}
	defer password.Close()
	node, err := configuration.Save(t.Context(), remotenodes.SaveCommand{
		ID: meta.ID, Name: "observer fixture", Host: "runner.example.test", Port: 22, User: "root", Enabled: enabled,
		Authentication: remotenodes.AuthenticationPassword, Secrets: remotenodes.SecretUpdate{Password: password},
		HostKey: &remotenodes.HostKey{Type: "ssh-ed25519", Fingerprint: "SHA256:" + strings.Repeat("A", 43)},
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeMeta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	installationMeta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	value := bytes.Repeat([]byte("token"), 10)
	digest := sha256.Sum256(value)
	token, err := credentials.NewTemporaryLease(value)
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	node, err = configuration.Bind(t.Context(), remotenodes.BindCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Token: token,
		Identity: runnerinstall.Identity{Contract: jobcontract.CurrentContract, NodeID: nodeMeta.ID,
			InstallationID: installationMeta.ID, ListenPort: 44917, TokenSHA256: hex.EncodeToString(digest[:])},
	})
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestProductionSynchronizationReopensSavedAuthorizationAndJoinsBeforeDatabaseClose(t *testing.T) {
	root, base := t.TempDir(), credentials.NewMemoryStore()
	options := Options{AppVersion: "v-test", RunnerSource: &unusedRemoteArtifactSource{},
		RemoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
			return nil, remotessh.ErrConnection
		}}
	services, closeNodes, err := NewProductionRemoteNodes(
		t.Context(),
		ProductionRemoteNodeDependencies{Directory: root, Credentials: base, Usage: unusedRemoteNodeUsage{}, Options: options},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = closeNodes() })
	node := saveProductionObservedNode(t, services.configuration, true)
	disabled := saveProductionObservedNode(t, services.configuration, false)
	path := filepath.Join(root, "observation.db")
	if err := sqlite.Migrate(t.Context(), path, sqlite.MigrateOptions{AppVersion: "observer-wiring"}); err != nil {
		t.Fatal(err)
	}
	repository, err := sqlite.OpenRepository(t.Context(), path, sqlite.RepositoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.Close()
	for pass := range 2 {
		observations := map[string]*productionObservation{}
		for _, configured := range []remotenodes.Node{node, disabled} {
			observations[configured.ID] = &productionObservation{listed: make(chan struct{}), closed: make(chan struct{})}
		}
		watcher, closeSync, err := newProductionRemoteSynchronization(t.Context(), remoteSyncDependencies{
			Directory: root, Repository: repository, Configuration: services.configuration,
			Connect: func(
				ctx context.Context,
				configuration *remotenodes.Service,
				requested remotesync.Node,
				access jobs.Access,
			) (RunnerConnection, error) {
				observation := observations[requested.Origin.ConfigurationID]
				if observation == nil || access != jobs.ObservationAccess {
					return nil, errors.New("source included an unexpected node or requested intake access")
				}
				auth, err := configuration.AcquireSSH(ctx, requested.Origin.ConfigurationID, requested.Revision)
				if err != nil {
					return nil, err
				}
				defer auth.Close()
				token, err := configuration.AcquireToken(ctx, requested.Origin.ConfigurationID, requested.Revision)
				if err != nil {
					return nil, err
				}
				defer token.Close()
				observation.origin = requested.Origin
				return observation, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = closeSync() })
		for _, observation := range observations {
			select {
			case <-observation.listed:
			case <-time.After(2 * time.Second):
				t.Fatal("production observer did not read both saved paired nodes")
			}
		}
		if len(watcher.Snapshot().Nodes) != 2 {
			t.Fatal("disabling intake stopped an existing paired node's observation")
		}
		if err := closeSync(); err != nil {
			t.Fatal(err)
		}
		if err := closeSync(); err != nil {
			t.Fatal("production observer closer is not idempotent")
		}
		for _, observation := range observations {
			select {
			case <-observation.closed:
			default:
				t.Fatal("production closer returned before its connection closed")
			}
		}
		if _, err := repository.ListQuickPerformanceReportSummaries(t.Context()); err != nil {
			t.Fatal("observer closed the shared database")
		}
		if pass == 0 {
			if err := closeNodes(); err != nil {
				t.Fatal(err)
			}
			services, closeNodes, err = NewProductionRemoteNodes(
				t.Context(),
				ProductionRemoteNodeDependencies{Directory: root, Credentials: base, Usage: repository, Options: options},
			)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestProductionRunnerOpeningRejectsChangedIdentityBeforeSSH(t *testing.T) {
	services, closeNodes, err := NewProductionRemoteNodes(
		t.Context(),
		ProductionRemoteNodeDependencies{Directory: t.TempDir(), Credentials: credentials.NewMemoryStore(),
			Usage: unusedRemoteNodeUsage{}, Options: Options{AppVersion: "v-test", RunnerSource: &unusedRemoteArtifactSource{},
				RemoteConnector: func(context.Context, remotessh.Endpoint, remotessh.Authentication) (remotenodes.RemoteSession, error) {
					return nil, remotessh.ErrConnection
				}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer closeNodes()
	node := saveProductionObservedNode(t, services.configuration, true)
	requested := remotesync.Node{Revision: node.Revision, Origin: remoteresults.Origin{
		ConfigurationID: node.ID, NodeID: node.Binding.Identity.NodeID, InstallationID: node.Binding.Identity.InstallationID,
	}}
	requested.Origin.NodeID = "11111111-1111-4111-8111-111111111111"
	if _, err := OpenProductionRunner(
		t.Context(),
		services.configuration,
		requested,
		jobs.ObservationAccess,
	); !errors.Is(err, jobs.ErrTargetChanged) {
		t.Fatalf("changed identity reached SSH: %v", err)
	}
}
