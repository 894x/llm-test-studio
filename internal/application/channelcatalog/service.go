// Package channelcatalog owns channels.json. The document stores only channel
// metadata and model mappings; API keys remain in the operating-system keyring.
package channelcatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

var (
	ErrInvalid  = errors.New("channel catalog: invalid input")
	ErrNotFound = errors.New("channel catalog: record not found")
	ErrConflict = errors.New("channel catalog: revision conflict")
	ErrCorrupt  = errors.New("channel catalog: corrupt file")
)

type document struct {
	domain.Channel
	ModelMappings []domain.ChannelModel `json:"model_mappings"`
}

type Service struct {
	path     string
	lockPath string
	mu       sync.Mutex
}

func New(path string) (*Service, error) {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return nil, ErrInvalid
	}
	path = filepath.Clean(path)
	return &Service{path: path, lockPath: path + ".lock"}, nil
}

func (service *Service) ListChannels(ctx context.Context) ([]domain.Channel, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	documents, err := service.load(ctx)
	if err != nil {
		return nil, err
	}
	channels := make([]domain.Channel, len(documents))
	for index, value := range documents {
		channels[index] = value.Channel
	}
	return channels, nil
}

func (service *Service) ListMappings(ctx context.Context) ([]domain.ChannelModel, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	documents, err := service.load(ctx)
	if err != nil {
		return nil, err
	}
	var mappings []domain.ChannelModel
	for _, value := range documents {
		mappings = append(mappings, value.ModelMappings...)
	}
	return mappings, nil
}

func (service *Service) GetChannel(ctx context.Context, id string) (domain.Channel, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) {
		return domain.Channel{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	documents, err := service.load(ctx)
	if err != nil {
		return domain.Channel{}, err
	}
	for _, value := range documents {
		if value.ID == id {
			return value.Channel, nil
		}
	}
	return domain.Channel{}, ErrNotFound
}

func (service *Service) GetMapping(ctx context.Context, id string) (domain.ChannelModel, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) {
		return domain.ChannelModel{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	documents, err := service.load(ctx)
	if err != nil {
		return domain.ChannelModel{}, err
	}
	for _, value := range documents {
		for _, mapping := range value.ModelMappings {
			if mapping.ID == id {
				return mapping, nil
			}
		}
	}
	return domain.ChannelModel{}, ErrNotFound
}

func (service *Service) CreateChannel(ctx context.Context, channel domain.Channel) error {
	return service.CreateChannelWithMappings(ctx, channel, []domain.ChannelModel{})
}

func (service *Service) CreateChannelWithMappings(ctx context.Context, channel domain.Channel, mappings []domain.ChannelModel) error {
	if service == nil || ctx == nil || channel.Validate() != nil || channel.Revision != 1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for _, current := range documents {
			if current.ID == channel.ID {
				return ErrConflict
			}
		}
		mappingIDs := make(map[string]bool)
		modelIDs := make(map[string]bool)
		for _, current := range documents {
			for _, mapping := range current.ModelMappings {
				mappingIDs[mapping.ID] = true
			}
		}
		for _, mapping := range mappings {
			if mapping.Validate() != nil || mapping.ChannelID != channel.ID || mapping.Revision != 1 {
				return ErrInvalid
			}
			if mappingIDs[mapping.ID] || modelIDs[mapping.ModelID] {
				return ErrConflict
			}
			mappingIDs[mapping.ID] = true
			modelIDs[mapping.ModelID] = true
		}
		documents = append(documents, document{Channel: channel, ModelMappings: mappings})
		return service.save(ctx, documents)
	})
}

func (service *Service) UpdateChannel(ctx context.Context, expectedRevision uint64, channel domain.Channel) error {
	if service == nil || ctx == nil || expectedRevision == 0 || channel.Validate() != nil || channel.Revision != expectedRevision+1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for index, current := range documents {
			if current.ID != channel.ID {
				continue
			}
			if current.Revision != expectedRevision {
				return ErrConflict
			}
			documents[index].Channel = channel
			return service.save(ctx, documents)
		}
		return ErrNotFound
	})
}

func (service *Service) DeleteChannel(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil || ctx == nil || !domain.IsUUID(id) || expectedRevision == 0 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for index, current := range documents {
			if current.ID != id {
				continue
			}
			if current.Revision != expectedRevision {
				return ErrConflict
			}
			if len(current.ModelMappings) != 0 {
				return ErrConflict
			}
			documents = append(documents[:index], documents[index+1:]...)
			return service.save(ctx, documents)
		}
		return ErrNotFound
	})
}

