// Package remotenodes owns the desktop's local node configuration and credential lifecycle.
package remotenodes

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

const (
	CurrentFormat     = 1
	MaxNodes          = 64
	MaxCredentialRefs = 1024
	MaxFileBytes      = 4 << 20
)

var (
	ErrInvalid           = errors.New("invalid_remote_node_configuration")
	ErrUnsupported       = errors.New("remote_node_configuration_upgrade_required")
	ErrConflict          = errors.New("remote_node_revision_conflict")
	ErrNotFound          = errors.New("remote_node_not_found")
	ErrDuplicate         = errors.New("remote_installation_already_configured")
	ErrRePairRequired    = errors.New("remote_node_connection_identity_is_bound")
	ErrReferenced        = errors.New("remote_node_still_referenced")
	ErrUsageUnknown      = errors.New("remote_node_usage_unavailable")
	ErrCommitUnconfirmed = errors.New("remote_node_configuration_commit_unconfirmed")
	ErrCredential        = errors.New("remote_node_credential_unavailable")
	ErrBusy              = errors.New("remote_node_checks_busy")
)

type AuthenticationKind string

const (
	AuthenticationPassword   AuthenticationKind = "password"
	AuthenticationPrivateKey AuthenticationKind = "private_key"
)

type Authentication struct {
	Kind          AuthenticationKind `json:"kind"`
	PasswordRef   string             `json:"password_ref"`
	PrivateKeyRef string             `json:"private_key_ref"`
	PassphraseRef string             `json:"passphrase_ref"`
}

type HostKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
}

type Binding struct {
	Identity runnerinstall.Identity `json:"identity"`
	TokenRef string                 `json:"token_ref"`
}

type Installation struct {
	Request            runnerinstall.Request `json:"request"`
	Release            string                `json:"release"`
	Arch               string                `json:"arch"`
	Stage              string                `json:"stage"`
	ErrorCode          string                `json:"error_code"`
	TemporaryDirectory string                `json:"temporary_directory"`
	CleanupPending     bool                  `json:"cleanup_pending"`
}

type Observation struct {
	CheckedAt       time.Time               `json:"checked_at"`
	Status          string                  `json:"status"`
	ProposedHostKey *HostKey                `json:"proposed_host_key"`
	System          *remotessh.SystemInfo   `json:"system"`
	Runner          *jobcontract.RunnerInfo `json:"runner"`
}

type Node struct {
	domain.EntityMeta
	Name           string         `json:"name"`
	Region         string         `json:"region"`
	Host           string         `json:"host"`
	Port           uint16         `json:"port"`
	User           string         `json:"user"`
	Enabled        bool           `json:"enabled"`
	Authentication Authentication `json:"authentication"`
	HostKey        *HostKey       `json:"host_key"`
	Binding        *Binding       `json:"binding"`
	Installation   *Installation  `json:"installation"`
	Observation    *Observation   `json:"observation"`
}

// Selection.NodeID is a local configuration ID. JobSpec.target.node_id instead
// uses the daemon's shared ID from Binding.Identity.NodeID.
type Selection struct {
	Revision uint64                 `json:"revision"`
	Kind     jobcontract.TargetKind `json:"kind"`
	NodeID   string                 `json:"node_id"`
}

type Configuration struct {
	Format         int       `json:"format"`
	Revision       uint64    `json:"revision"`
	Default        Selection `json:"default"`
	Nodes          []Node    `json:"nodes"`
	CredentialRefs []string  `json:"credential_refs"`
}

func emptyConfiguration() Configuration {
	return Configuration{
		Format: CurrentFormat, Revision: 1,
		Default: Selection{Revision: 1, Kind: jobcontract.TargetLocal},
		Nodes:   []Node{}, CredentialRefs: []string{},
	}
}

