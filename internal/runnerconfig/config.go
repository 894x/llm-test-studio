// Package runnerconfig defines the single current private runner configuration.
package runnerconfig

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

const MaxConfigBytes = 16 << 10

var ErrInvalid = errors.New("invalid_runner_configuration")

type Config struct {
	Contract         int                      `json:"contract"`
	NodeID           string                   `json:"node_id"`
	InstallationID   string                   `json:"installation_id"`
	ListenPort       uint16                   `json:"listen_port"`
	TokenSHA256      string                   `json:"token_sha256"`
	CommitmentKey    []byte                   `json:"commitment_key"`
	Budget           jobcontract.RunnerBudget `json:"budget"`
	MaxStorageBytes  uint64                   `json:"max_storage_bytes"`
	MinFreeBytes     uint64                   `json:"min_free_bytes"`
	ReservationBytes uint64                   `json:"reservation_bytes"`
}

func (Config) String() string          { return "runnerconfig.Config{redacted}" }
func (config Config) GoString() string { return config.String() }

// New creates one installation identity from a caller-owned node token. The
// token itself is never persisted; existing installations must load their file.
func New(token []byte) (Config, error) {
	if !validToken(token) {
		return Config{}, ErrInvalid
	}
	node, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return Config{}, err
	}
	installation, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		return Config{}, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Config{}, err
	}
	digest := sha256.Sum256(token)
	policy := jobhost.RemotePolicy()
	return Config{
		Contract: jobcontract.CurrentContract, NodeID: node.ID, InstallationID: installation.ID,
		ListenPort: 44917, TokenSHA256: hex.EncodeToString(digest[:]), CommitmentKey: key,
		Budget: jobcontract.RunnerBudget{
			MaxActiveJobs: policy.MaxActiveJobs, MaxQueuedJobs: policy.MaxQueuedJobs,
			MaxConcurrency: policy.MaxConcurrency, QueueTimeoutNS: int64(policy.QueueTimeout),
			ExecutionTimeoutNS: int64(policy.ExecutionTimeout),
		},
		MaxStorageBytes: 20 << 30, MinFreeBytes: 512 << 20, ReservationBytes: 128 << 20,
	}, nil
}

func (config Config) Validate() error {
	if config.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	validIdentity := domain.IsUUID(config.NodeID) && domain.IsUUID(config.InstallationID)
	validKeys := jobcontract.IsSHA256(config.TokenSHA256) && config.TokenSHA256 != strings.Repeat("0", 64) &&
		len(config.CommitmentKey) == 32 &&
		!bytes.Equal(config.CommitmentKey, make([]byte, 32))
	validStorage := config.MaxStorageBytes >= 512<<20 && config.MaxStorageBytes <= 1<<40 &&
		config.ReservationBytes >= 64<<20 && config.ReservationBytes <= config.MaxStorageBytes &&
		config.MinFreeBytes <= 1<<40
	valid := validIdentity && validKeys && validStorage && config.ListenPort >= 1024
	if !valid {
		return ErrInvalid
	}
	if err := config.Policy().Validate(); err != nil {
		return ErrInvalid
	}
	return nil
}

func (config Config) Policy() jobhost.Policy {
	budget := config.Budget
	return jobhost.Policy{
		MaxActiveJobs: budget.MaxActiveJobs, MaxQueuedJobs: budget.MaxQueuedJobs,
		MaxConcurrency: budget.MaxConcurrency, QueueTimeout: time.Duration(budget.QueueTimeoutNS),
		ExecutionTimeout: time.Duration(budget.ExecutionTimeoutNS),
	}
}

func (config Config) Clear() { clear(config.CommitmentKey) }

func validToken(token []byte) bool {
	if len(token) < 32 || len(token) > 128 {
		return false
	}
	for _, character := range token {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
		digit := character >= '0' && character <= '9'
		valid := letter || digit || character == '-' || character == '_'
		if !valid {
			return false
		}
	}
	return true
}

func Decode(reader io.Reader) (Config, error) {
	document, err := io.ReadAll(io.LimitReader(reader, MaxConfigBytes+1))
	if err != nil {
		return Config{}, ErrInvalid
	}
	defer clear(document)
	if len(document) == 0 || len(document) > MaxConfigBytes {
		return Config{}, ErrInvalid
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		config.Clear()
		return Config{}, ErrInvalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		config.Clear()
		return Config{}, ErrInvalid
	}
	if err := config.Validate(); err != nil {
		config.Clear()
		return Config{}, err
	}
	return config, nil
}
