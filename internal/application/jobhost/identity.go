package jobhost

import "github.com/894x/llm-test-studio/internal/jobcontract"

type Identity struct {
	Target         jobcontract.ExecutionTarget
	InstallationID string
	EngineBuildID  string
	Policy         Policy
}

// Identity returns immutable, non-secret admission identity and resource policy.
func (host *Host) Identity() Identity {
	if host == nil {
		return Identity{}
	}
	return Identity{
		Target: host.target, InstallationID: host.installationID,
		EngineBuildID: host.buildID, Policy: host.policy,
	}
}
