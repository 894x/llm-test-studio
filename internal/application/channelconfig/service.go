// Package channelconfig owns the atomic Base URL + API key channel workflow.
package channelconfig

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/894x/llm-test-studio/internal/application/catalog"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

var ErrInvalid = fmt.Errorf("channel config: %w", catalog.ErrInvalid)

type Repository interface {
	CreateChannel(context.Context, domain.Channel) error
	GetChannel(context.Context, string) (domain.Channel, error)
	UpdateChannel(context.Context, uint64, domain.Channel) error
	DeleteChannel(context.Context, string, uint64) error
}

type CredentialReferenceChecker interface {
	IsCredentialReferenced(context.Context, string) (bool, error)
}

// CredentialReferenceGuard keeps the authored catalog stable while deciding
// whether a keyring entry is still referenced and, if it is not, running the
// cleanup callback. Implementations must hold the same cross-process lock used
// by Channel and Plan mutations until action returns.
type CredentialReferenceGuard interface {
	WithCredentialUnreferenced(context.Context, string, func() error) (bool, error)
}

// CredentialMutationGuard serializes the provisional keyring write, authored
// Channel commit, and reference-guarded cleanup against other processes that
// may replay the credential registry.
type CredentialMutationGuard interface {
	WithCredentialMutation(context.Context, func(context.Context) error) error
}

type Clock interface{ Now() time.Time }
type MetaFactory func(time.Time) (domain.EntityMeta, error)

type Dependencies struct {
	Repository           Repository
	Credentials          credentials.Store
	CleanupQueue         credentials.CleanupQueue
	Clock                Clock
	MetaFactory          MetaFactory
	ReportCleanupFailure func(error)
}

type Service struct {
	repository           Repository
	credentials          credentials.Store
	cleanupQueue         credentials.CleanupQueue
	clock                Clock
	metaFactory          MetaFactory
	reportCleanupFailure func(error)
}

type CreateCommand struct {
	Name     string
	BaseURL  string
	APIKey   string
	Protocol domain.Protocol
	Enabled  bool
}

type UpdateCommand struct {
	ID               string
	ExpectedRevision uint64
	Name             string
	BaseURL          string
	APIKey           string
	Protocol         domain.Protocol
	Enabled          bool
}

type MutationResult struct {
	ChannelID          string
	ChannelRevision    uint64
	CredentialID       string
	CredentialRevision uint64
}

func New(dependencies Dependencies) (*Service, error) {
	if nilDependency(dependencies.Repository) || nilDependency(dependencies.Credentials) || nilDependency(dependencies.Clock) {
		return nil, ErrInvalid
	}
	factory := dependencies.MetaFactory
	if factory == nil {
		factory = domain.NewEntityMeta
	}
	cleanupQueue := dependencies.CleanupQueue
	if nilDependency(cleanupQueue) {
		cleanupQueue = credentials.NewMemoryCleanupQueue()
	}
	return &Service{
		repository: dependencies.Repository, credentials: dependencies.Credentials,
		cleanupQueue: cleanupQueue,
		clock:        dependencies.Clock, metaFactory: factory,
		reportCleanupFailure: dependencies.ReportCleanupFailure,
	}, nil
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (MutationResult, error) {
	if service == nil {
		return MutationResult{}, ErrInvalid
	}
	var result MutationResult
	err := service.withCredentialMutation(ctx, func(lockedCtx context.Context) error {
		var err error
		result, err = service.create(lockedCtx, command)
		return err
	})
	return result, err
}

func (service *Service) create(ctx context.Context, command CreateCommand) (MutationResult, error) {
	if service == nil || ctx == nil || command.APIKey == "" {
		return MutationResult{}, ErrInvalid
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	credentialMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialMeta.ID)
	if err != nil {
		return MutationResult{}, ErrInvalid
	}
	channelMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	channel := domain.Channel{
		EntityMeta: channelMeta, Name: command.Name, BaseURL: command.BaseURL,
		Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: credentialMeta.ID,
	}
	if err := channel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.cleanupQueue.Enqueue(ctx, credentialMeta.ID); err != nil {
		return MutationResult{}, fmt.Errorf("schedule new channel credential cleanup: %w", err)
	}
	if err := service.credentials.Set(ctx, storeRef, secret); err != nil {
		return MutationResult{}, errors.Join(
			fmt.Errorf("store channel credential: %w", err),
			service.cleanupScheduledCredential(context.WithoutCancel(ctx), credentialMeta.ID),
		)
	}
	if err := service.repository.CreateChannel(ctx, channel); err != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		committed, readErr := service.repository.GetChannel(cleanupCtx, channel.ID)
		if readErr == nil && reflect.DeepEqual(committed, channel) {
			service.reportCredentialCleanup(service.cleanupScheduledCredential(cleanupCtx, credentialMeta.ID))
			return mutationResult(channel, credentialMeta), nil
		}
		cleanupErr := service.cleanupScheduledCredential(cleanupCtx, credentialMeta.ID)
		return MutationResult{}, errors.Join(
			fmt.Errorf("persist channel: %w", err),
			catalogCommitVerificationError("channel", readErr),
			cleanupErr,
		)
	}
	service.reportCredentialCleanup(service.cleanupScheduledCredential(context.WithoutCancel(ctx), credentialMeta.ID))
	return mutationResult(channel, credentialMeta), nil
}

