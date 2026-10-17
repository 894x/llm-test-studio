package client

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func object(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, ErrIntegrity
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		name, ok := token.(string)
		allowed := slices.Contains(required, name) || slices.Contains(optional, name)
		valid := err == nil && ok && allowed && fields[name] == nil
		if !valid {
			return nil, ErrIntegrity
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrIntegrity
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, ErrIntegrity
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrIntegrity
	}
	for _, name := range required {
		if fields[name] == nil {
			return nil, ErrIntegrity
		}
	}
	return fields, nil
}

func decodeError(response *http.Response) error {
	raw, err := readJSON(response, 4<<10)
	if err != nil {
		return err
	}
	if _, err := object(raw, []string{"contract", "code", "message", "retryable"}, []string{"field"}); err != nil {
		return err
	}
	var wire struct {
		Contract  int    `json:"contract"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		Field     string `json:"field"`
		Retryable bool   `json:"retryable"`
	}
	if json.Unmarshal(raw, &wire) != nil {
		return ErrIntegrity
	}
	if wire.Contract != jobcontract.CurrentContract {
		return jobcontract.ErrUnsupported
	}
	codes := map[string]int{
		"unauthorized": 401, "unsupported_contract": 426, "invalid_query": 400,
		"invalid_request": 400, "invalid_command": 400, "invalid_content_type": 415,
		"body_too_large": 413, "capability_missing": 422, "job_conflict": 409,
		"not_found": 404, "capacity_exceeded": 429, "runner_maintenance": 423,
		"storage_unavailable": 503, "invalid_range": 416,
		"asset_integrity_mismatch": 422, "asset_read_failed": 408, "invalid_content_encoding": 415,
		"upload_in_progress": 409,
	}
	uploadBusy := response.StatusCode == http.StatusConflict && wire.Code == "upload_in_progress"
	retryable := response.StatusCode == 503 || response.StatusCode == 429 || uploadBusy
	if codes[wire.Code] != response.StatusCode || wire.Retryable != retryable {
		return ErrIntegrity
	}
	return &RemoteError{Code: wire.Code, Retryable: wire.Retryable}
}

func array(raw []byte, limit int) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, ErrIntegrity
	}
	items := []json.RawMessage{}
	for decoder.More() {
		if len(items) >= limit {
			return nil, ErrIntegrity
		}
		var item json.RawMessage
		if decoder.Decode(&item) != nil {
			return nil, ErrIntegrity
		}
		items = append(items, item)
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, ErrIntegrity
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, ErrIntegrity
	}
	return items, nil
}
