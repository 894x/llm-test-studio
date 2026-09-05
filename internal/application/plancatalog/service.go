// Package plancatalog owns the shareable one-plan-per-file authored catalog.
package plancatalog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const CurrentFileSchemaVersion = 1

var (
	ErrInvalid  = errors.New("plan catalog: invalid input")
	ErrNotFound = errors.New("plan catalog: plan not found")
	ErrConflict = errors.New("plan catalog: revision conflict")
	ErrCorrupt  = errors.New("plan catalog: corrupt file")
)

// TargetBinding freezes the exact non-secret authored target configuration used
// by a targeted Plan. Channel.CredentialID is only a keyring reference; secret
// material is never part of this document.
type TargetBinding struct {
	Model   domain.Model        `json:"model"`
	Channel domain.Channel      `json:"channel"`
	Mapping domain.ChannelModel `json:"mapping"`
}

// Document is the on-disk Plan format. Embedding Plan keeps its established
// fields flat while the file schema and immutable target bindings remain
// explicitly versioned by the file catalog.
type Document struct {
	FileSchemaVersion int `json:"file_schema_version"`
	domain.Plan
	TargetBindings []TargetBinding `json:"target_bindings"`
}

// Validate verifies the complete Cartesian target binding matrix. Bindings are
// ordered by Plan.ChannelIDs first and Plan.ModelIDs second so the file is both
// deterministic and unambiguous.
func (document Document) Validate() error {
	if document.FileSchemaVersion != CurrentFileSchemaVersion {
		return fmt.Errorf("unsupported plan file schema version %d", document.FileSchemaVersion)
	}
	if document.Plan.Validate() != nil {
		return errors.New("invalid plan")
	}
	// Requiring a non-nil slice distinguishes a complete wrapper from a wrapper
	// that omitted target_bindings. Targetless plans encode an empty JSON array.
	if document.TargetBindings == nil {
		return errors.New("target bindings must be present")
	}
	if len(document.ModelIDs) == 0 {
		if len(document.TargetBindings) != 0 {
			return errors.New("targetless plan must not contain target bindings")
		}
		return nil
	}

	expectedCount := len(document.ChannelIDs) * len(document.ModelIDs)
	if expectedCount/len(document.ModelIDs) != len(document.ChannelIDs) || len(document.TargetBindings) != expectedCount {
		return errors.New("target bindings must cover the plan Cartesian product")
	}

	models := make(map[string]domain.Model, len(document.ModelIDs))
	channels := make(map[string]domain.Channel, len(document.ChannelIDs))
	mappingIDs := make(map[string]struct{}, expectedCount)
	bindingIndex := 0
	for _, channelID := range document.ChannelIDs {
		for _, modelID := range document.ModelIDs {
			binding := document.TargetBindings[bindingIndex]
			bindingIndex++
			if binding.Model.Validate() != nil || binding.Channel.Validate() != nil || binding.Mapping.Validate() != nil {
				return errors.New("target binding contains an invalid document")
			}
			if binding.Model.ID != modelID || binding.Channel.ID != channelID {
				return errors.New("target binding order or identity does not match the plan")
			}
			if binding.Model.Protocol != binding.Channel.Protocol {
				return errors.New("target model and channel protocols do not match")
			}
			if binding.Mapping.ModelID != modelID || binding.Mapping.ChannelID != channelID {
				return errors.New("target mapping does not match its model and channel")
			}
			if previous, exists := models[modelID]; exists && !reflect.DeepEqual(previous, binding.Model) {
				return errors.New("repeated target model documents are inconsistent")
			}
			if previous, exists := channels[channelID]; exists && !reflect.DeepEqual(previous, binding.Channel) {
				return errors.New("repeated target channel documents are inconsistent")
			}
			if _, duplicate := mappingIDs[binding.Mapping.ID]; duplicate {
				return errors.New("target mapping identity is duplicated")
			}
			models[modelID] = binding.Model
			channels[channelID] = binding.Channel
			mappingIDs[binding.Mapping.ID] = struct{}{}
		}
	}
	return nil
}

type Service struct {
	root     string
	lockPath string
	mu       sync.Mutex
}

func New(root string) (*Service, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	root = filepath.Clean(root)
	return &Service{root: root, lockPath: root + ".lock"}, nil
}

// List returns only Plan projections for existing catalog.Repository callers.
func (service *Service) List(ctx context.Context) ([]domain.Plan, error) {
	documents, err := service.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	plans := make([]domain.Plan, len(documents))
	for index, document := range documents {
		plans[index] = clonePlan(document.Plan)
	}
	return plans, nil
}

func (service *Service) ListDocuments(ctx context.Context) ([]Document, error) {
	if service == nil || ctx == nil {
		return nil, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	documents, err := service.loadAll(ctx)
	if err != nil {
		return nil, err
	}
	return cloneDocuments(documents), nil
}

// Get returns only the Plan projection for existing catalog.Repository callers.
func (service *Service) Get(ctx context.Context, id string) (domain.Plan, error) {
	document, err := service.GetDocument(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	return clonePlan(document.Plan), nil
}

func (service *Service) GetDocument(ctx context.Context, id string) (Document, error) {
	if service == nil || ctx == nil || !domain.IsUUID(id) {
		return Document{}, ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	document, err := service.loadOne(ctx, id)
	if err != nil {
		return Document{}, err
	}
	return cloneDocument(document), nil
}

func (service *Service) GetRevision(ctx context.Context, id string, revision uint64) (domain.Plan, error) {
	plan, err := service.Get(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	if plan.Revision != revision {
		return domain.Plan{}, ErrNotFound
	}
	return plan, nil
}

// Create is retained for targetless Plans. Targeted Plans must use
// CreateDocument so their exact target revisions cannot be omitted.
func (service *Service) Create(ctx context.Context, plan domain.Plan) error {
	if len(plan.ModelIDs) != 0 || len(plan.ChannelIDs) != 0 {
		return ErrInvalid
	}
	return service.CreateDocument(ctx, Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings:    []TargetBinding{},
	})
}

func (service *Service) CreateDocument(ctx context.Context, document Document) error {
	if service == nil || ctx == nil || document.Validate() != nil || document.Revision != 1 {
		return ErrInvalid
	}
	document = cloneDocument(document)
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		if _, err := os.Lstat(service.path(document.ID)); err == nil {
			return ErrConflict
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect plan file: %w", err)
		}
		return service.save(ctx, document)
	})
}

// Update is retained for targetless Plans. Targeted Plans must use
// UpdateDocument so their exact target revisions cannot be omitted.
func (service *Service) Update(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	if len(plan.ModelIDs) != 0 || len(plan.ChannelIDs) != 0 {
		return ErrInvalid
	}
	return service.UpdateDocument(ctx, expectedRevision, Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan:              plan,
		TargetBindings:    []TargetBinding{},
	})
}