func (service *Service) Update(ctx context.Context, command UpdateCommand) (MutationResult, error) {
	if service == nil {
		return MutationResult{}, ErrInvalid
	}
	var result MutationResult
	err := service.withCredentialMutation(ctx, func(lockedCtx context.Context) error {
		var err error
		result, err = service.update(lockedCtx, command)
		return err
	})
	return result, err
}

func (service *Service) update(ctx context.Context, command UpdateCommand) (MutationResult, error) {
	if service == nil || ctx == nil || !domain.IsUUID(command.ID) || command.ExpectedRevision == 0 || command.APIKey == "" {
		return MutationResult{}, ErrInvalid
	}
	current, err := service.repository.GetChannel(ctx, command.ID)
	if err != nil {
		return MutationResult{}, fmt.Errorf("load channel for update: %w", err)
	}
	if current.ID != command.ID || current.Validate() != nil {
		return MutationResult{}, fmt.Errorf("load channel for update: %w", catalog.ErrCorrupt)
	}
	if current.Revision != command.ExpectedRevision {
		return MutationResult{}, catalog.ErrConflict
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	credentialMeta, err := service.metaFactory(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, credentialMeta.ID)
	if err != nil {
		return MutationResult{}, ErrInvalid
	}
	channelMeta, err := current.EntityMeta.NextRevision(service.clock.Now())
	if err != nil {
		return MutationResult{}, err
	}
	updatedChannel := domain.Channel{
		EntityMeta: channelMeta, Name: command.Name, BaseURL: command.BaseURL,
		Protocol: command.Protocol, Enabled: command.Enabled, CredentialID: credentialMeta.ID,
	}
	if err := updatedChannel.Validate(); err != nil {
		return MutationResult{}, ErrInvalid
	}
	if err := service.cleanupQueue.Enqueue(ctx, credentialMeta.ID); err != nil {
		return MutationResult{}, fmt.Errorf("schedule replacement channel credential cleanup: %w", err)
	}
	if err := service.credentials.Set(ctx, storeRef, secret); err != nil {
		return MutationResult{}, errors.Join(
			fmt.Errorf("store replacement channel credential: %w", err),
			service.cleanupScheduledCredential(context.WithoutCancel(ctx), credentialMeta.ID),
		)
	}
	if current.CredentialID != "" {
		if err := service.cleanupQueue.Enqueue(ctx, current.CredentialID); err != nil {
			return MutationResult{}, errors.Join(
				fmt.Errorf("schedule obsolete channel credential cleanup: %w", err),
				service.cleanupScheduledCredential(context.WithoutCancel(ctx), credentialMeta.ID),
			)
		}
	}
	if err := service.repository.UpdateChannel(ctx, current.Revision, updatedChannel); err != nil {
		cleanupCtx := context.WithoutCancel(ctx)
		committed, readErr := service.repository.GetChannel(cleanupCtx, current.ID)
		if readErr == nil && reflect.DeepEqual(committed, updatedChannel) {
			service.reportCredentialCleanup(errors.Join(
				service.cleanupScheduledCredential(cleanupCtx, credentialMeta.ID),
				service.cleanupScheduledCredential(cleanupCtx, current.CredentialID),
			))
			return mutationResult(updatedChannel, credentialMeta), nil
		}
		cleanupErr := errors.Join(
			service.cleanupScheduledCredential(cleanupCtx, credentialMeta.ID),
			service.cleanupScheduledCredential(cleanupCtx, current.CredentialID),
		)
		return MutationResult{}, errors.Join(
			fmt.Errorf("persist replacement channel revision: %w", err),
			catalogCommitVerificationError("replacement channel revision", readErr),
			cleanupErr,
		)
	}
	cleanupCtx := context.WithoutCancel(ctx)
	service.reportCredentialCleanup(errors.Join(
		service.cleanupScheduledCredential(cleanupCtx, credentialMeta.ID),
		service.cleanupScheduledCredential(cleanupCtx, current.CredentialID),
	))
	return mutationResult(updatedChannel, credentialMeta), nil
}

func (service *Service) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil {
		return ErrInvalid
	}
	return service.withCredentialMutation(ctx, func(lockedCtx context.Context) error {
		return service.delete(lockedCtx, id, expectedRevision)
	})
}

