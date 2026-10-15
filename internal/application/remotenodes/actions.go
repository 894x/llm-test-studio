package remotenodes

import (
	"context"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type HostKeyConfirmation struct {
	ID               string
	ExpectedRevision uint64
	Key              HostKey
}

func (manager *Manager) SelectDefault(ctx context.Context, expected uint64, target jobcontract.ExecutionTarget) error {
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return err
	}
	defer finish()
	return manager.configuration.SelectDefault(ctx, expected, target)
}

// ConfirmHostKey acknowledges the exact, fresh key shown by the last check.
// A changed previously confirmed key requires an explicit new node definition.
func (manager *Manager) ConfirmHostKey(ctx context.Context, command HostKeyConfirmation) (Node, error) {
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return Node{}, err
	}
	defer finish()
	node, err := manager.node(ctx, command.ID, command.ExpectedRevision)
	if err != nil {
		return Node{}, err
	}
	if node.HostKey != nil || node.Observation == nil {
		return Node{}, ErrRePairRequired
	}
	observation := node.Observation
	age := manager.configuration.now().UTC().Sub(observation.CheckedAt)
	validObservation := observation.Status == "host_unverified" && observation.ProposedHostKey != nil &&
		*observation.ProposedHostKey == command.Key
	fresh := age >= 0 && age <= ObservationLifetime
	if !validObservation || !fresh {
		return Node{}, ErrConflict
	}
	return manager.Save(ctx, SaveCommand{
		ID: node.ID, ExpectedRevision: node.Revision, Name: node.Name, Region: node.Region,
		Host: node.Host, Port: node.Port, User: node.User, Enabled: node.Enabled,
		Authentication: node.Authentication.Kind, HostKey: &command.Key,
	})
}

func (manager *Manager) RetryInstallation(ctx context.Context, id string, expected uint64) error {
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return err
	}
	defer finish()
	err = manager.configuration.store.withNodeOperation(ctx, id, func() error {
		node, err := manager.node(ctx, id, expected)
		if err != nil {
			return err
		}
		if node.Installation == nil {
			return ErrNotFound
		}
		attempt := *node.Installation
		attempt.ErrorCode = ""
		return manager.recordInstallation(ctx, node, attempt)
	})
	if err != nil {
		return err
	}
	_, err = manager.schedule(ctx, checkRequest{id: id, revision: expected, restart: true})
	return err
}

func (manager *Manager) Refresh(ctx context.Context, id string, expected uint64) error {
	_, err := manager.schedule(ctx, checkRequest{id: id, revision: expected})
	return err
}

func (manager *Manager) Remove(ctx context.Context, id string, expected uint64) error {
	ctx, finish, err := manager.ownedAction(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err := manager.configuration.Remove(ctx, id, expected); err != nil {
		return err
	}
	manager.mu.Lock()
	if task := manager.checks[id]; task != nil && task.cancel != nil {
		task.cancel()
	}
	manager.mu.Unlock()
	return nil
}

func (manager *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, err := manager.configuration.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Checking = manager.checks[snapshot.Nodes[index].ID] != nil
	}
	return snapshot, nil
}

func (manager *Manager) ownedAction(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalid
	}
	manager.mu.Lock()
	if manager.closed || manager.ctx.Err() != nil {
		manager.mu.Unlock()
		return nil, nil, context.Canceled
	}
	manager.workers.Add(1)
	manager.mu.Unlock()
	owned, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(manager.ctx, cancel)
	return owned, func() { stop(); cancel(); manager.workers.Done() }, nil
}
