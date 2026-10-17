package runs

import (
	"fmt"
	"github.com/894x/llm-test-studio/internal/domain"
)

// PreparedRun is the complete non-secret input accepted by an execution host.
// Credentials are transferred separately as a lease and cannot be serialized.
type PreparedRun struct {
	Meta     domain.EntityMeta  `json:"meta"`
	PlanID   string             `json:"plan_id"`
	Snapshot domain.RunSnapshot `json:"snapshot"`
}

func freezePreparedRun(meta domain.EntityMeta, planID string, snapshot domain.RunSnapshot) (PreparedRun, error) {
	run, err := domain.NewRun(meta, planID, snapshot)
	if err != nil {
		return PreparedRun{}, fmt.Errorf("freeze prepared run: %w", err)
	}
	return PreparedRun{Meta: meta, PlanID: planID, Snapshot: run.Snapshot()}, nil
}
