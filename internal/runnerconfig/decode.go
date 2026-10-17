package runnerconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"slices"
)

func (config *Config) UnmarshalJSON(document []byte) error {
	if len(document) > MaxConfigBytes {
		return ErrInvalid
	}
	fields, err := exactObject(document, []string{
		"contract", "node_id", "installation_id", "listen_port", "token_sha256", "commitment_key", "budget",
		"max_storage_bytes", "min_free_bytes", "reservation_bytes",
	})
	if err != nil {
		return err
	}
	if _, err := exactObject(fields["budget"], []string{
		"max_active_jobs", "max_queued_jobs", "max_concurrency", "queue_timeout_ns", "execution_timeout_ns",
	}); err != nil {
		return err
	}
	// JSON's []byte decoder also accepts numeric arrays. The current private
	// format defines one base64 string and must not infer an alternate shape.
	if key := bytes.TrimSpace(fields["commitment_key"]); len(key) == 0 || key[0] != '"' {
		return ErrInvalid
	}
	type configDocument Config
	var decoded configDocument
	if err := json.Unmarshal(document, &decoded); err != nil {
		clear(decoded.CommitmentKey)
		return ErrInvalid
	}
	var encodedKey string
	if err := json.Unmarshal(fields["commitment_key"], &encodedKey); err != nil {
		clear(decoded.CommitmentKey)
		return ErrInvalid
	}
	if encodedKey != base64.StdEncoding.EncodeToString(decoded.CommitmentKey) {
		clear(decoded.CommitmentKey)
		return ErrInvalid
	}
	*config = Config(decoded)
	return nil
}

func exactObject(document []byte, allowed []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, ErrInvalid
	}
	fields := map[string]json.RawMessage{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		name, ok := token.(string)
		validName := ok && slices.Contains(allowed, name) && fields[name] == nil
		if !validName {
			return nil, ErrInvalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrInvalid
		}
		fields[name] = value
	}
	if _, err := decoder.Token(); err != nil || len(fields) != len(allowed) {
		return nil, ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, ErrInvalid
	}
	return fields, nil
}
