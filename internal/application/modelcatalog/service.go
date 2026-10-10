// Package modelcatalog owns the shareable models.json authored catalog.
package modelcatalog

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
	ErrInvalid  = errors.New("model catalog: invalid input")
	ErrNotFound = errors.New("model catalog: model not found")
	ErrConflict = errors.New("model catalog: revision conflict")
	ErrCorrupt  = errors.New("model catalog: corrupt file")
)

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

func (service *Service) List(ctx context.Context) ([]domain.Model, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	models, err := service.load(ctx)
	if err != nil {
		return nil, err
	}
	return cloneModels(models), nil
}

func (service *Service) Get(ctx context.Context, id string) (domain.Model, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) {
		return domain.Model{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	models, err := service.load(ctx)
	if err != nil {
		return domain.Model{}, err
	}
	for _, model := range models {
		if model.ID == id {
			return cloneModel(model), nil
		}
	}
	return domain.Model{}, ErrNotFound
}

func (service *Service) Create(ctx context.Context, model domain.Model) error {
	if service == nil || ctx == nil || model.Validate() != nil || model.Revision != 1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		models, err := service.load(ctx)
		if err != nil {
			return err
		}
		for _, current := range models {
			if current.ID == model.ID {
				return ErrConflict
			}
		}
		models = append(models, cloneModel(model))
		return service.save(ctx, models)
	})
}

func (service *Service) Update(ctx context.Context, expectedRevision uint64, model domain.Model) error {
	if service == nil || ctx == nil || expectedRevision == 0 || model.Validate() != nil || model.Revision != expectedRevision+1 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		models, err := service.load(ctx)
		if err != nil {
			return err
		}
		for index, current := range models {
			if current.ID != model.ID {
				continue
			}
			if current.Revision != expectedRevision {
				return ErrConflict
			}
			models[index] = cloneModel(model)
			return service.save(ctx, models)
		}
		return ErrNotFound
	})
}

func (service *Service) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil || ctx == nil || !domain.IsUUID(id) || expectedRevision == 0 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		models, err := service.load(ctx)
		if err != nil {
			return err
		}
		for index, current := range models {
			if current.ID != id {
				continue
			}
			if current.Revision != expectedRevision {
				return ErrConflict
			}
			models = append(models[:index], models[index+1:]...)
			return service.save(ctx, models)
		}
		return ErrNotFound
	})
}

func (service *Service) load(ctx context.Context) ([]domain.Model, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(service.path)
	if errors.Is(err, os.ErrNotExist) {
		return []domain.Model{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read model catalog: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var models []domain.Model
	if err := decoder.Decode(&models); err != nil {
		return nil, fmt.Errorf("%w: decode models: %v; update models.json to use protocols arrays", ErrCorrupt, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: models.json must contain one array", ErrCorrupt)
	}
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model.Validate() != nil {
			return nil, fmt.Errorf("%w: invalid model %q", ErrCorrupt, model.ID)
		}
		if _, duplicate := seen[model.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate model %q", ErrCorrupt, model.ID)
		}
		seen[model.ID] = struct{}{}
	}
	sort.Slice(models, func(left, right int) bool {
		if models[left].CreatedAt.Equal(models[right].CreatedAt) {
			return models[left].ID < models[right].ID
		}
		return models[left].CreatedAt.Before(models[right].CreatedAt)
	})
	return models, nil
}

func (service *Service) save(ctx context.Context, models []domain.Model) error {
	raw, err := json.MarshalIndent(models, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model catalog: %w", err)
	}
	raw = append(raw, '\n')
	if err := fileconfig.WriteAtomically(ctx, service.path, raw); err != nil {
		return fmt.Errorf("write model catalog: %w", err)
	}
	return nil
}

func cloneModels(models []domain.Model) []domain.Model {
	result := make([]domain.Model, len(models))
	for index, model := range models {
		result[index] = cloneModel(model)
	}
	return result
}

func cloneModel(model domain.Model) domain.Model {
	model.Protocols = append([]domain.Protocol{}, model.Protocols...)
	model.Capabilities = append([]string(nil), model.Capabilities...)
	return model
}