func (service *Service) CreateMapping(ctx context.Context, mapping domain.ChannelModel) error {
	if service == nil || ctx == nil || mapping.Validate() != nil || mapping.Revision != 1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for documentIndex := range documents {
			for _, current := range documents[documentIndex].ModelMappings {
				if current.ID == mapping.ID || current.ChannelID == mapping.ChannelID && current.ModelID == mapping.ModelID {
					return ErrConflict
				}
			}
		}
		for documentIndex := range documents {
			if documents[documentIndex].ID == mapping.ChannelID {
				documents[documentIndex].ModelMappings = append(documents[documentIndex].ModelMappings, mapping)
				return service.save(ctx, documents)
			}
		}
		return ErrNotFound
	})
}

func (service *Service) UpdateMapping(ctx context.Context, expectedRevision uint64, mapping domain.ChannelModel) error {
	if service == nil || ctx == nil || expectedRevision == 0 || mapping.Validate() != nil || mapping.Revision != expectedRevision+1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for documentIndex := range documents {
			for mappingIndex, current := range documents[documentIndex].ModelMappings {
				if current.ID != mapping.ID {
					continue
				}
				if current.Revision != expectedRevision {
					return ErrConflict
				}
				if current.ChannelID != mapping.ChannelID || current.ModelID != mapping.ModelID {
					return ErrInvalid
				}
				documents[documentIndex].ModelMappings[mappingIndex] = mapping
				return service.save(ctx, documents)
			}
		}
		return ErrNotFound
	})
}

func (service *Service) DeleteMapping(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil || ctx == nil || !domain.IsUUID(id) || expectedRevision == 0 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		documents, err := service.load(ctx)
		if err != nil {
			return err
		}
		for documentIndex := range documents {
			for mappingIndex, current := range documents[documentIndex].ModelMappings {
				if current.ID != id {
					continue
				}
				if current.Revision != expectedRevision {
					return ErrConflict
				}
				documents[documentIndex].ModelMappings = append(
					documents[documentIndex].ModelMappings[:mappingIndex],
					documents[documentIndex].ModelMappings[mappingIndex+1:]...,
				)
				return service.save(ctx, documents)
			}
		}
		return ErrNotFound
	})
}

func (service *Service) load(ctx context.Context) ([]document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return []document{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read channel catalog: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var documents []document
	if err := decoder.Decode(&documents); err != nil {
		return nil, fmt.Errorf("%w: decode channels: %v; convert channels.json to the current format: remove channel protocol and retain mapping protocols arrays", ErrCorrupt, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: channels.json must contain one array", ErrCorrupt)
	}
	channels := make(map[string]struct{}, len(documents))
	mappings := make(map[string]struct{})
	bindings := make(map[string]struct{})
	for documentIndex := range documents {
		value := &documents[documentIndex]
		if value.Channel.Validate() != nil {
			return nil, fmt.Errorf("%w: invalid channel %q", ErrCorrupt, value.ID)
		}
		if _, duplicate := channels[value.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate channel %q", ErrCorrupt, value.ID)
		}
		channels[value.ID] = struct{}{}
		for _, mapping := range value.ModelMappings {
			if err := mapping.Validate(); err != nil {
				return nil, fmt.Errorf("%w: invalid channel mapping %q: %v", ErrCorrupt, mapping.ID, err)
			}
			if mapping.ChannelID != value.ID {
				return nil, fmt.Errorf("%w: invalid channel mapping %q", ErrCorrupt, mapping.ID)
			}
			if _, duplicate := mappings[mapping.ID]; duplicate {
				return nil, fmt.Errorf("%w: duplicate mapping %q", ErrCorrupt, mapping.ID)
			}
			binding := mapping.ChannelID + "/" + mapping.ModelID
			if _, duplicate := bindings[binding]; duplicate {
				return nil, fmt.Errorf("%w: duplicate mapping binding %q", ErrCorrupt, binding)
			}
			mappings[mapping.ID] = struct{}{}
			bindings[binding] = struct{}{}
		}
	}
	sortDocuments(documents)
	return documents, nil
}

func (service *Service) save(ctx context.Context, documents []document) error {
	sortDocuments(documents)
	raw, err := json.MarshalIndent(documents, "", "  ")
	if err != nil {
		return fmt.Errorf("encode channel catalog: %w", err)
	}
	raw = append(raw, '\n')
	if err := fileconfig.WriteAtomically(ctx, service.path, raw); err != nil {
		return fmt.Errorf("write channel catalog: %w", err)
	}
	return nil
}

func sortDocuments(documents []document) {
	sort.Slice(documents, func(left, right int) bool {
		if documents[left].CreatedAt.Equal(documents[right].CreatedAt) {
			return documents[left].ID < documents[right].ID
		}
		return documents[left].CreatedAt.Before(documents[right].CreatedAt)
	})
	for index := range documents {
		sort.Slice(documents[index].ModelMappings, func(left, right int) bool {
			leftValue := documents[index].ModelMappings[left]
			rightValue := documents[index].ModelMappings[right]
			if leftValue.CreatedAt.Equal(rightValue.CreatedAt) {
				return leftValue.ID < rightValue.ID
			}
			return leftValue.CreatedAt.Before(rightValue.CreatedAt)
		})
	}
}
