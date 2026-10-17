package jobhost

import (
	"context"
	"sync"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type MaintenanceStore interface {
	Read(context.Context) (*jobcontract.MaintenanceRecord, error)
	Write(context.Context, jobcontract.MaintenanceRecord) error
	// Sync establishes durability after an ambiguous write. Read visibility alone
	// cannot confirm a failed rename-directory fsync.
	Sync(context.Context) error
}

type Maintenance struct {
	host   *Host
	store  MaintenanceStore
	mu     sync.Mutex
	record *jobcontract.MaintenanceRecord
}

type MaintenanceResult struct {
	Record jobcontract.MaintenanceRecord `json:"record"`
	Status Status                        `json:"status"`
}

func NewMaintenance(ctx context.Context, host *Host, store MaintenanceStore) (*Maintenance, error) {
	invalid := ctx == nil || host == nil || nilDependency(store)
	if invalid {
		return nil, ErrInvalid
	}
	if host.target.Kind != jobcontract.TargetRemote {
		return nil, ErrInvalid
	}
	record, err := store.Read(ctx)
	if err != nil {
		return nil, err
	}
	if record != nil {
		if err := record.Validate(host.installationID); err != nil {
			return nil, err
		}
		copy := *record
		record = &copy
		if record.Paused {
			host.mu.Lock()
			host.maintenanceID = record.OperationID
			host.mu.Unlock()
		}
	}
	return &Maintenance{host: host, store: store, record: record}, nil
}

func (maintenance *Maintenance) Owns(host *Host) bool {
	return maintenance != nil && maintenance.host == host
}

func (maintenance *Maintenance) Apply(
	ctx context.Context,
	command jobcontract.MaintenanceCommand,
) (MaintenanceResult, error) {
	if maintenance == nil || ctx == nil {
		return MaintenanceResult{}, ErrInvalid
	}
	if err := command.Validate(); err != nil {
		return MaintenanceResult{}, err
	}
	maintenance.mu.Lock()
	defer maintenance.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return MaintenanceResult{}, err
	}
	previous := maintenance.record
	if previous != nil && previous.OperationID == command.OperationID {
		paused := command.Action == jobcontract.MaintenancePause
		if previous.Paused == paused {
			return MaintenanceResult{Record: *previous, Status: maintenance.host.Status()}, nil
		}
		if !paused && previous.Paused {
			return maintenance.change(command)
		}
		return MaintenanceResult{}, ErrConflict
	}
	unknownResume := command.Action == jobcontract.MaintenanceResume
	ownedByAnother := previous != nil && previous.Paused
	if unknownResume || ownedByAnother {
		return MaintenanceResult{}, ErrConflict
	}
	return maintenance.change(command)
}

func (maintenance *Maintenance) change(command jobcontract.MaintenanceCommand) (MaintenanceResult, error) {
	host := maintenance.host
	paused := command.Action == jobcontract.MaintenancePause
	host.mu.Lock()
	if err := host.available(); err != nil {
		host.mu.Unlock()
		return MaintenanceResult{}, err
	}
	if paused {
		if host.maintenanceID != "" && host.maintenanceID != command.OperationID {
			host.mu.Unlock()
			return MaintenanceResult{}, ErrConflict
		}
		host.maintenanceID = command.OperationID
	} else if host.maintenanceID != command.OperationID {
		host.mu.Unlock()
		return MaintenanceResult{}, ErrConflict
	}
	host.mu.Unlock()
	at := host.clock.Now().UTC()
	if maintenance.record != nil && at.Before(maintenance.record.ChangedAt) {
		at = maintenance.record.ChangedAt
	}
	record := jobcontract.MaintenanceRecord{
		Contract: jobcontract.CurrentContract, NodeInstallationID: host.installationID,
		OperationID: command.OperationID, Paused: paused, ChangedAt: at,
	}
	ctx, cancel := context.WithTimeout(context.Background(), storageTimeout)
	err := maintenance.store.Write(ctx, record)
	cancel()
	if err != nil {
		confirm, cancel := context.WithTimeout(context.Background(), storageTimeout)
		stored, readErr := maintenance.store.Read(confirm)
		same := readErr == nil && stored != nil
		if same {
			same = *stored == record
		}
		if same {
			same = maintenance.store.Sync(confirm) == nil
		}
		cancel()
		if !same {
			host.MarkUnavailable()
			return MaintenanceResult{}, ErrUnavailable
		}
	}
	maintenance.record = &record
	host.mu.Lock()
	if !paused {
		if host.maintenanceID != command.OperationID {
			host.mu.Unlock()
			return MaintenanceResult{}, ErrConflict
		}
		host.maintenanceID = ""
	}
	status := host.statusLocked()
	host.mu.Unlock()
	return MaintenanceResult{Record: record, Status: status}, nil
}
