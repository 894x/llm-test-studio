package domain

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

const CurrentEntitySchemaVersion = 1

type EntityMeta struct {
	ID            string    `json:"id"`
	SchemaVersion int       `json:"schema_version"`
	Revision      uint64    `json:"revision"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func NewEntityMeta(now time.Time) (EntityMeta, error) {
	if now.IsZero() {
		return EntityMeta{}, errors.New("entity timestamp must not be zero")
	}
	id, err := newUUID()
	if err != nil {
		return EntityMeta{}, err
	}
	return EntityMeta{
		ID: id, SchemaVersion: CurrentEntitySchemaVersion, Revision: 1,
		CreatedAt: now.UTC(), UpdatedAt: now.UTC(),
	}, nil
}

func (meta EntityMeta) Validate() error {
	if !IsUUID(meta.ID) {
		return fmt.Errorf("entity id %q is not a canonical UUID", meta.ID)
	}
	if meta.SchemaVersion != CurrentEntitySchemaVersion {
		return fmt.Errorf("unsupported entity schema version %d", meta.SchemaVersion)
	}
	if meta.Revision < 1 {
		return errors.New("entity revision must be positive")
	}
	if meta.CreatedAt.IsZero() || meta.UpdatedAt.IsZero() {
		return errors.New("entity timestamps must not be zero")
	}
	if !timestampIsUTC(meta.CreatedAt) || !timestampIsUTC(meta.UpdatedAt) {
		return errors.New("entity timestamps must be UTC")
	}
	if meta.UpdatedAt.Before(meta.CreatedAt) {
		return errors.New("entity update timestamp precedes creation")
	}
	return nil
}

func (meta EntityMeta) NextRevision(now time.Time) (EntityMeta, error) {
	if err := meta.Validate(); err != nil {
		return EntityMeta{}, err
	}
	if now.IsZero() || now.Before(meta.UpdatedAt) {
		return EntityMeta{}, errors.New("revision timestamp must not precede the current update")
	}
	if meta.Revision == math.MaxUint64 {
		return EntityMeta{}, errors.New("entity revision cannot advance beyond uint64 maximum")
	}
	meta.Revision++
	meta.UpdatedAt = now.UTC()
	return meta, nil
}

func timestampIsUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

func IsUUID(value string) bool {
	if value == "00000000-0000-0000-0000-000000000000" {
		return false
	}
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	hexValue := strings.ReplaceAll(value, "-", "")
	if len(hexValue) != 32 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(hexValue)
	return err == nil
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
