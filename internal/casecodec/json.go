// Package casecodec encodes and decodes shareable Case documents while preserving
// the stable identities and content revisions used by catalogs and run snapshots.
package casecodec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
)

const CurrentFilesystemSchemaVersion = 1

type shareableFilesystemCase struct {
	SchemaVersion int                       `json:"schema_version"`
	Key           string                    `json:"key"`
	Name          string                    `json:"name"`
	Dimension     string                    `json:"dimension"`
	Protocol      domain.Protocol           `json:"protocol"`
	Enabled       bool                      `json:"enabled"`
	Default       bool                      `json:"default"`
	Severity      domain.CaseSeverity       `json:"severity"`
	ExecutionMode domain.CaseExecutionMode  `json:"execution_mode"`
	Definition    domain.TestCaseDefinition `json:"definition"`
}

type convertedCase struct {
	SemanticSHA256 string
	Key            string
	Name           string
	Dimension      string
	Protocol       domain.Protocol
	Enabled        bool
	Default        bool
	Severity       domain.CaseSeverity
	ExecutionMode  domain.CaseExecutionMode
	Definition     domain.TestCaseDefinition
}

// DecodeFilesystemCase accepts the current shareable case document only.
func DecodeFilesystemCase(sourcePath string, raw []byte) (domain.TestCase, error) {
	candidate, err := convertFilesystemCase(sourcePath, raw)
	if err != nil {
		return domain.TestCase{}, err
	}
	digest, err := hex.DecodeString(candidate.SemanticSHA256)
	if err != nil || len(digest) < 8 {
		return domain.TestCase{}, errors.New("decode filesystem case semantic revision")
	}
	revision := binary.BigEndian.Uint64(digest[:8]) & ((1 << 53) - 1)
	if revision == 0 {
		revision = 1
	}
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	entity := candidate.materialize(domain.EntityMeta{
		ID: stableCaseID(string(candidate.Protocol) + "/" + candidate.Key), SchemaVersion: domain.CurrentEntitySchemaVersion,
		Revision: revision, CreatedAt: stamp, UpdatedAt: stamp,
	})
	if err := casetypes.MustBuiltinRegistry().Validate(entity.Protocol, entity.Definition); err != nil {
		return domain.TestCase{}, err
	}
	return entity, nil
}

func EncodeFilesystemCase(testCase domain.TestCase) ([]byte, error) {
	if err := testCase.Validate(); err != nil {
		return nil, err
	}
	if err := casetypes.MustBuiltinRegistry().Validate(testCase.Protocol, testCase.Definition); err != nil {
		return nil, err
	}
	payload := shareableFilesystemCase{
		SchemaVersion: CurrentFilesystemSchemaVersion, Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
		Protocol: testCase.Protocol, Enabled: testCase.Enabled, Default: testCase.Default,
		Severity: testCase.Severity, ExecutionMode: testCase.ExecutionMode, Definition: testCase.Definition,
	}
	return json.MarshalIndent(payload, "", "  ")
}

func convertFilesystemCase(sourcePath string, raw []byte) (convertedCase, error) {
	if sourcePath == "" || strings.TrimSpace(sourcePath) != sourcePath {
		return convertedCase{}, errors.New("filesystem case source path is required")
	}
	var payload shareableFilesystemCase
	if err := decodeStrictJSON(raw, &payload); err != nil || payload.SchemaVersion != CurrentFilesystemSchemaVersion {
		return convertedCase{}, errors.New("unsupported filesystem case format; explicitly upgrade to schema_version 1")
	}
	entity := domain.TestCase{
		Key: payload.Key, Name: payload.Name, Dimension: payload.Dimension, Protocol: payload.Protocol,
		Enabled: payload.Enabled, Default: payload.Default, Severity: payload.Severity,
		ExecutionMode: payload.ExecutionMode, Definition: payload.Definition,
	}
	stamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	entity.EntityMeta = domain.EntityMeta{ID: "00000000-0000-5000-8000-000000000001", SchemaVersion: 1, Revision: 1, CreatedAt: stamp, UpdatedAt: stamp}
	if err := entity.Validate(); err != nil {
		return convertedCase{}, err
	}
	if err := casetypes.MustBuiltinRegistry().Validate(entity.Protocol, entity.Definition); err != nil {
		return convertedCase{}, err
	}
	semantic, err := canonicalJSON(raw)
	if err != nil {
		return convertedCase{}, err
	}
	candidate := convertedCase{
		SemanticSHA256: sha256Hex(semantic),
		Key:            entity.Key, Name: entity.Name, Dimension: entity.Dimension, Protocol: entity.Protocol,
		Enabled: entity.Enabled, Default: entity.Default, Severity: entity.Severity,
		ExecutionMode: entity.ExecutionMode, Definition: entity.Definition,
	}
	return candidate, nil
}

func (candidate convertedCase) materialize(meta domain.EntityMeta) domain.TestCase {
	return domain.TestCase{
		EntityMeta: meta, Key: candidate.Key, Name: candidate.Name, Dimension: candidate.Dimension,
		Protocol: candidate.Protocol, Enabled: candidate.Enabled, Default: candidate.Default,
		Severity: candidate.Severity, ExecutionMode: candidate.ExecutionMode, Definition: candidate.Definition,
	}
}

func sha256Hex(value []byte) string {
	sum := sha256.Sum256(value)
	return fmt.Sprintf("%x", sum[:])
}

func materializedHash(testCase domain.TestCase) (string, error) {
	payload := shareableFilesystemCase{
		SchemaVersion: CurrentFilesystemSchemaVersion, Key: testCase.Key, Name: testCase.Name, Dimension: testCase.Dimension,
		Protocol: testCase.Protocol, Enabled: testCase.Enabled, Default: testCase.Default,
		Severity: testCase.Severity, ExecutionMode: testCase.ExecutionMode, Definition: testCase.Definition,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	canonical, err := canonicalJSON(encoded)
	if err != nil {
		return "", err
	}
	return sha256Hex(canonical), nil
}

func MaterializedSHA256(testCase domain.TestCase) (string, error) {
	if err := testCase.Validate(); err != nil {
		return "", err
	}
	return materializedHash(testCase)
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("JSON must contain exactly one value")
		}
		return err
	}
	return nil
}

func canonicalJSON(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized any
	if err := decoder.Decode(&normalized); err != nil {
		return nil, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("JSON must contain exactly one value")
		}
		return nil, err
	}
	return json.Marshal(normalized)
}
