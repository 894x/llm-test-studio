package runs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
)

var ErrQuickTaskCredential = errors.New("runs: remembered quick task credential is unavailable")

type RememberQuickTaskCredentialCommand struct {
	RunID    string          `json:"run_id"`
	BaseURL  string          `json:"base_url"`
	Protocol domain.Protocol `json:"protocol"`
	APIKey   string          `json:"api_key"`
}

func validQuickTaskAPIKey(value string) bool {
	return len(value) > 0 && len(value) <= 16384 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

// RememberQuickTaskCredential is a separate, explicit action after a Run exists.
// That Run identity owns the keyring entry, so no authored target or credential
// metadata file is needed and failed attempts cannot orphan an unreferenced key.
func (service *Service) RememberQuickTaskCredential(ctx context.Context, command RememberQuickTaskCredentialCommand) error {
	if !validQuickTaskAPIKey(command.APIKey) {
		return ErrInvalid
	}
	ref, err := service.quickTaskCredentialRef(ctx, command.RunID, command.BaseURL, command.Protocol)
	if err != nil {
		return err
	}
	secret := []byte(command.APIKey)
	defer clear(secret)
	err = service.quickTaskCredentials.Set(ctx, ref, secret)
	if errors.Is(err, credentials.ErrAlreadyExists) {
		err = service.quickTaskCredentials.Replace(ctx, ref, secret)
	}
	if err != nil {
		return fmt.Errorf("remember quick task credential: %w", errors.Join(ErrQuickTaskCredential, err))
	}
	return nil
}

func (service *Service) ForgetQuickTaskCredential(ctx context.Context, runID string) error {
	if err := service.quickTaskCredentialStoreReady(ctx); err != nil {
		return err
	}
	snapshot, err := service.quickTaskSnapshot(ctx, runID)
	if err != nil {
		return err
	}
	if snapshot.QuickTask.SavedChannelID != "" {
		return ErrNotRunnable
	}
	ref, _ := credentials.NewStoreRef(domain.CredentialQuickTaskAPIKey, runID)
	if err := service.quickTaskCredentials.Delete(ctx, ref); err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return fmt.Errorf("forget quick task credential: %w", errors.Join(ErrQuickTaskCredential, err))
	}
	return nil
}

// LeaseQuickTaskCredential binds a remembered key to its original protocol and
// complete base URL. A different model/Suite at that target can reuse the key.
func (service *Service) LeaseQuickTaskCredential(ctx context.Context, runID, baseURL string, protocol domain.Protocol) (*credentials.Lease, error) {
	ref, err := service.quickTaskCredentialRef(ctx, runID, baseURL, protocol)
	if err != nil {
		return nil, err
	}
	lease, err := service.quickTaskCredentials.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("lease quick task credential: %w", errors.Join(ErrQuickTaskCredential, err))
	}
	return lease, nil
}

func (service *Service) quickTaskCredentialRef(ctx context.Context, runID, baseURL string, protocol domain.Protocol) (credentials.StoreRef, error) {
	if err := service.quickTaskCredentialStoreReady(ctx); err != nil {
		return credentials.StoreRef{}, err
	}
	snapshot, err := service.quickTaskSnapshot(ctx, runID)
	if err != nil {
		return credentials.StoreRef{}, err
	}
	if snapshot.QuickTask.SavedChannelID != "" || snapshot.Channel.BaseURL != baseURL || snapshot.Channel.Protocol != protocol || !secureCredentialEndpoint(baseURL, service.allowInsecureLoopback) {
		return credentials.StoreRef{}, ErrNotRunnable
	}
	return credentials.NewStoreRef(domain.CredentialQuickTaskAPIKey, runID)
}

func (service *Service) quickTaskCredentialStoreReady(ctx context.Context) error {
	if service == nil || ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.Lock()
	closed := service.closed
	service.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if isNil(service.quickTaskCredentials) {
		return ErrQuickTaskCredential
	}
	return nil
}

func (service *Service) rememberedQuickTaskCredential(ctx context.Context, runID string, snapshot domain.RunSnapshot) string {
	if service.quickTaskCredentialStoreReady(ctx) != nil || snapshot.QuickTask.SavedChannelID != "" {
		return ""
	}
	for _, id := range []string{runID, snapshot.QuickTask.CredentialRunID} {
		if id == "" {
			continue
		}
		ref, err := service.quickTaskCredentialRef(ctx, id, snapshot.Channel.BaseURL, snapshot.Channel.Protocol)
		if err == nil && service.quickTaskCredentials.Test(ctx, ref) == nil {
			return id
		}
	}
	return ""
}
