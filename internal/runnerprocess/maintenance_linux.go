package runnerprocess

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

const maintenanceName = "maintenance.json"

type maintenanceStore struct {
	state          *runnerstate.State
	installationID string
}

func (store maintenanceStore) Read(ctx context.Context) (*jobcontract.MaintenanceRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	document, err := store.state.ReadPrivate(maintenanceName, jobcontract.MaxCommandBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record jobcontract.MaintenanceRecord
	if err := json.Unmarshal(document, &record); err != nil {
		return nil, err
	}
	if err := record.Validate(store.installationID); err != nil {
		return nil, err
	}
	return &record, ctx.Err()
}

func (store maintenanceStore) Write(ctx context.Context, record jobcontract.MaintenanceRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(store.installationID); err != nil {
		return err
	}
	document, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := store.state.ReplacePrivate(maintenanceName, document); err != nil {
		return err
	}
	return ctx.Err()
}

func (store maintenanceStore) Sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.state.SyncPrivate(maintenanceName); err != nil {
		return err
	}
	return ctx.Err()
}
