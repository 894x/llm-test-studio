package jobhost

import (
	"context"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type TaskConcurrencyStore interface {
	Save(context.Context, uint32, uint32) error
}

type Scheduling struct {
	host  *Host
	store TaskConcurrencyStore
}

func NewScheduling(host *Host, store TaskConcurrencyStore) (*Scheduling, error) {
	if host == nil || nilDependency(store) {
		return nil, ErrInvalid
	}
	return &Scheduling{host: host, store: store}, nil
}

func (scheduling *Scheduling) Owns(host *Host) bool {
	return scheduling != nil && scheduling.host == host
}

func (scheduling *Scheduling) Apply(ctx context.Context, command jobcontract.TaskConcurrencyCommand) error {
	if ctx == nil || scheduling == nil {
		return ErrInvalid
	}
	if err := command.Validate(); err != nil {
		return err
	}
	host := scheduling.host
	host.mu.Lock()
	defer host.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := host.available(); err != nil {
		return err
	}
	if host.maintenanceID != "" {
		return ErrMaintenance
	}
	if command.NodeID != host.origin.NodeID || command.NodeInstallationID != host.installationID {
		return ErrIdentity
	}
	if command.MaxActiveJobs == host.policy.MaxActiveJobs {
		return nil
	}
	if command.ExpectedMaxActiveJobs != host.policy.MaxActiveJobs {
		return ErrConflict
	}
	// Existing clients require active <= max_active_jobs. Refuse a decrease
	// below the live count rather than interrupting accepted work.
	if command.MaxActiveJobs < host.active {
		return ErrCapacity
	}
	if err := scheduling.store.Save(ctx, host.policy.MaxActiveJobs, command.MaxActiveJobs); err != nil {
		return err
	}
	host.policy.MaxActiveJobs = command.MaxActiveJobs
	host.dispatchLocked()
	return nil
}
