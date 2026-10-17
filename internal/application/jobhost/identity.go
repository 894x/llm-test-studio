package jobhost

import (
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type Identity struct {
	Target         jobcontract.ExecutionTarget
	InstallationID string
	EngineBuildID  string
	Origin         domain.ExecutionOrigin
	Policy         Policy
}

// Identity returns immutable, non-secret admission identity and resource policy.
func (host *Host) Identity() Identity {
	if host == nil {
		return Identity{}
	}
	return Identity{
		Target: host.target, InstallationID: host.installationID,
		EngineBuildID: host.buildID, Origin: host.origin, Policy: host.policy,
	}
}

// MarkUnavailable isolates dispatch after the process detects a storage fault.
func (host *Host) MarkUnavailable() {
	if host != nil {
		host.storageFailed()
	}
}
