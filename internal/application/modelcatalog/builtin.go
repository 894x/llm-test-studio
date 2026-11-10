package modelcatalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/google/uuid"

	"github.com/894x/llm-test-studio/internal/domain"
)

// Built-in string identities map deterministically to desktop UUIDs, without
// rewriting the source or any user-authored models.json. Version 1 retains its
// single protocol; version 2 uses the current model's protocols array.
type builtinDefinition struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Model         string   `json:"model"`
	Endpoint      string   `json:"endpoint"`
	Suites        []string `json:"suites"`
	Capabilities  []string `json:"capabilities"`
	Enabled       bool     `json:"enabled"`
}

type builtinDefinitionV1 struct {
	builtinDefinition
	Protocol domain.Protocol `json:"protocol"`
}

type builtinDefinitionV2 struct {
	builtinDefinition
	Protocols []domain.Protocol `json:"protocols"`
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
	var definition builtinDefinition
	if err := json.Unmarshal(raw, &definition); err != nil {
		return domain.Model{}, false, err
	}
	protocols := []domain.Protocol{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	switch definition.SchemaVersion {
	case 1:
		var legacy builtinDefinitionV1
		if err := decoder.Decode(&legacy); err != nil {
			return domain.Model{}, false, err
		}
		protocols = append(protocols, legacy.Protocol)
	case 2:
		var current builtinDefinitionV2
		if err := decoder.Decode(&current); err != nil {
			return domain.Model{}, false, err
		}
		protocols = append(protocols, current.Protocols...)
	default:
		return domain.Model{}, false, errors.New("unsupported built-in model definition version")
	}
	validIdentity := definition.ID != "" && definition.Model != "" && definition.DisplayName != ""
	if !validIdentity {
		return domain.Model{}, false, errors.New("incomplete built-in model definition")
	}
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte("llm-test-studio/builtin/model/"+definition.ID)).String()
	epoch := time.Unix(0, 0).UTC()
	model := domain.Model{
		EntityMeta: domain.EntityMeta{
			ID: id, Revision: 1, CreatedAt: epoch, UpdatedAt: epoch,
			SchemaVersion: domain.CurrentEntitySchemaVersion,
		},
		Builtin: true, Name: definition.Model, Protocols: protocols,
		Capabilities: append([]string{}, definition.Capabilities...),
	}
	return model, definition.Enabled, model.Validate()
}
