package updater

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func decodePreferences(data []byte, saved *preferences) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil || len(fields) != 3 {
		return errors.New("incomplete update preferences")
	}
	for _, name := range []string{"schema_version", "auto_check", "skipped_version"} {
		value, exists := fields[name]
		if !exists || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("missing update preference")
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(saved); err != nil {
		return err
	}
	if saved.SchemaVersion != 1 || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid update preferences version")
	}
	if saved.SkippedVersion != "" {
		if _, ok := versionParts(saved.SkippedVersion); !ok {
			return errors.New("invalid skipped update version")
		}
	}
	return nil
}

func writeAtomic(directory, name string, data []byte) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(directory, ".update-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(directory, name))
}

type installResult struct {
	SchemaVersion int  `json:"schema_version"`
	Failed        bool `json:"failed"`
}

func recordInstallResult(plan installPlan, failed bool) {
	directory := filepath.Dir(plan.Payload)
	root := filepath.Dir(filepath.Dir(directory))
	data, _ := json.Marshal(installResult{SchemaVersion: 1, Failed: failed})
	_ = writeAtomic(root, "last-install-result.json", data)
	_ = writeAtomic(directory, "result.json", data)
}

func failedInstall(directory string) bool {
	file, err := os.Open(filepath.Join(directory, "last-install-result.json"))
	if err != nil {
		return false
	}
	defer file.Close()
	data, err := readBounded(file, 1024)
	if err != nil {
		return true
	}
	var result installResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || result.SchemaVersion != 1 || decoder.Decode(new(any)) != io.EOF {
		return true
	}
	return result.Failed
}
