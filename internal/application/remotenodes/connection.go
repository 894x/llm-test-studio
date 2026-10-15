package remotenodes

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

type Connection struct {
	Node           Node
	Authentication remotessh.Authentication
	leases         []*credentials.Lease
}

func (*Connection) String() string               { return "remote_node_connection{redacted}" }
func (*Connection) GoString() string             { return "remote_node_connection{redacted}" }
func (*Connection) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

func (connection *Connection) Close() error {
	if connection != nil {
		for _, lease := range connection.leases {
			_ = lease.Close()
		}
	}
	return nil
}

// AcquireSSH freezes one node revision and copies its credentials under the
// config lock. Later edits or cleanup cannot alter the already issued leases.
func (service *Service) AcquireSSH(ctx context.Context, id string, expected uint64) (*Connection, error) {
	connection := &Connection{leases: []*credentials.Lease{}}
	err := service.store.withConfiguration(ctx, func(config *Configuration) error {
		node, err := requireNode(*config, id, expected)
		if err != nil {
			return err
		}
		connection.Node = node
		leases := map[string]*credentials.Lease{}
		for _, value := range node.Authentication.refs() {
			ref, err := credentials.ParseStoreRef(value)
			if err != nil {
				return ErrInvalid
			}
			lease, err := service.credentials.Get(ctx, ref)
			if err != nil {
				return credentialFailure(ctx)
			}
			connection.leases = append(connection.leases, lease)
			leases[value] = lease
		}
		switch node.Authentication.Kind {
		case AuthenticationPassword:
			connection.Authentication = remotessh.PasswordAuthentication(leases[node.Authentication.PasswordRef])
		case AuthenticationPrivateKey:
			connection.Authentication = remotessh.PrivateKeyAuthentication(
				leases[node.Authentication.PrivateKeyRef], leases[node.Authentication.PassphraseRef],
			)
		}
		return nil
	})
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return connection, nil
}

func (service *Service) AcquireToken(ctx context.Context, id string, expected uint64) (*credentials.Lease, error) {
	var result *credentials.Lease
	err := service.store.withConfiguration(ctx, func(config *Configuration) error {
		node, err := requireNode(*config, id, expected)
		if err != nil {
			return err
		}
		if node.Binding == nil {
			return ErrRePairRequired
		}
		ref, err := credentials.ParseStoreRef(node.Binding.TokenRef)
		if err != nil {
			return ErrInvalid
		}
		result, err = service.credentials.Get(ctx, ref)
		if err != nil {
			return credentialFailure(ctx)
		}
		token, err := result.Bytes()
		defer clear(token)
		if err != nil || !tokenMatches(token, node.Binding.Identity) {
			return ErrCredential
		}
		return nil
	})
	if err != nil {
		_ = result.Close()
		return nil, err
	}
	return result, nil
}

type BindCommand struct {
	ID               string
	ExpectedRevision uint64
	Identity         runnerinstall.Identity
	Token            *credentials.Lease
}

func (BindCommand) String() string               { return "remote_node_binding{redacted}" }
func (BindCommand) GoString() string             { return "remote_node_binding{redacted}" }
func (BindCommand) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

func (service *Service) Bind(ctx context.Context, command BindCommand) (Node, error) {
	id, expected, identity, lease := command.ID, command.ExpectedRevision, command.Identity, command.Token
	if identity.Validate() != nil || lease == nil {
		return Node{}, ErrInvalid
	}
	token, err := lease.Bytes()
	if err != nil {
		return Node{}, ErrCredential
	}
	defer clear(token)
	if !tokenMatches(token, identity) {
		return Node{}, ErrInvalid
	}
	var result Node
	err = service.store.withConfiguration(ctx, func(config *Configuration) error {
		node, err := requireNode(*config, id, expected)
		if err != nil {
			return err
		}
		if node.HostKey == nil {
			return ErrRePairRequired
		}
		if node.Binding != nil && node.Binding.Identity != identity {
			return ErrRePairRequired
		}
		for _, other := range config.Nodes {
			otherBinding := other.ID != id && other.Binding != nil
			duplicate := otherBinding && other.Binding.Identity.InstallationID == identity.InstallationID
			if duplicate {
				return ErrDuplicate
			}
		}
		meta, err := node.NextRevision(service.now())
		if err != nil {
			return ErrInvalid
		}
		credentialMeta, err := domain.NewEntityMeta(service.now())
		if err != nil {
			return err
		}
		ref, err := credentials.NewStoreRef(domain.CredentialRunnerToken, credentialMeta.ID)
		if err != nil {
			return ErrInvalid
		}
		pending := []pendingCredential{{ref: ref, value: token}}
		if err := service.installCredentials(ctx, config, pending); err != nil {
			return err
		}
		node.EntityMeta, node.Binding, node.Observation = meta, &Binding{Identity: identity, TokenRef: ref.Value()}, nil
		config.Nodes[nodeIndex(*config, id)], result = node, node
		return service.store.persist(ctx, config)
	})
	return result, err
}

func tokenMatches(token []byte, identity runnerinstall.Identity) bool {
	validSize := len(token) >= 32 && len(token) <= 128
	digest := sha256.Sum256(token)
	encoded := hex.EncodeToString(digest[:])
	return validSize && subtle.ConstantTimeCompare([]byte(encoded), []byte(identity.TokenSHA256)) == 1
}

func credentialFailure(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrCredential
}

func requireNode(config Configuration, id string, expected uint64) (Node, error) {
	index := nodeIndex(config, id)
	if index < 0 {
		return Node{}, ErrNotFound
	}
	node := config.Nodes[index]
	if expected == 0 || node.Revision != expected {
		return Node{}, ErrConflict
	}
	return node, nil
}
