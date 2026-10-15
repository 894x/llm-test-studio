package runs

import "github.com/894x/llm-test-studio/internal/domain"

// PreparedRun is the complete non-secret input accepted by an execution host.
// Credentials are transferred separately as a lease and cannot be serialized.
type PreparedRun struct {
	Meta     domain.EntityMeta  `json:"meta"`
	PlanID   string             `json:"plan_id"`
	Snapshot domain.RunSnapshot `json:"snapshot"`
}
