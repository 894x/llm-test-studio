package sqlite

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The baseline bytes come from the commits recorded in the catalog, not from
// the current schema. Never regenerate this file to accept an unknown database.
//
//go:embed migrations/operational_baselines.json
var operationalBaselinesJSON []byte

const operationalBaselineName = "0001_protocol_runtime"

type historicalObject struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Table string `json:"table"`
	DDL   string `json:"ddl"`
}

type historicalBaseline struct {
	Commit   string   `json:"commit"`
	Label    string   `json:"label"`
	Version  int      `json:"version"`
	Name     string   `json:"name"`
	Checksum string   `json:"checksum"`
	Objects  []string `json:"objects"`
	objects  []schemaObject
}

type migrationCatalog struct {
	Objects   map[string]historicalObject `json:"objects"`
	Baselines []historicalBaseline        `json:"baselines"`
}

func loadMigrationCatalog() (migrationCatalog, error) {
	var catalog migrationCatalog
	if err := json.Unmarshal(operationalBaselinesJSON, &catalog); err != nil {
		return catalog, errors.New("invalid embedded migration catalog")
	}
	if len(catalog.Baselines) != 10 {
		return catalog, errors.New("incomplete operational baseline catalog")
	}
	for index := range catalog.Baselines {
		baseline := &catalog.Baselines[index]
		baseline.objects = make([]schemaObject, 0, len(baseline.Objects))
		for _, key := range baseline.Objects {
			object, found := catalog.Objects[key]
			sum := sha256.Sum256([]byte(object.DDL))
			if !found || hex.EncodeToString(sum[:]) != key {
				return catalog, errors.New("migration catalog object checksum mismatch")
			}
			baseline.objects = append(baseline.objects, schemaObject{
				kind: object.Kind, name: object.Name, table: object.Table, ddl: object.DDL,
			})
		}
		validIdentity := baseline.Version == 1 && baseline.Name == operationalBaselineName
		if !validIdentity || schemaChecksum(baseline.Name, baseline.objects) != baseline.Checksum {
			return catalog, fmt.Errorf("migration catalog baseline %s checksum mismatch: %s != %s",
				baseline.Commit, schemaChecksum(baseline.Name, baseline.objects), baseline.Checksum)
		}
	}
	// New schema changes must add a migration, not silently change this target.
	latest := catalog.Baselines[len(catalog.Baselines)-1]
	if schemaChecksum(operationalBaselineName, operationalSchemaObjects) != latest.Checksum {
		return catalog, errors.New("current schema changed without a new versioned migration")
	}
	return catalog, nil
}

func (catalog migrationCatalog) baseline(checksum string) (historicalBaseline, bool) {
	for _, baseline := range catalog.Baselines {
		if baseline.Checksum == checksum {
			return baseline, true
		}
	}
	return historicalBaseline{}, false
}

func schemaChecksum(name string, objects []schemaObject) string {
	parts := make([]string, 0, len(objects)+1)
	parts = append(parts, name)
	for _, object := range objects {
		parts = append(parts, object.ddl)
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n-- object --\n")))
	return hex.EncodeToString(sum[:])
}
