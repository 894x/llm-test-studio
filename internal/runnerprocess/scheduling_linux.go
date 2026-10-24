package runnerprocess

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
)

type schedulingState interface {
	ReadPrivate(string, int64) ([]byte, error)
	ReplacePrivate(string, []byte) error
	SyncPrivate(string) error
}

type schedulingStore struct {
	state  schedulingState
	config runnerconfig.Config
}

func (store schedulingStore) Save(ctx context.Context, expected, limit uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	document, err := store.state.ReadPrivate(ConfigName, runnerconfig.MaxConfigBytes)
	if err != nil {
		return err
	}
	defer clear(document)
	config, err := runnerconfig.Decode(bytes.NewReader(document))
	if err != nil {
		return err
	}
	defer config.Clear()
	expectedConfig := store.config
	expectedConfig.Budget.MaxActiveJobs = expected
	desiredConfig := store.config
	desiredConfig.Budget.MaxActiveJobs = limit
	if reflect.DeepEqual(config, desiredConfig) {
		return store.state.SyncPrivate(ConfigName)
	}
	if !reflect.DeepEqual(config, expectedConfig) {
		return jobhost.ErrConflict
	}
	config.Budget.MaxActiveJobs = limit
	if err := config.Validate(); err != nil {
		return err
	}
	next, err := json.Marshal(config)
	if err != nil {
		return err
	}
	defer clear(next)
	writeErr := store.state.ReplacePrivate(ConfigName, next)
	// A directory sync failure can occur after rename. Confirm the same bytes
	// and durability without issuing another logical configuration change.
	actual, readErr := store.state.ReadPrivate(ConfigName, runnerconfig.MaxConfigBytes)
	defer clear(actual)
	if readErr != nil || !bytes.Equal(actual, next) {
		return errors.Join(writeErr, readErr, jobhost.ErrUnavailable)
	}
	return store.state.SyncPrivate(ConfigName)
}
