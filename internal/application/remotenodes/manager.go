package remotenodes

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type ManagerDependencies struct {
	Configuration *Service
	Artifacts     ArtifactSource
	Release       string
	Connect       Connector
}

type Manager struct {
	configuration *Service
	artifacts     ArtifactSource
	release       string
	connect       Connector
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	checks        map[string]*checkTask
	workers       sync.WaitGroup
	closed        bool
	monitoring    bool
}

type checkTask struct {
	revision   uint64
	generation uint64
	cancel     context.CancelFunc
	done       chan struct{}
	err        error
}

type checkRequest struct {
	id       string
	revision uint64
	restart  bool
}

func NewManager(ctx context.Context, dependencies ManagerDependencies) (*Manager, error) {
	if ctx == nil || dependencies.Configuration == nil {
		return nil, ErrInvalid
	}
	if dependencies.Artifacts != nil && !runnerartifacts.ValidRelease(dependencies.Release) {
		return nil, ErrInvalid
	}
	connect := dependencies.Connect
	if connect == nil {
		connect = ConnectSSH
	}
	lifetime, cancel := context.WithCancel(ctx)
	return &Manager{
		configuration: dependencies.Configuration, artifacts: dependencies.Artifacts,
		release: dependencies.Release, connect: connect, ctx: lifetime, cancel: cancel,
		checks: map[string]*checkTask{},
	}, nil
}

func (manager *Manager) Save(ctx context.Context, command SaveCommand) (Node, error) {
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return Node{}, err
	}
	defer finish()
	node, err := manager.configuration.Save(ctx, command)
	if err != nil {
		return node, err
	}
	_, err = manager.schedule(ctx, checkRequest{id: node.ID, revision: node.Revision})
	return node, err
}

// Check waits for an application-owned check. Ending the observer's context
// stops only this wait; closing the manager stops its SSH activity, never jobs.
func (manager *Manager) Check(ctx context.Context, id string, expected uint64) error {
	task, err := manager.schedule(ctx, checkRequest{id: id, revision: expected})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-task.done:
		return task.err
	}
}

func (manager *Manager) node(ctx context.Context, id string, expected uint64) (Node, error) {
	if !domain.IsUUID(id) || expected == 0 {
		return Node{}, ErrInvalid
	}
	config, err := manager.configuration.store.Read(ctx)
	if err != nil {
		return Node{}, err
	}
	return requireNode(config, id, expected)
}

func (manager *Manager) schedule(ctx context.Context, request checkRequest) (*checkTask, error) {
	id, expected := request.id, request.revision
	if _, err := manager.node(ctx, id, expected); err != nil {
		return nil, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed || manager.ctx.Err() != nil {
		return nil, context.Canceled
	}
	if task := manager.checks[id]; task != nil {
		if expected < task.revision {
			return nil, ErrConflict
		}
		if expected != task.revision || request.restart {
			task.revision = expected
			task.generation++
			if task.cancel != nil {
				task.cancel()
			}
		}
		return task, nil
	}
	if len(manager.checks) >= MaxNodes {
		return nil, ErrBusy
	}
	// Only existing configuration IDs enter this map, so at most MaxNodes
	// workers exist. A queued newer revision reuses its node's existing worker.
	task := &checkTask{revision: expected, generation: 1, done: make(chan struct{})}
	manager.checks[id] = task
	manager.workers.Add(1)
	go manager.checkWorker(id, task)
	return task, nil
}

func (manager *Manager) checkWorker(id string, task *checkTask) {
	defer manager.workers.Done()
	for {
		manager.mu.Lock()
		revision := task.revision
		generation := task.generation
		ctx, cancel := context.WithTimeout(manager.ctx, 4*time.Minute)
		task.cancel = cancel
		manager.mu.Unlock()
		err := manager.check(ctx, id, revision)
		cancel()
		manager.mu.Lock()
		if !manager.closed && task.generation != generation {
			manager.mu.Unlock()
			continue
		}
		task.err = err
		delete(manager.checks, id)
		close(task.done)
		manager.mu.Unlock()
		return
	}
}

// Run performs independent per-node checks every 15 seconds. A slow or offline
// node never blocks checks of another node. Call Close and join Run at shutdown.
func (manager *Manager) Run() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return context.Canceled
	}
	if manager.monitoring {
		manager.mu.Unlock()
		return ErrConflict
	}
	manager.monitoring = true
	manager.workers.Add(1)
	manager.mu.Unlock()
	defer manager.workers.Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		config, err := manager.configuration.store.Read(manager.ctx)
		if err != nil {
			return err
		}
		for _, node := range config.Nodes {
			if !node.Enabled {
				continue
			}
			_, err := manager.schedule(manager.ctx, checkRequest{id: node.ID, revision: node.Revision})
			transient := errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound) || errors.Is(err, ErrBusy)
			if err != nil && !transient {
				return err
			}
		}
		select {
		case <-manager.ctx.Done():
			return manager.ctx.Err()
		case <-ticker.C:
		}
	}
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	manager.closed = true
	manager.cancel()
	manager.mu.Unlock()
	manager.workers.Wait()
	return nil
}

