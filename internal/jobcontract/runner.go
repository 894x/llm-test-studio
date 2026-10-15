package jobcontract

import (
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/894x/llm-test-studio/internal/domain"
)

const ContractHeader = "X-LLM-Test-Contract"

type RunnerBudget struct {
	MaxActiveJobs      uint32 `json:"max_active_jobs"`
	MaxQueuedJobs      uint32 `json:"max_queued_jobs"`
	MaxConcurrency     uint32 `json:"max_concurrency"`
	QueueTimeoutNS     int64  `json:"queue_timeout_ns"`
	ExecutionTimeoutNS int64  `json:"execution_timeout_ns"`
}

type RunnerCapabilities struct {
	JobKinds  []Kind   `json:"job_kinds"`
	Protocols []string `json:"protocols"`
	Features  []string `json:"features"`
}

type RunnerInfo struct {
	Contract               int                `json:"contract"`
	NodeID                 string             `json:"node_id"`
	NodeInstallationID     string             `json:"node_installation_id"`
	EngineBuildID          string             `json:"engine_build_id"`
	RunnerVersion          string             `json:"runner_version"`
	OS                     string             `json:"os"`
	Arch                   string             `json:"arch"`
	Capabilities           RunnerCapabilities `json:"capabilities"`
	Budget                 RunnerBudget       `json:"budget"`
	Ready                  bool               `json:"ready"`
	Healthy                bool               `json:"healthy"`
	Active                 uint32             `json:"active"`
	Queued                 uint32             `json:"queued"`
	Pending                uint32             `json:"pending"`
	MaintenanceOperationID string             `json:"maintenance_operation_id"`
}

func (info RunnerInfo) Validate() error {
	if info.Contract != CurrentContract {
		return ErrUnsupported
	}
	identity := domain.IsUUID(info.NodeID) && domain.IsUUID(info.NodeInstallationID) && IsSHA256(info.EngineBuildID)
	if !identity || !validRunnerLabel(info.RunnerVersion, 128) ||
		!validRunnerLabel(info.OS, 32) || !validRunnerLabel(info.Arch, 32) {
		return invalid("runner.identity")
	}
	budget := info.Budget
	validJobs := budget.MaxActiveJobs >= 1 && budget.MaxActiveJobs <= 128 && budget.MaxQueuedJobs <= 1024
	validConcurrency := budget.MaxConcurrency >= 1 && budget.MaxConcurrency <= 65536
	validTimes := budget.QueueTimeoutNS > 0 && budget.QueueTimeoutNS <= int64(24*time.Hour) &&
		budget.ExecutionTimeoutNS > 0 && budget.ExecutionTimeoutNS <= int64(24*time.Hour)
	if !validJobs || !validConcurrency || !validTimes {
		return invalid("runner.budget")
	}
	if info.Active > budget.MaxActiveJobs || info.Queued > budget.MaxQueuedJobs {
		return invalid("runner.state")
	}
	validMaintenance := info.MaintenanceOperationID == "" || domain.IsUUID(info.MaintenanceOperationID)
	validReady := info.Ready == (info.Healthy && info.MaintenanceOperationID == "")
	validPending := uint64(info.Pending) >= uint64(info.Active)+uint64(info.Queued)
	if !validMaintenance || !validReady || !validPending {
		return invalid("runner.state")
	}
	capabilities := info.Capabilities
	if len(capabilities.JobKinds) == 0 || len(capabilities.JobKinds) > 3 {
		return invalid("runner.capabilities")
	}
	seen := map[Kind]bool{}
	for _, kind := range capabilities.JobKinds {
		valid := kind == KindRun || kind == KindPerformance || kind == KindComparison
		if !valid || seen[kind] {
			return invalid("runner.capabilities")
		}
		seen[kind] = true
	}
	if !validCapabilityNames(capabilities.Protocols, false) || !validCapabilityNames(capabilities.Features, true) {
		return invalid("runner.capabilities")
	}
	return nil
}

func (info RunnerInfo) Clone() RunnerInfo {
	info.Capabilities.JobKinds = slices.Clone(info.Capabilities.JobKinds)
	info.Capabilities.Protocols = slices.Clone(info.Capabilities.Protocols)
	info.Capabilities.Features = slices.Clone(info.Capabilities.Features)
	return info
}

func validRunnerLabel(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func validCapabilityNames(values []string, empty bool) bool {
	if values == nil || len(values) > 64 || (!empty && len(values) == 0) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range values {
		if !validRunnerLabel(value, 64) || seen[value] {
			return false
		}
		seen[value] = true
	}
	return true
}
