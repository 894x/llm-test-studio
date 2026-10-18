// Package plancatalog owns the shareable one-plan-per-file authored catalog.
package plancatalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/fileconfig"
)

const CurrentFileSchemaVersion = 2

var (
	ErrInvalid  = errors.New("plan catalog: invalid input")
	ErrNotFound = errors.New("plan catalog: plan not found")
	ErrConflict = errors.New("plan catalog: revision conflict")
	ErrCorrupt  = errors.New("plan catalog: corrupt file")
)

// Document stores authored references and configuration; Run snapshots own resolved execution data.
type Document struct {
	FileSchemaVersion int `json:"file_schema_version"`
	domain.Plan
}

// authoredDocument is the explicit v1/v2 boundary envelope. The top-level
// Performance field is accepted only by the v1 adapter and never written.
type authoredDocument struct {
	FileSchemaVersion int `json:"file_schema_version"`
	domain.EntityMeta
	Name        string                         `json:"name"`
	Protocol    domain.Protocol                `json:"protocol"`
	Seed        uint64                         `json:"seed"`
	Entries     []domain.PlanEntry             `json:"entries"`
	Performance *domain.PerformancePlanProfile `json:"performance,omitempty"`
}

func (document Document) MarshalJSON() ([]byte, error) {
	return json.Marshal(authoredDocument{
		FileSchemaVersion: document.FileSchemaVersion,
		EntityMeta:        document.EntityMeta,
		Name:              document.Name,
		Protocol:          document.Protocol, Seed: document.Seed, Entries: document.Entries,
	})
}

func (document *Document) UnmarshalJSON(raw []byte) error {
	if document == nil {
		return errors.New("nil plan document")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var decoded authoredDocument
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("plan document must contain one object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if decoded.FileSchemaVersion == 1 {
		var entries []map[string]json.RawMessage
		if err := json.Unmarshal(fields["entries"], &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			for _, key := range []string{"name", "on_failure", "performance"} {
				if _, exists := entry[key]; exists {
					return fmt.Errorf("version 1 plan entry cannot contain %s", key)
				}
			}
			var kind string
			if err := json.Unmarshal(entry["target_kind"], &kind); err != nil {
				return err
			}
			if kind != "case" && kind != "suite" {
				return errors.New("unsupported version 1 plan target")
			}
		}
	} else if _, exists := fields["performance"]; exists {
		return errors.New("performance belongs to a plan entry")
	}
	if decoded.FileSchemaVersion != 1 && decoded.FileSchemaVersion != CurrentFileSchemaVersion {
		return fmt.Errorf("unsupported plan file schema version %d", decoded.FileSchemaVersion)
	}
	if decoded.Performance != nil {
		if len(decoded.Entries) != 0 {
			return errors.New("legacy performance plan contains entries")
		}
		decoded.Entries = []domain.PlanEntry{legacyPerformanceEntry(decoded.ID, decoded.Name, *decoded.Performance)}
	}
	*document = Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan: domain.Plan{
			EntityMeta: decoded.EntityMeta,
			Name:       decoded.Name, Protocol: decoded.Protocol, Seed: decoded.Seed, Entries: decoded.Entries,
		},
	}
	return nil
}

// Validate checks the authored shape without resolving referenced execution data.
func (document Document) Validate() error {
	if document.FileSchemaVersion != CurrentFileSchemaVersion {
		return fmt.Errorf("unsupported plan file schema version %d", document.FileSchemaVersion)
	}
	return document.Plan.Validate()
}

type Service struct {
	cache    fileconfig.ParsedCache[Document]
	builtin  map[string]Document
	root     string
	lockPath string
	mu       sync.Mutex
}

type Option func(*Service) error