func (service *Service) delete(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil || ctx == nil || !domain.IsUUID(id) || expectedRevision == 0 {
		return ErrInvalid
	}
	current, err := service.repository.GetChannel(ctx, id)
	if err != nil {
		return fmt.Errorf("load channel for delete: %w", err)
	}
	if current.ID != id || current.Validate() != nil {
		return fmt.Errorf("load channel for delete: %w", catalog.ErrCorrupt)
	}
	if current.Revision != expectedRevision {
		return catalog.ErrConflict
	}
	if current.CredentialID != "" {
		if err := service.cleanupQueue.Enqueue(ctx, current.CredentialID); err != nil {
			return fmt.Errorf("schedule deleted channel credential cleanup: %w", err)
		}
	}
	if err := service.repository.DeleteChannel(ctx, id, expectedRevision); err != nil {
		return errors.Join(err, service.cleanupScheduledCredential(context.WithoutCancel(ctx), current.CredentialID))
	}
	service.reportCredentialCleanup(service.cleanupScheduledCredential(context.WithoutCancel(ctx), current.CredentialID))
	return nil
}

func (service *Service) withCredentialMutation(ctx context.Context, action func(context.Context) error) error {
	if service == nil || ctx == nil || action == nil {
		return ErrInvalid
	}
	guard, ok := service.repository.(CredentialMutationGuard)
	if !ok || nilDependency(guard) {
		return action(ctx)
	}
	return guard.WithCredentialMutation(ctx, action)
}

func mutationResult(channel domain.Channel, credentialMeta domain.EntityMeta) MutationResult {
	return MutationResult{
		ChannelID: channel.ID, ChannelRevision: channel.Revision,
		CredentialID: credentialMeta.ID, CredentialRevision: credentialMeta.Revision,
	}
}

func catalogCommitVerificationError(kind string, readErr error) error {
	if readErr != nil {
		return fmt.Errorf("verify committed %s: %w", kind, readErr)
	}
	return fmt.Errorf("verify committed %s: stored document differs", kind)
}

// CleanupCredentialIfUnreferenced removes a credential released by another
// authored-catalog mutation, such as replacing or deleting a Plan binding.
// Cleanup is deliberately diagnostic-only: the authored mutation has already
// committed and must never be rolled back because the keyring is unavailable.
func (service *Service) CleanupCredentialIfUnreferenced(id string) {
	if service == nil {
		return
	}
	ctx := context.Background()
	if err := service.cleanupQueue.Enqueue(ctx, id); err != nil {
		service.reportCredentialCleanup(fmt.Errorf("schedule released credential cleanup: %w", err))
		return
	}
	service.reportCredentialCleanup(service.cleanupScheduledCredential(ctx, id))
}

// ScheduleCredentialCleanup durably records candidates before a Plan mutation
// can remove their final serialized reference.
func (service *Service) ScheduleCredentialCleanup(ctx context.Context, ids ...string) error {
	if service == nil || ctx == nil {
		return ErrInvalid
	}
	if err := service.cleanupQueue.Enqueue(ctx, ids...); err != nil {
		return fmt.Errorf("schedule released credential cleanup: %w", err)
	}
	return nil
}

// RetryPendingCredentialCleanup replays the durable non-secret queue at
// startup. Failed deletions remain queued for the next retry.
func (service *Service) RetryPendingCredentialCleanup(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalid
	}
	ids, err := service.cleanupQueue.List(ctx)
	if err != nil {
		return fmt.Errorf("list pending credential cleanup: %w", err)
	}
	var result error
	for _, id := range ids {
		result = errors.Join(result, service.cleanupScheduledCredential(ctx, id))
	}
	return result
}

func (service *Service) cleanupScheduledCredential(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	if !domain.IsUUID(id) {
		return ErrInvalid
	}
	storeRef, err := credentials.NewStoreRef(domain.CredentialChannelAPIKey, id)
	if err != nil {
		return ErrInvalid
	}
	guard, ok := service.repository.(CredentialReferenceGuard)
	if !ok || nilDependency(guard) {
		return errors.New("delete obsolete channel credential: repository does not provide an atomic credential reference guard")
	}
	executed, err := guard.WithCredentialUnreferenced(ctx, id, func() error {
		deleteErr := service.credentials.Delete(ctx, storeRef)
		if errors.Is(deleteErr, credentials.ErrNotFound) {
			return nil
		}
		return deleteErr
	})
	if err != nil {
		return fmt.Errorf("delete obsolete channel credential: %w", err)
	}
	if !executed {
		// Referenced entries form the durable registry needed to discover a
		// secret after a user directly edits file-backed configuration. The
		// mutation that removes the final reference, or the next startup scan,
		// will retry it.
		return nil
	}
	if err := service.cleanupQueue.Remove(ctx, id); err != nil {
		return fmt.Errorf("finish obsolete channel credential cleanup: %w", err)
	}
	return nil
}

func (service *Service) reportCredentialCleanup(err error) {
	if err != nil && service.reportCleanupFailure != nil {
		service.reportCleanupFailure(err)
	}
}

func nilDependency(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