func (node Node) Endpoint() remotessh.Endpoint {
	endpoint := remotessh.Endpoint{Host: node.Host, Port: node.Port, User: node.User}
	if node.HostKey != nil {
		endpoint.Fingerprint, endpoint.HostKeyType = node.HostKey.Fingerprint, node.HostKey.Type
	}
	return endpoint
}

func (auth Authentication) refs() []string {
	result := []string{}
	for _, ref := range []string{auth.PasswordRef, auth.PrivateKeyRef, auth.PassphraseRef} {
		if ref != "" {
			result = append(result, ref)
		}
	}
	return result
}

func (node Node) refs() []string {
	result := node.Authentication.refs()
	if node.Binding != nil {
		result = append(result, node.Binding.TokenRef)
	}
	return result
}

func (auth Authentication) validate() error {
	switch auth.Kind {
	case AuthenticationPassword:
		valid := validRef(auth.PasswordRef, domain.CredentialSSHPassword) &&
			auth.PrivateKeyRef == "" && auth.PassphraseRef == ""
		if valid {
			return nil
		}
	case AuthenticationPrivateKey:
		validPassphrase := auth.PassphraseRef == "" || validRef(auth.PassphraseRef, domain.CredentialSSHPassphrase)
		valid := validRef(auth.PrivateKeyRef, domain.CredentialSSHPrivateKey) &&
			auth.PasswordRef == "" && validPassphrase
		if valid {
			return nil
		}
	}
	return ErrInvalid
}

func (node Node) Validate() error {
	validMeta := node.EntityMeta.Validate() == nil && node.Revision < math.MaxUint64
	validLabels := safeLabel(node.Name, 128, false) && safeLabel(node.Region, 128, true)
	validIdentity := validMeta && validLabels
	if !validIdentity || node.Endpoint().Validate() != nil {
		return ErrInvalid
	}
	if node.HostKey != nil && node.HostKey.Fingerprint == "" {
		return ErrInvalid
	}
	if err := node.Authentication.validate(); err != nil {
		return err
	}
	if node.Binding != nil {
		validBinding := node.Binding.Identity.Validate() == nil &&
			validRef(node.Binding.TokenRef, domain.CredentialRunnerToken) && node.HostKey != nil
		if !validBinding {
			return ErrInvalid
		}
	}
	if attempt := node.Installation; attempt != nil {
		validArtifact := runnerartifacts.ValidRelease(attempt.Release) && (attempt.Arch == "amd64" || attempt.Arch == "arm64")
		validStage := attempt.Stage == "uploading" ||
			(runnerinstall.Progress{OperationID: attempt.Request.OperationID, Stage: attempt.Stage}).Validate() == nil
		validError := attempt.ErrorCode == "" || attempt.ErrorCode == installationCode(installationCause(attempt.ErrorCode))
		validCleanup := attempt.TemporaryDirectory == "" && !attempt.CleanupPending
		if attempt.TemporaryDirectory != "" {
			validCleanup = remotessh.ValidBootstrapDirectory(attempt.TemporaryDirectory)
		}
		validAttempt := validArtifact && validStage && validError && validCleanup
		if !validAttempt || attempt.Request.Validate() != nil {
			return ErrInvalid
		}
	}
	if observation := node.Observation; observation != nil {
		statuses := []string{"checking", "host_unverified", "host_changed", "authentication_failed", "offline",
			"unsupported", "installing", "ready", "maintenance", "unhealthy", "pairing_required", "artifact_required",
			"identity_changed", "credential_unavailable", "failed"}
		_, offset := observation.CheckedAt.Zone()
		validTime := !observation.CheckedAt.IsZero() && offset == 0
		if !validTime || !slices.Contains(statuses, observation.Status) {
			return ErrInvalid
		}
		if observation.Runner != nil && observation.Runner.Validate() != nil {
			return ErrInvalid
		}
		if proposed := observation.ProposedHostKey; proposed != nil {
			endpoint := node.Endpoint()
			endpoint.Fingerprint, endpoint.HostKeyType = proposed.Fingerprint, proposed.Type
			if proposed.Fingerprint == "" || endpoint.Validate() != nil {
				return ErrInvalid
			}
		}
		if observation.Status == "ready" {
			if unresolvedInstallation(node.Installation) {
				return ErrInvalid
			}
			ready := node.Binding != nil && observation.Runner != nil && observation.Runner.Ready
			if !ready {
				return ErrInvalid
			}
			info, identity := observation.Runner, node.Binding.Identity
			supported := info.OS == "linux" && (info.Arch == "amd64" || info.Arch == "arm64")
			if !supported {
				return ErrInvalid
			}
			if info.NodeID != identity.NodeID || info.NodeInstallationID != identity.InstallationID {
				return ErrInvalid
			}
		}
	}
	return nil
}