// WithBuiltin adds immutable authored plans to the same catalog as user plans.
func WithBuiltin(bundle fs.FS) Option {
	return func(service *Service) error {
		if bundle == nil {
			return ErrInvalid
		}
		entries, err := fs.ReadDir(bundle, ".")
		if err != nil {
			return fmt.Errorf("read built-in plans: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			id := strings.TrimSuffix(entry.Name(), ".json")
			raw, err := fs.ReadFile(bundle, entry.Name())
			if err != nil {
				return fmt.Errorf("read built-in plan %q: %w", id, err)
			}
			document, err := decodeDocument(raw, id)
			if err != nil {
				return err
			}
			document.Builtin = true
			service.builtin[id] = document
		}
		return nil
	}
}

func New(root string, options ...Option) (*Service, error) {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return nil, ErrInvalid
	}
	root = filepath.Clean(root)
	service := &Service{root: root, lockPath: root + ".lock", builtin: map[string]Document{}}
	for _, option := range options {
		if err := option(service); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// List returns only Plan projections for existing catalog.Repository callers.
func (service *Service) List(ctx context.Context) ([]domain.Plan, error) {
	documents, err := service.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	plans := make([]domain.Plan, len(documents))
	for index, document := range documents {
		plans[index] = document.Plan
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
	return documents, nil
}

// Get returns only the Plan projection for existing catalog.Repository callers.
func (service *Service) Get(ctx context.Context, id string) (domain.Plan, error) {
	document, err := service.GetDocument(ctx, id)
	if err != nil {
		return domain.Plan{}, err
	}
	return document.Plan, nil
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
	return document, nil
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

// Create stores a Plan without resolving its execution references.
func (service *Service) Create(ctx context.Context, plan domain.Plan) error {
	return service.CreateDocument(ctx, Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan:              plan,
	})
}

func (service *Service) CreateDocument(ctx context.Context, document Document) error {
	if service == nil || ctx == nil || document.Validate() != nil || document.Revision != 1 {
		return ErrInvalid
	}
	if _, builtin := service.builtin[document.ID]; builtin {
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

// Update stores a new authored Plan revision.
func (service *Service) Update(ctx context.Context, expectedRevision uint64, plan domain.Plan) error {
	return service.UpdateDocument(ctx, expectedRevision, Document{
		FileSchemaVersion: CurrentFileSchemaVersion,
		Plan:              plan,
	})
}

func (service *Service) UpdateDocument(ctx context.Context, expectedRevision uint64, document Document) error {
	if service == nil || ctx == nil || expectedRevision == 0 || document.Validate() != nil || document.Revision != expectedRevision+1 {
		return ErrInvalid
	}
	if _, builtin := service.builtin[document.ID]; builtin {
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
	if _, builtin := service.builtin[id]; builtin {
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
		entries = []os.DirEntry{}
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plan directory: %w", err)
	}
	documents := make([]Document, 0, len(entries))
	seen := make(map[string]struct{}, len(entries)+len(service.builtin))
	for id, document := range service.builtin {
		documents = append(documents, cloneDocument(document))
		seen[id] = struct{}{}
	}
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
	if document, builtin := service.builtin[id]; builtin {
		if _, err := os.Lstat(service.path(id)); !errors.Is(err, os.ErrNotExist) {
			return Document{}, fmt.Errorf("%w: built-in plan id %q is reserved", ErrCorrupt, id)
		}
		return cloneDocument(document), nil
	}
	raw, err := os.ReadFile(service.path(id))
	if errors.Is(err, os.ErrNotExist) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, fmt.Errorf("read plan file: %w", err)
	}
	return service.cache.Read(
		id,
		raw,
		func() (Document, error) { return decodeDocument(raw, id) },
		cloneDocument,
	)
}

func decodeDocument(raw []byte, id string) (Document, error) {
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
	// Old files are decoded in memory only. Before an explicit edit replaces a
	// version 1 document, keep and verify its original bytes outside the catalog.
	previous, readErr := os.ReadFile(service.path(document.ID))
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if readErr == nil {
		var header struct {
			Version int `json:"file_schema_version"`
		}
		if err := json.Unmarshal(previous, &header); err != nil {
			return err
		}
		if header.Version == 1 {
			backup := filepath.Join(service.root+".v1-backups", fmt.Sprintf("%s-%x.json", document.ID, sha256.Sum256(previous)))
			if err := fileconfig.WriteAtomically(ctx, backup, previous); err != nil {
				return fmt.Errorf("backup legacy plan: %w", err)
			}
			verified, err := os.ReadFile(backup)
			if err != nil || !bytes.Equal(verified, previous) {
				return errors.New("legacy plan backup verification failed")
			}
		}
	}
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

func cloneDocument(document Document) Document {
	document.Plan = clonePlan(document.Plan)
	return document
}

func clonePlan(plan domain.Plan) domain.Plan {
	if plan.Entries != nil {
		entries := make([]domain.PlanEntry, len(plan.Entries))
		for index, entry := range plan.Entries {
			entries[index] = entry
			if entry.Performance != nil {
				profile := *entry.Performance
				entries[index].Performance = &profile
			}
			entries[index].Parameters = cloneRawMessages(entry.Parameters)
			entries[index].SLA.Thresholds = cloneThresholds(entry.SLA.Thresholds)
		}
		plan.Entries = entries
	}
	return plan
}

func cloneRawMessages(values map[string]json.RawMessage) map[string]json.RawMessage {
	if values == nil {
		return nil
	}
	result := make(map[string]json.RawMessage, len(values))
	for key, value := range values {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
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
