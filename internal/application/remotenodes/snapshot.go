package remotenodes

import (
	"context"
	"time"
)

const ObservationLifetime = 45 * time.Second

// Snapshot exposes no credential references, token hashes, suffixes or key text.
// A persisted observation is advisory; submission still requires a fresh check.
type Snapshot struct {
	Revision                 uint64     `json:"revision"`
	Default                  Selection  `json:"default"`
	Nodes                    []NodeView `json:"nodes"`
	PendingCredentialCleanup int        `json:"pending_credential_cleanup"`
}

type NodeView struct {
	ID                 string             `json:"id"`
	Revision           uint64             `json:"revision"`
	Name               string             `json:"name"`
	Region             string             `json:"region"`
	Host               string             `json:"host"`
	Port               uint16             `json:"port"`
	User               string             `json:"user"`
	Enabled            bool               `json:"enabled"`
	AuthenticationKind AuthenticationKind `json:"authentication_kind"`
	HasAuthentication  bool               `json:"has_authentication"`
	HasPassphrase      bool               `json:"has_passphrase"`
	HostKey            *HostKey           `json:"host_key"`
	RunnerNodeID       string             `json:"runner_node_id"`
	InstallationID     string             `json:"installation_id"`
	Installation       *Installation      `json:"installation"`
	Upgrade            *Upgrade           `json:"upgrade"`
	Observation        *Observation       `json:"observation"`
	Status             string             `json:"status"`
	Eligible           bool               `json:"eligible"`
	Checking           bool               `json:"checking"`
}

func (service *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	config, err := service.store.Read(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	result := Snapshot{Revision: config.Revision, Default: config.Default, Nodes: []NodeView{}}
	now := service.now().UTC()
	for _, node := range config.Nodes {
		upgrade, err := service.store.readUpgrade(ctx, node.ID)
		if err != nil {
			return Snapshot{}, err
		}
		view := NodeView{
			ID: node.ID, Revision: node.Revision, Name: node.Name, Region: node.Region,
			Host: node.Host, Port: node.Port, User: node.User, Enabled: node.Enabled,
			AuthenticationKind: node.Authentication.Kind, HasAuthentication: len(node.Authentication.refs()) != 0,
			HasPassphrase: node.Authentication.PassphraseRef != "", HostKey: node.HostKey,
			Installation: node.Installation, Upgrade: upgrade, Observation: node.Observation, Status: "unchecked",
		}
		if node.Binding != nil {
			view.RunnerNodeID, view.InstallationID = node.Binding.Identity.NodeID, node.Binding.Identity.InstallationID
		}
		if observation := node.Observation; observation != nil {
			view.Status = observation.Status
			age := now.Sub(observation.CheckedAt)
			fresh := age >= 0 && age <= ObservationLifetime
			if !fresh {
				view.Status = "offline"
			}
			view.Eligible = fresh && observation.Status == "ready"
		}
		if !node.Enabled {
			view.Status, view.Eligible = "disabled", false
		}
		if unresolvedUpgrade(upgrade) {
			view.Status, view.Eligible = "maintenance", false
		}
		result.Nodes = append(result.Nodes, view)
	}
	active := activeReferences(config)
	for _, ref := range config.CredentialRefs {
		if !active[ref] {
			result.PendingCredentialCleanup++
		}
	}
	return result, nil
}

func (service *Service) RecordObservation(
	ctx context.Context,
	id string,
	expected uint64,
	observation Observation,
) error {
	return service.store.withConfiguration(ctx, func(config *Configuration) error {
		node, err := requireNode(*config, id, expected)
		if err != nil {
			return err
		}
		if node.Observation != nil && observation.CheckedAt.Before(node.Observation.CheckedAt) {
			return ErrConflict
		}
		node.Observation = &observation
		config.Nodes[nodeIndex(*config, id)] = node
		return service.store.persist(ctx, config)
	})
}

type InstallationCommand struct {
	ID                  string
	ExpectedRevision    uint64
	ExpectedOperationID string
	Installation        Installation
}

func (service *Service) RecordInstallation(ctx context.Context, command InstallationCommand) error {
	return service.store.withConfiguration(ctx, func(config *Configuration) error {
		node, err := requireNode(*config, command.ID, command.ExpectedRevision)
		if err != nil {
			return err
		}
		operationID := ""
		if node.Installation != nil {
			operationID = node.Installation.Request.OperationID
		}
		if operationID != command.ExpectedOperationID {
			return ErrConflict
		}
		if node.Installation != nil {
			sameRequest := node.Installation.Request == command.Installation.Request
			sameDirectory := node.Installation.TemporaryDirectory == command.Installation.TemporaryDirectory
			if !sameRequest || !sameDirectory {
				return ErrConflict
			}
		}
		node.Installation = &command.Installation
		config.Nodes[nodeIndex(*config, command.ID)] = node
		return service.store.persist(ctx, config)
	})
}
