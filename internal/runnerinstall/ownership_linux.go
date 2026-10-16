package runnerinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type programOwner struct {
	Contract    int    `json:"contract"`
	OperationID string `json:"operation_id"`
}

func (paths layout) checkInstallationRoot() error {
	if _, err := os.Lstat(paths.install); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := trustedDirectory(paths.install, os.Geteuid(), true); err != nil {
		return err
	}
	entries, err := boundedEntries(paths.install)
	if err != nil {
		return err
	}
	paired, committedOperation := false, false
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() && strings.HasPrefix(name, "upgrade-data-") {
			if err := paths.checkUpgradeDataDirectory(name); err != nil {
				return err
			}
			continue
		}
		file, err := openOwned(filepath.Join(paths.install, name), os.Geteuid(), true)
		if errors.Is(err, os.ErrNotExist) && validTemporaryName(name) {
			continue
		}
		if err != nil {
			return ErrUnownedPath
		}
		info, err := file.Stat()
		if err != nil || info.Size() > MaxRecordBytes {
			_ = file.Close()
			return ErrUnownedPath
		}
		document, err := io.ReadAll(io.LimitReader(file, MaxRecordBytes+1))
		_ = file.Close()
		if err != nil {
			clear(document)
			return err
		}
		err = func() error {
			defer clear(document)
			switch {
			case name == "runner.lock":
				if len(document) != 0 {
					return ErrUnownedPath
				}
			case name == PairingName, name == "identity.json":
				paired = true
			case strings.HasPrefix(name, "operation-") && strings.HasSuffix(name, ".json"):
				operation, err := DecodeOperation(document)
				if err != nil {
					return err
				}
				if operationName(operation.Request.OperationID) != name {
					return ErrUnownedPath
				}
				committedOperation = true
			case strings.HasPrefix(name, "upgrade-") && strings.HasSuffix(name, ".json"):
				operation, err := DecodeUpgradeOperation(document)
				if err != nil || upgradeOperationName(operation.Request.OperationID) != name {
					return ErrUnownedPath
				}
			case name == "active-upgrade.json":
				if _, err := DecodeUpgradeRequest(bytes.NewReader(document)); err != nil {
					return ErrUnownedPath
				}
			case strings.HasPrefix(name, "uninstall-") && strings.HasSuffix(name, ".json"):
				operation, err := DecodeUninstallOperation(document)
				if err != nil || uninstallOperationName(operation.Request.OperationID) != name {
					return ErrUnownedPath
				}
			case name == activeUninstallName:
				if _, err := DecodeUninstallRequest(bytes.NewReader(document)); err != nil {
					return ErrUnownedPath
				}
			case validTemporaryName(name):
				// An interrupted atomic write is never decoded as a committed record.
			default:
				return ErrUnownedPath
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	if paired && !committedOperation {
		return ErrUnownedPath
	}
	return nil
}

func validTemporaryName(name string) bool {
	if !strings.HasPrefix(name, ".new-") || len(name) != len(".new-")+26 {
		return false
	}
	for _, character := range strings.TrimPrefix(name, ".new-") {
		letter := character >= 'A' && character <= 'Z'
		digit := character >= '2' && character <= '7'
		if !letter && !digit {
			return false
		}
	}
	return true
}

func boundedEntries(name string) ([]os.DirEntry, error) {
	directory, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(10002)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) >= 10002 {
		return nil, ErrBusy
	}
	return entries, nil
}

func (paths layout) checkProgramRoot(state *runnerstate.State) error {
	if _, err := os.Lstat(paths.program); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := trustedDirectory(paths.program, os.Geteuid(), false); err != nil {
		return err
	}
	entries, err := boundedEntries(paths.program)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	file, err := openOwned(filepath.Join(paths.program, ".owner.json"), os.Geteuid(), false)
	if err != nil {
		return ErrUnownedPath
	}
	defer file.Close()
	document, err := io.ReadAll(io.LimitReader(file, MaxRequestBytes+1))
	if err != nil || len(document) > MaxRequestBytes {
		return ErrUnownedPath
	}
	var owner programOwner
	if json.Unmarshal(document, &owner) != nil || !domain.IsUUID(owner.OperationID) {
		return ErrUnownedPath
	}
	if owner.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	canonical, _ := json.Marshal(owner)
	if !bytes.Equal(document, canonical) {
		return ErrUnownedPath
	}
	proof, err := state.ReadPrivate(operationName(owner.OperationID), MaxRecordBytes)
	if err != nil {
		return ErrUnownedPath
	}
	operation, err := DecodeOperation(proof)
	if err != nil {
		return err
	}
	if operation.Request.OperationID != owner.OperationID {
		return ErrUnownedPath
	}
	if err := syncDirectory(paths.program); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(paths.program))
}
