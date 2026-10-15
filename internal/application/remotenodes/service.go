package remotenodes

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type Dependencies struct {
	Store       *FileStore
	Credentials credentials.Store
	Now         func() time.Time
	// Must consult accepted, uncertain and undownloaded local job ownership.
	// Absence fails closed; removing a node never uninstalls its server runner.
	CanRemove func(context.Context, Node) error
}

type Service struct {
	store       *FileStore
	credentials credentials.Store
	now         func() time.Time
	canRemove   func(context.Context, Node) error
}

type SecretUpdate struct {
	Password         *credentials.Lease
	PrivateKey       *credentials.Lease
	Passphrase       *credentials.Lease
	RemovePassphrase bool
}

func (SecretUpdate) String() string               { return "remote_node_secrets{redacted}" }
func (SecretUpdate) GoString() string             { return "remote_node_secrets{redacted}" }
func (SecretUpdate) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

type SaveCommand struct {
	ID               string
	ExpectedRevision uint64
	Name             string
	Region           string
	Host             string
	Port             uint16
	User             string
	Enabled          bool
	Authentication   AuthenticationKind
	HostKey          *HostKey
	Secrets          SecretUpdate
}

func (SaveCommand) String() string               { return "remote_node_save{redacted}" }
func (SaveCommand) GoString() string             { return "remote_node_save{redacted}" }
func (SaveCommand) MarshalJSON() ([]byte, error) { return nil, ErrInvalid }

func New(dependencies Dependencies) (*Service, error) {
	if dependencies.Store == nil || dependencies.Credentials == nil {
		return nil, ErrInvalid
	}
	clock := dependencies.Now
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Service{store: dependencies.Store, credentials: dependencies.Credentials,
		now: clock, canRemove: dependencies.CanRemove}, nil
}

// Save writes a durable credential intent before touching the keyring, then
// commits the node reference. Garbage collection is a separate guarded action.
func (service *Service) Save(ctx context.Context, command SaveCommand) (Node, error) {
	if !domain.IsUUID(command.ID) {
		return Node{}, ErrInvalid
	}
	var result Node
	err := service.store.withConfiguration(ctx, func(config *Configuration) error {
		index := nodeIndex(*config, command.ID)
		current := Node{}
		if index >= 0 {
			current = config.Nodes[index]
		}
		creating := index < 0 && command.ExpectedRevision == 0
		updating := index >= 0 && current.Revision == command.ExpectedRevision
		if !creating && !updating {
			return ErrConflict
		}
		if creating && len(config.Nodes) >= MaxNodes {
			return ErrInvalid
		}
		node := current
		if creating {
			meta, err := domain.NewEntityMeta(service.now())
			if err != nil {
				return ErrInvalid
			}
			meta.ID = command.ID
			node.EntityMeta = meta
		} else {
			meta, err := current.NextRevision(service.now())
			if err != nil {
				return ErrInvalid
			}
			node.EntityMeta = meta
		}
		if current.HostKey != nil && !reflect.DeepEqual(current.HostKey, command.HostKey) {
			return ErrRePairRequired
		}
		connectionChanged := current.Host != command.Host || current.Port != command.Port || current.User != command.User
		if current.Binding != nil && connectionChanged {
			return ErrRePairRequired
		}
		node.Name, node.Region, node.Host = command.Name, command.Region, command.Host
		node.Port, node.User, node.Enabled, node.HostKey = command.Port, command.User, command.Enabled, command.HostKey
		node.Observation = nil
		auth, pending, err := service.prepareAuthentication(current.Authentication, command)
		if err != nil {
			return err
		}
		defer closePending(pending)
		node.Authentication = auth
		if err := node.Validate(); err != nil {
			return err
		}
		if err := service.installCredentials(ctx, config, pending); err != nil {
			return err
		}
		if creating {
			config.Nodes = append(config.Nodes, node)
		} else {
			config.Nodes[index] = node
		}
		result = node
		return service.store.persist(ctx, config)
	})
	return result, err
}

func (service *Service) SelectDefault(ctx context.Context, expected uint64, target jobcontract.ExecutionTarget) error {
	if target.Validate() != nil || expected == 0 {
		return ErrInvalid
	}
	return service.store.withConfiguration(ctx, func(config *Configuration) error {
		if config.Default.Revision != expected {
			return ErrConflict
		}
		if target.Kind == jobcontract.TargetRemote && nodeIndex(*config, target.NodeID) < 0 {
			return ErrNotFound
		}
		config.Default = Selection{Revision: expected + 1, Kind: target.Kind, NodeID: target.NodeID}
		return service.store.persist(ctx, config)
	})
}

