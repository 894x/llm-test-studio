package jobcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
)

const MaxRunnerInfoBytes = 64 << 10

// DecodeRunnerInfo requires every field of the single current management
// contract. Zero-valued booleans and counts must not hide missing or null fields.
func DecodeRunnerInfo(reader io.Reader) (RunnerInfo, error) {
	if reader == nil {
		return RunnerInfo{}, invalid("runner")
	}
	document, err := io.ReadAll(io.LimitReader(reader, MaxRunnerInfoBytes+1))
	if err != nil {
		return RunnerInfo{}, err
	}
	if len(document) > MaxRunnerInfoBytes {
		return RunnerInfo{}, invalid("runner")
	}
	fields, err := objectFields(document, []string{
		"contract", "node_id", "node_installation_id", "engine_build_id", "runner_version", "os", "arch",
		"capabilities", "budget", "ready", "healthy", "active", "queued", "pending", "maintenance_operation_id",
	}, []string{})
	if err != nil {
		return RunnerInfo{}, err
	}
	if _, err := objectFields(fields["capabilities"], []string{
		"job_kinds", "protocols", "features",
	}, []string{}); err != nil {
		return RunnerInfo{}, err
	}
	if _, err := objectFields(fields["budget"], []string{
		"max_active_jobs", "max_queued_jobs", "max_concurrency", "queue_timeout_ns", "execution_timeout_ns",
	}, []string{}); err != nil {
		return RunnerInfo{}, err
	}
	var info RunnerInfo
	if err := json.Unmarshal(document, &info); err != nil {
		return RunnerInfo{}, invalid("runner")
	}
	return info, info.Validate()
}

func objectFields(document []byte, required, optional []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid("runner")
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		allowed := slices.Contains(required, name) || slices.Contains(optional, name)
		valid := err == nil && ok && allowed && fields[name] == nil
		if !valid {
			return nil, invalid("runner")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, invalid("runner")
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid("runner")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, invalid("runner")
	}
	for _, name := range required {
		if fields[name] == nil {
			return nil, invalid("body")
		}
	}
	return fields, nil
}