func (manager *Manager) check(ctx context.Context, id string, expected uint64) error {
	node, err := manager.node(ctx, id, expected)
	if err != nil {
		return err
	}
	if !node.Enabled {
		return nil
	}
	connection, err := manager.configuration.AcquireSSH(ctx, id, expected)
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			nil,
		)
	}
	defer connection.Close()
	node = connection.Node
	if !node.Enabled {
		return nil
	}
	session, err := manager.connect(ctx, node.Endpoint(), connection.Authentication)
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			nil,
		)
	}
	defer session.Close()
	system, err := session.Probe(ctx)
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			nil,
		)
	}
	supportedPlatform := system.OS == "linux" && (system.Arch == "amd64" || system.Arch == "arm64")
	supportedService := system.SystemdPID1 && system.SystemdVersion >= 249 && system.HasSHA256 && system.HasRunuser
	if !supportedPlatform || !supportedService {
		return manager.failed(
			ctx,
			id,
			expected,
			runnerinstall.ErrUnsupported,
			&system,
		)
	}
	node, err = manager.resolveInstallation(ctx, node, session)
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			&system,
		)
	}
	if attempt := node.Installation; attempt != nil {
		if attempt.ErrorCode != "" {
			return manager.failed(
				ctx,
				id,
				expected,
				installationCause(attempt.ErrorCode),
				&system,
			)
		}
		if attempt.Stage != "completed" {
			if err := manager.install(
				ctx,
				node,
				system,
				session,
			); err != nil {
				return manager.failed(
					ctx,
					id,
					expected,
					err,
					&system,
				)
			}
		}
	}
	if node.Binding != nil {
		token, err := manager.configuration.AcquireToken(ctx, id, expected)
		if err != nil {
			return manager.failed(
				ctx,
				id,
				expected,
				err,
				&system,
			)
		}
		defer token.Close()
		info, err := session.Bound(ctx, *node.Binding, token)
		if err != nil {
			return manager.failed(
				ctx,
				id,
				expected,
				err,
				&system,
			)
		}
		return manager.observed(
			ctx,
			node,
			system,
			info,
		)
	}
	pairing, err := session.Shared(ctx)
	if errors.Is(err, os.ErrNotExist) {
		err = manager.install(
			ctx,
			node,
			system,
			session,
		)
		if err == nil {
			pairing, err = session.Shared(ctx)
		}
	}
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			&system,
		)
	}
	defer pairing.Token.Close()
	if err := pairing.Runner.Validate(); err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			&system,
		)
	}
	validIdentity := pairing.Identity.Validate() == nil && pairing.Runner.NodeID == pairing.Identity.NodeID &&
		pairing.Runner.NodeInstallationID == pairing.Identity.InstallationID
	validPlatform := pairing.Runner.OS == system.OS && pairing.Runner.Arch == system.Arch
	if !validIdentity || !validPlatform {
		return manager.failed(
			ctx,
			id,
			expected,
			remotessh.ErrIntegrity,
			&system,
		)
	}
	node, err = manager.configuration.Bind(ctx, BindCommand{
		ID: id, ExpectedRevision: expected, Identity: pairing.Identity, Token: pairing.Token,
	})
	if err != nil {
		return manager.failed(
			ctx,
			id,
			expected,
			err,
			&system,
		)
	}
	return manager.observed(
		ctx,
		node,
		system,
		pairing.Runner,
	)
}

