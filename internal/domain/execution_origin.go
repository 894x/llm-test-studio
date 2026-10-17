package domain

import (
	"encoding/hex"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ExecutionOrigin is the immutable executor identity recorded at admission.
// Kind is explicit for both local and remote execution; no missing identity
// can be interpreted as the current process or a local executor.
type ExecutionOrigin struct {
	Kind           string `json:"kind"`
	NodeID         string `json:"node_id"`
	InstallationID string `json:"installation_id"`
	EngineBuildID  string `json:"engine_build_id"`
	RunnerVersion  string `json:"runner_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
}

func (origin ExecutionOrigin) Validate() error {
	validKind := origin.Kind == "local" || origin.Kind == "remote"
	validIdentity := IsUUID(origin.NodeID) && IsUUID(origin.InstallationID)
	validBuild := validExecutionBuild(origin.EngineBuildID)
	if !validKind || !validIdentity || !validBuild {
		return errors.New("execution origin requires an explicit executor identity and build")
	}
	validVersion := validExecutionOriginLabel(origin.RunnerVersion, 128)
	validPlatform := validExecutionOriginLabel(origin.OS, 32) && validExecutionOriginLabel(origin.Arch, 32)
	if !validVersion || !validPlatform {
		return errors.New("execution origin requires executor version, os, and arch")
	}
	return nil
}

func validExecutionBuild(value string) bool {
	if len(value) != 64 {
		return false
	}
	build, err := hex.DecodeString(value)
	return err == nil && hex.EncodeToString(build) == value
}

func validExecutionOriginLabel(value string, limit int) bool {
	validSize := value != "" && len(value) <= limit
	validText := utf8.ValidString(value) && strings.TrimSpace(value) == value
	return validSize && validText && !strings.ContainsFunc(value, unicode.IsControl)
}
