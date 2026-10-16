package runnerinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"

	"github.com/894x/llm-test-studio/internal/runnerstate"
)

const activeUninstallName = "active-uninstall.json"

func readUninstallOperation(state *runnerstate.State, request UninstallRequest) (UninstallOperation, bool, error) {
	document, err := state.ReadPrivate(uninstallOperationName(request.OperationID), MaxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		return UninstallOperation{}, false, nil
	}
	if err != nil {
		return UninstallOperation{}, false, err
	}
	operation, err := DecodeUninstallOperation(document)
	if err != nil {
		return UninstallOperation{}, false, err
	}
	if operation.Request != request {
		return UninstallOperation{}, false, ErrConflict
	}
	return operation, true, state.SyncPrivate(uninstallOperationName(request.OperationID))
}

func replaceUninstallPrivate(state *runnerstate.State, name string, document []byte) error {
	if err := state.ReplacePrivate(name, document); err != nil {
		stored, readErr := state.ReadPrivate(name, MaxRecordBytes)
		if readErr != nil || !bytes.Equal(stored, document) {
			return err
		}
		return state.SyncPrivate(name)
	}
	return nil
}

func writeUninstallOperation(state *runnerstate.State, operation UninstallOperation) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	document, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	return replaceUninstallPrivate(state, uninstallOperationName(operation.Request.OperationID), document)
}

func activeUninstall(state *runnerstate.State) (*UninstallOperation, error) {
	document, err := state.ReadPrivate(activeUninstallName, MaxRequestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	request, err := DecodeUninstallRequest(bytes.NewReader(document))
	if err != nil {
		return nil, err
	}
	operation, exists, err := readUninstallOperation(state, request)
	if err != nil || !exists {
		return nil, ErrConflict
	}
	return &operation, state.SyncPrivate(activeUninstallName)
}

func checkNoUninstall(state *runnerstate.State) error {
	operation, err := activeUninstall(state)
	if err != nil || operation == nil {
		return err
	}
	switch operation.Stage {
	case "aborted":
		return nil
	case "completed":
		return ErrUninstalled
	default:
		return ErrBusy
	}
}

func claimUninstall(state *runnerstate.State, request UninstallRequest) error {
	active, err := activeUninstall(state)
	if err != nil {
		return err
	}
	if active != nil && active.Request != request {
		if err := checkNoUninstall(state); err != nil {
			return err
		}
	}
	document, _ := json.Marshal(request)
	return replaceUninstallPrivate(state, activeUninstallName, document)
}
