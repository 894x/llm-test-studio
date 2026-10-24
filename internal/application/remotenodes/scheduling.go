package remotenodes

import (
	"context"
	"errors"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remoteclient "github.com/894x/llm-test-studio/internal/remote/client"
)

type TaskConcurrencyCommand struct {
	ID                    string
	ExpectedRevision      uint64
	ExpectedMaxActiveJobs uint32
	MaxActiveJobs         uint32
}

type schedulingSession interface {
	SetTaskConcurrency(
		context.Context,
		Binding,
		*credentials.Lease,
		uint32,
		uint32,
	) (jobcontract.RunnerInfo, error)
}

func (manager *Manager) SetTaskConcurrency(ctx context.Context, command TaskConcurrencyCommand) error {
	validExpected := command.ExpectedMaxActiveJobs >= 1 && command.ExpectedMaxActiveJobs <= 128
	validLimit := command.MaxActiveJobs >= 1 && command.MaxActiveJobs <= 128
	if !validExpected || !validLimit {
		return ErrInvalid
	}
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return err
	}
	defer finish()
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return manager.configuration.store.withNodeOperation(ctx, command.ID, func() error {
		attempt, err := manager.configuration.store.readUpgrade(ctx, command.ID)
		if err != nil {
			return err
		}
		if unresolvedUpgrade(attempt) {
			return ErrBusy
		}
		connection, err := manager.configuration.AcquireSSH(ctx, command.ID, command.ExpectedRevision)
		if err != nil {
			return err
		}
		defer connection.Close()
		node := connection.Node
		unbound := node.HostKey == nil || node.Binding == nil
		if !node.Enabled || unbound || unresolvedInstallation(node.Installation) {
			return ErrRePairRequired
		}
		token, err := manager.configuration.AcquireToken(ctx, node.ID, node.Revision)
		if err != nil {
			return err
		}
		defer token.Close()
		session, err := manager.connect(ctx, node.Endpoint(), connection.Authentication)
		if err != nil {
			return err
		}
		defer session.Close()
		setter, supported := session.(schedulingSession)
		if !supported {
			return ErrUnsupported
		}
		system, err := session.Probe(ctx)
		if err != nil {
			return err
		}
		info, writeErr := setter.SetTaskConcurrency(
			ctx,
			*node.Binding,
			token,
			command.ExpectedMaxActiveJobs,
			command.MaxActiveJobs,
		)
		// A check already in flight may still carry the old budget. Stop its
		// observation before publishing the setting confirmed by this operation.
		manager.mu.Lock()
		if task := manager.checks[node.ID]; task != nil && task.cancel != nil {
			task.cancel()
		}
		manager.mu.Unlock()
		if writeErr != nil {
			// The remote write may have committed before its response was lost.
			// Read back the actual state, without automatically repeating the write.
			if actual, readErr := session.Bound(ctx, *node.Binding, token); readErr == nil {
				_ = manager.observed(ctx, node, system, actual)
			}
			return schedulingError(writeErr)
		}
		return manager.observed(ctx, node, system, info)
	})
}

func schedulingError(err error) error {
	if errors.Is(err, jobcontract.ErrUnsupported) {
		return ErrUnsupported
	}
	var remote *remoteclient.RemoteError
	if errors.As(err, &remote) {
		switch remote.Code {
		case "job_conflict":
			return ErrConflict
		case "capacity_exceeded", "runner_maintenance":
			return ErrBusy
		case "unsupported_contract", "not_found":
			return ErrUnsupported
		case "invalid_request":
			return ErrInvalid
		}
	}
	return ErrCommitUnconfirmed
}

func (session *sshSession) SetTaskConcurrency(
	ctx context.Context,
	binding Binding,
	token *credentials.Lease,
	expected uint32,
	limit uint32,
) (jobcontract.RunnerInfo, error) {
	node, _, err := session.client.ConnectBoundNode(ctx, binding.Identity, token)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	defer node.Close()
	client, err := remoteclient.New(remoteclient.Identity{
		NodeID: binding.Identity.NodeID, InstallationID: binding.Identity.InstallationID,
	}, node.Do)
	if err != nil {
		return jobcontract.RunnerInfo{}, err
	}
	return client.SetTaskConcurrency(ctx, expected, limit)
}