func unresolvedInstallation(attempt *Installation) bool {
	return attempt != nil && (attempt.Stage != "completed" || attempt.ErrorCode != "" || attempt.CleanupPending)
}

func (config Configuration) Validate() error {
	if config.Format != CurrentFormat {
		return ErrUnsupported
	}
	validRevisions := config.Revision > 0 && config.Revision < math.MaxUint64 &&
		config.Default.Revision > 0 && config.Default.Revision < math.MaxUint64
	validCollections := config.Nodes != nil && len(config.Nodes) <= MaxNodes &&
		config.CredentialRefs != nil && len(config.CredentialRefs) <= MaxCredentialRefs
	if !validRevisions || !validCollections {
		return ErrInvalid
	}
	registry := map[string]bool{}
	for index, value := range config.CredentialRefs {
		ref, err := credentials.ParseStoreRef(value)
		ordered := index == 0 || config.CredentialRefs[index-1] < value
		validRegistryRef := err == nil && ref.Scope() == "" && remotePurpose(ref.Purpose())
		if !validRegistryRef || !ordered {
			return ErrInvalid
		}
		registry[value] = true
	}
	ids, installations, activeRefs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, node := range config.Nodes {
		if node.Validate() != nil || ids[node.ID] {
			return ErrInvalid
		}
		ids[node.ID] = true
		if node.Binding != nil {
			id := node.Binding.Identity.InstallationID
			if installations[id] {
				return ErrDuplicate
			}
			installations[id] = true
		}
		for _, ref := range node.refs() {
			if !registry[ref] || activeRefs[ref] {
				return ErrInvalid
			}
			activeRefs[ref] = true
		}
	}
	target := jobcontract.ExecutionTarget{Kind: config.Default.Kind, NodeID: config.Default.NodeID}
	if target.Validate() != nil {
		return ErrInvalid
	}
	if target.Kind == jobcontract.TargetRemote && !ids[target.NodeID] {
		return ErrNotFound
	}
	return nil
}

func decodeConfiguration(document []byte) (Configuration, error) {
	var config Configuration
	if len(document) > MaxFileBytes || json.Unmarshal(document, &config) != nil {
		return config, ErrInvalid
	}
	if err := config.Validate(); err != nil {
		return config, err
	}
	canonical, _ := json.Marshal(config)
	var compact bytes.Buffer
	if json.Compact(&compact, document) != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return config, ErrInvalid
	}
	return config, nil
}

func validRef(value string, purpose domain.CredentialPurpose) bool {
	ref, err := credentials.ParseStoreRef(value)
	return err == nil && ref.Scope() == "" && ref.Purpose() == purpose
}

func remotePurpose(purpose domain.CredentialPurpose) bool {
	return slices.Contains([]domain.CredentialPurpose{domain.CredentialSSHPassword, domain.CredentialSSHPrivateKey,
		domain.CredentialSSHPassphrase, domain.CredentialRunnerToken}, purpose)
}

func safeLabel(value string, limit int, empty bool) bool {
	valid := (empty || value != "") && len(value) <= limit && utf8.ValidString(value) && strings.TrimSpace(value) == value
	return valid && !strings.ContainsFunc(value, unicode.IsControl)
}