func (manager *Manager) observed(
	ctx context.Context,
	node Node,
	system remotessh.SystemInfo,
	info jobcontract.RunnerInfo,
) error {
	if err := info.Validate(); err != nil {
		return manager.failed(
			ctx,
			node.ID,
			node.Revision,
			err,
			&system,
		)
	}
	validIdentity := node.Binding != nil && info.NodeID == node.Binding.Identity.NodeID &&
		info.NodeInstallationID == node.Binding.Identity.InstallationID
	validPlatform := info.OS == system.OS && info.Arch == system.Arch
	if !validIdentity || !validPlatform {
		return manager.failed(
			ctx,
			node.ID,
			node.Revision,
			remotessh.ErrIntegrity,
			&system,
		)
	}
	status := "ready"
	if !info.Healthy {
		status = "unhealthy"
	}
	if info.MaintenanceOperationID != "" {
		status = "maintenance"
	}
	return manager.configuration.RecordObservation(
		ctx,
		node.ID,
		node.Revision,
		Observation{CheckedAt: manager.configuration.now().UTC(), Status: status, System: &system, Runner: &info},
	)
}

func (manager *Manager) failed(
	ctx context.Context,
	id string,
	expected uint64,
	cause error,
	system *remotessh.SystemInfo,
) error {
	if errors.Is(cause, ErrBusy) {
		return cause
	}
	observation := Observation{CheckedAt: manager.configuration.now().UTC(), Status: failureStatus(cause), System: system}
	var hostKey *remotessh.HostKeyError
	if errors.As(cause, &hostKey) {
		observation.ProposedHostKey = &HostKey{Type: hostKey.KeyType, Fingerprint: hostKey.Fingerprint}
	}
	err := manager.configuration.RecordObservation(
		ctx,
		id,
		expected,
		observation,
	)
	return errors.Join(cause, err)
}

func failureStatus(cause error) string {
	switch {
	case errors.Is(cause, remotessh.ErrHostUnverified):
		return "host_unverified"
	case errors.Is(cause, remotessh.ErrHostChanged):
		return "host_changed"
	case errors.Is(cause, remotessh.ErrAuthentication):
		return "authentication_failed"
	case errors.Is(cause, ErrCredential):
		return "credential_unavailable"
	case errors.Is(cause, runnerinstall.ErrPairingRequired):
		return "pairing_required"
	case errors.Is(cause, ErrDuplicate), errors.Is(cause, remotessh.ErrIntegrity):
		return "identity_changed"
	case errors.Is(cause, runnerinstall.ErrUnsupported), errors.Is(cause, runnerinstall.ErrRootRequired),
		errors.Is(cause, runnerinstall.ErrUpgradeRequired), errors.Is(cause, jobcontract.ErrUnsupported):
		return "unsupported"
	case errors.Is(cause, runnerartifacts.ErrUnavailable):
		return "artifact_required"
	case errors.Is(cause, runnerartifacts.ErrInvalid), errors.Is(cause, runnerartifacts.ErrUnsupported),
		errors.Is(cause, runnerartifacts.ErrIntegrity):
		return "artifact_required"
	case errors.Is(cause, remotessh.ErrConnection), errors.Is(cause, context.DeadlineExceeded):
		return "offline"
	default:
		return "failed"
	}
}
