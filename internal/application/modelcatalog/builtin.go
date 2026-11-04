package modelcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"time"

	"github.com/google/uuid"

	"github.com/894x/llm-test-studio/internal/domain"
)

// builtinDefinitionV1 is the existing data/definitions/models file boundary.
// Its string identity maps deterministically to a desktop UUID, without
// rewriting the source or any user-authored models.json.
type builtinDefinitionV1 struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Model         string   `json:"model"`
	Protocol      string   `json:"protocol"`
	Endpoint      string   `json:"endpoint"`
	Suites        []string `json:"suites"`
	Capabilities  []string `json:"capabilities"`
	Enabled       bool     `json:"enabled"`
}

func WithBuiltin(bundle fs.FS) Option {
	return func(service *Service) error {
		if bundle == nil {
			return ErrInvalid
		}
		return fs.WalkDir(bundle, ".", func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || path.Ext(name) != ".json" {
				return nil
			}
			raw, err := fs.ReadFile(bundle, name)
			if err != nil {
				return err
			}
			model, enabled, err := decodeBuiltinDefinition(raw)
			if err != nil {
				return fmt.Errorf("invalid built-in model %q: %w", name, err)
			}
			if !enabled {
				return nil
			}
			if _, duplicate := service.builtin[model.ID]; duplicate {
				return fmt.Errorf("%w: duplicate built-in model %q", ErrCorrupt, model.ID)
			}
			service.builtin[model.ID] = model
			return nil
		})
	}
}

func decodeBuiltinDefinition(raw []byte) (domain.Model, bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var definition builtinDefinitionV1
	if err := decoder.Decode(&definition); err != nil {
		return domain.Model{}, false, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.Model{}, false, errors.New("model definition must contain one object")
	}
	validVersion := definition.SchemaVersion == 1
	validIdentity := definition.ID != "" && definition.Model != "" && definition.DisplayName != ""
	if !validVersion || !validIdentity {
		return domain.Model{}, false, errors.New("unsupported or incomplete built-in model definition")
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("llm-test-studio/builtin/model/"+definition.ID)).String()
	epoch := time.Unix(0, 0).UTC()
	model := domain.Model{
		EntityMeta: domain.EntityMeta{
			ID: id, Revision: 1, CreatedAt: epoch, UpdatedAt: epoch,
			SchemaVersion: domain.CurrentEntitySchemaVersion,
		},
		Builtin: true, Name: definition.Model, Protocols: []domain.Protocol{domain.Protocol(definition.Protocol)},
		Capabilities: append([]string{}, definition.Capabilities...),
	}
	return model, definition.Enabled, model.Validate()
}