func (service *Service) UpdateDocument(ctx context.Context, expectedRevision uint64, document Document) error {
	if service == nil || ctx == nil || expectedRevision == 0 || document.Validate() != nil || document.Revision != expectedRevision+1 {
		return ErrInvalid
	}
	document = cloneDocument(document)
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		current, err := service.loadOne(ctx, document.ID)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrConflict
		}
		return service.save(ctx, document)
	})
}

func (service *Service) Delete(ctx context.Context, id string, expectedRevision uint64) error {
	if service == nil || ctx == nil || !domain.IsUUID(id) || expectedRevision == 0 {
		return ErrInvalid
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	return fileconfig.WithExclusiveLock(ctx, service.lockPath, func() error {
		current, err := service.loadOne(ctx, id)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return ErrConflict
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(service.path(id)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return fmt.Errorf("delete plan file: %w", err)
		}
		return nil
	})
}

func (service *Service) loadAll(ctx context.Context) ([]Document, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(service.root)
	if errors.Is(err, os.ErrNotExist) {
		return []Document{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plan directory: %w", err)
	}
	documents := make([]Document, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if fileconfig.IsAtomicTemporaryName(entry.Name()) && entry.Type()&os.ModeSymlink == 0 && !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return nil, fmt.Errorf("inspect plan directory entry %q: %w", entry.Name(), err)
			}
			if info.Mode().IsRegular() {
				continue
			}
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return nil, fmt.Errorf("%w: unexpected plan directory entry %q", ErrCorrupt, entry.Name())
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !domain.IsUUID(id) {
			return nil, fmt.Errorf("%w: invalid plan filename %q", ErrCorrupt, entry.Name())
		}
		document, err := service.loadOne(ctx, id)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[document.ID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate plan %q", ErrCorrupt, document.ID)
		}
		seen[document.ID] = struct{}{}
		documents = append(documents, document)
	}
	sort.Slice(documents, func(left, right int) bool {
		if documents[left].CreatedAt.Equal(documents[right].CreatedAt) {
			return documents[left].ID < documents[right].ID
		}
		return documents[left].CreatedAt.Before(documents[right].CreatedAt)
	})
	return documents, nil
}

func (service *Service) loadOne(ctx context.Context, id string) (Document, error) {
	if err := ctx.Err(); err != nil {
		return Document{}, err
	}
	raw, err := os.ReadFile(service.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("read plan file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("%w: decode plan %q: %v", ErrCorrupt, id, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Document{}, fmt.Errorf("%w: plan file must contain one object", ErrCorrupt)
	}
	if document.ID != id || document.Validate() != nil {
		return Document{}, fmt.Errorf("%w: invalid plan %q", ErrCorrupt, id)
	}
	return document, nil
}

func (service *Service) save(ctx context.Context, document Document) error {
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode plan: %w", err)
	}
	raw = append(raw, '\n')
	if err := fileconfig.WriteAtomically(ctx, service.path(document.ID), raw); err != nil {
		return fmt.Errorf("write plan file: %w", err)
	}
	return nil
}

func (service *Service) path(id string) string {
	return filepath.Join(service.root, id+".json")
}

func cloneDocuments(documents []Document) []Document {
	result := make([]Document, len(documents))
	for index, document := range documents {
		result[index] = cloneDocument(document)
	}
	return result
}

func cloneDocument(document Document) Document {
	document.Plan = clonePlan(document.Plan)
	if document.TargetBindings != nil {
		document.TargetBindings = append([]TargetBinding{}, document.TargetBindings...)
		for index := range document.TargetBindings {
			document.TargetBindings[index] = cloneTargetBinding(document.TargetBindings[index])
		}
	}
	return document
}

func cloneTargetBinding(binding TargetBinding) TargetBinding {
	binding.Model.Capabilities = append([]string(nil), binding.Model.Capabilities...)
	return binding
}

func clonePlans(plans []domain.Plan) []domain.Plan {
	result := make([]domain.Plan, len(plans))
	for index, plan := range plans {
		result[index] = clonePlan(plan)
	}
	return result
}

func clonePlan(plan domain.Plan) domain.Plan {
	if plan.ModelIDs != nil {
		plan.ModelIDs = append([]string{}, plan.ModelIDs...)
	}
	if plan.ChannelIDs != nil {
		plan.ChannelIDs = append([]string{}, plan.ChannelIDs...)
	}
	plan.Cases = append([]domain.CaseRevisionRef(nil), plan.Cases...)
	plan.SLA.Thresholds = cloneThresholds(plan.SLA.Thresholds)
	return plan
}

func cloneThresholds(values map[string]float64) map[string]float64 {
	if values == nil {
		return nil
	}
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