func (service *Service) Remove(ctx context.Context, id string, expected uint64) error {
	if !domain.IsUUID(id) || expected == 0 {
		return ErrInvalid
	}
	return service.store.withConfiguration(ctx, func(config *Configuration) error {
		index := nodeIndex(*config, id)
		if index < 0 {
			return ErrNotFound
		}
		node := config.Nodes[index]
		if expected == 0 || node.Revision != expected {
			return ErrConflict
		}
		if config.Default.Kind == jobcontract.TargetRemote && config.Default.NodeID == id {
			return ErrReferenced
		}
		if service.canRemove == nil {
			return ErrUsageUnknown
		}
		if err := service.canRemove(ctx, node); err != nil {
			return err
		}
		config.Nodes = slices.Delete(config.Nodes, index, index+1)
		return service.store.persist(ctx, config)
	})
}

// Cleanup holds the node mutation lock until unreferenced keyring entries have
// been deleted and their registry records removed. It first confirms that the
// entire reference set is durable, including after a prior ambiguous save.
func (service *Service) Cleanup(ctx context.Context) error {
	return service.store.withConfiguration(ctx, func(config *Configuration) error {
		active := activeReferences(*config)
		unused := []string{}
		for _, ref := range config.CredentialRefs {
			if !active[ref] {
				unused = append(unused, ref)
			}
		}
		if len(unused) == 0 {
			return nil
		}
		if err := service.store.write(ctx, *config); err != nil {
			return err
		}
		removed := map[string]bool{}
		failures := []error{}
		for _, value := range unused {
			ref, err := credentials.ParseStoreRef(value)
			if err != nil {
				return ErrInvalid
			}
			err = service.credentials.Delete(ctx, ref)
			if err != nil && !errors.Is(err, credentials.ErrNotFound) {
				failures = append(failures, ErrCredential)
				continue
			}
			removed[value] = true
		}
		if len(removed) != 0 {
			config.CredentialRefs = slices.DeleteFunc(config.CredentialRefs, func(ref string) bool { return removed[ref] })
			if err := service.store.persist(ctx, config); err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	})
}

type pendingCredential struct {
	ref   credentials.StoreRef
	value []byte
}

func closePending(pending []pendingCredential) {
	for _, value := range pending {
		clear(value.value)
	}
}

func (service *Service) prepareAuthentication(
	current Authentication,
	command SaveCommand,
) (Authentication, []pendingCredential, error) {
	update := command.Secrets
	pending := []pendingCredential{}
	auth := Authentication{Kind: command.Authentication}
	if current.Kind == command.Authentication {
		auth = current
	}
	add := func(lease *credentials.Lease, purpose domain.CredentialPurpose, limit int, destination *string) error {
		if lease == nil {
			return nil
		}
		value, err := lease.Bytes()
		validValue := len(value) > 0 && len(value) <= limit
		if err != nil || !validValue {
			clear(value)
			return ErrInvalid
		}
		meta, err := domain.NewEntityMeta(service.now())
		if err != nil {
			clear(value)
			return ErrInvalid
		}
		ref, err := credentials.NewStoreRef(purpose, meta.ID)
		if err != nil {
			clear(value)
			return ErrInvalid
		}
		pending = append(pending, pendingCredential{ref: ref, value: value})
		*destination = ref.Value()
		return nil
	}
	var err error
	switch command.Authentication {
	case AuthenticationPassword:
		invalid := update.PrivateKey != nil || update.Passphrase != nil || update.RemovePassphrase
		if invalid {
			return auth, pending, ErrInvalid
		}
		err = add(
			update.Password,
			domain.CredentialSSHPassword,
			8192,
			&auth.PasswordRef,
		)
	case AuthenticationPrivateKey:
		invalid := update.Password != nil || (update.Passphrase != nil && update.RemovePassphrase)
		if invalid {
			return auth, pending, ErrInvalid
		}
		err = add(
			update.PrivateKey,
			domain.CredentialSSHPrivateKey,
			1<<20,
			&auth.PrivateKeyRef,
		)
		if err == nil {
			err = add(
				update.Passphrase,
				domain.CredentialSSHPassphrase,
				8192,
				&auth.PassphraseRef,
			)
		}
		if update.RemovePassphrase {
			auth.PassphraseRef = ""
		}
	default:
		err = ErrInvalid
	}
	if err == nil {
		err = auth.validate()
	}
	if err != nil {
		closePending(pending)
	}
	return auth, pending, err
}

func (service *Service) installCredentials(
	ctx context.Context,
	config *Configuration,
	pending []pendingCredential,
) error {
	if len(pending) == 0 {
		return nil
	}
	for _, value := range pending {
		config.CredentialRefs = append(config.CredentialRefs, value.ref.Value())
	}
	sort.Strings(config.CredentialRefs)
	if err := service.store.persist(ctx, config); err != nil {
		return err
	}
	for _, value := range pending {
		if err := service.credentials.Set(ctx, value.ref, value.value); err != nil {
			return credentialFailure(ctx)
		}
	}
	return nil
}

func nodeIndex(config Configuration, id string) int {
	return slices.IndexFunc(config.Nodes, func(node Node) bool { return node.ID == id })
}

func activeReferences(config Configuration) map[string]bool {
	active := map[string]bool{}
	for _, node := range config.Nodes {
		for _, ref := range node.refs() {
			active[ref] = true
		}
	}
	return active
}
