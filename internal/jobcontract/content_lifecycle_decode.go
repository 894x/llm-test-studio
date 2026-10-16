package jobcontract

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"unicode/utf8"
)

type contentLifecycleWire interface {
	FinalManifest | HoldingAttestation | HoldingAcknowledgement | ReleaseCommand | ReleaseTombstone | ReleaseStatus
	Validate() error
}

func DecodeFinalManifest(reader io.Reader) (FinalManifest, error) {
	return decodeContentLifecycle[FinalManifest](reader, MaxFinalManifestBytes, manifestFields)
}

func DecodeHoldingAttestation(reader io.Reader) (HoldingAttestation, error) {
	return decodeContentLifecycle[HoldingAttestation](reader, MaxHoldingAttestationBytes, holdingFields)
}

func DecodeHoldingAcknowledgement(reader io.Reader) (HoldingAcknowledgement, error) {
	return decodeContentLifecycle[HoldingAcknowledgement](reader, MaxHoldingAcknowledgementBytes, holdingAckFields)
}

func DecodeReleaseCommand(reader io.Reader) (ReleaseCommand, error) {
	return decodeContentLifecycle[ReleaseCommand](reader, MaxReleaseCommandBytes, releaseCommandFields)
}

func DecodeReleaseTombstone(reader io.Reader) (ReleaseTombstone, error) {
	return decodeContentLifecycle[ReleaseTombstone](reader, MaxReleaseTombstoneBytes, releaseTombstoneFields)
}

func DecodeReleaseStatus(reader io.Reader) (ReleaseStatus, error) {
	return decodeContentLifecycle[ReleaseStatus](reader, MaxReleaseStatusBytes, releaseStatusFields)
}

func decodeContentLifecycle[T contentLifecycleWire](
	reader io.Reader,
	limit int64,
	shape func([]byte) error,
) (T, error) {
	var zero T
	raw, err := stateDocument(reader, limit)
	if err != nil {
		return zero, err
	}
	if !utf8.Valid(raw) {
		return zero, invalid("content_lifecycle.utf8")
	}
	if err := shape(raw); err != nil {
		return zero, err
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return zero, invalid("content_lifecycle")
	}
	if err := value.Validate(); err != nil {
		return zero, err
	}
	return value, nil
}

func manifestFields(raw []byte) error {
	fields, err := contentLifecycleFields(raw, []string{
		"contract", "node_id", "node_installation_id", "job_id", "spec_sha256", "execution_origin",
		"inventory_revision", "terminal_revision", "sealed_sequence", "created_at", "items", "documents", "capture_failures",
	}, []string{})
	if err != nil {
		return err
	}
	if _, err := objectFields(fields["execution_origin"], []string{
		"kind", "node_id", "installation_id", "engine_build_id", "runner_version", "os", "arch",
	}, []string{}); err != nil {
		return err
	}
	if err := contentLifecycleArray(fields["items"], MaxManifestItems, manifestItemFields); err != nil {
		return err
	}
	if err := contentLifecycleArray(fields["documents"], MaxManifestDocuments, manifestDocumentFields); err != nil {
		return err
	}
	return contentLifecycleArray(fields["capture_failures"], MaxManifestArtifacts, captureFailureFields)
}

func manifestItemFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"item_id", "role", "ref", "run_id", "sha256", "size", "media_type",
	}, []string{"run_id"})
	return err
}

func manifestDocumentFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"kind", "id", "run_id", "sha256", "size",
	}, []string{"run_id"})
	return err
}

func captureFailureFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"item_id", "ref", "run_id", "error_code",
	}, []string{"run_id"})
	return err
}

func holdingFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"contract", "receipt_id", "node_id", "node_installation_id", "job_id", "spec_sha256",
		"manifest_sha256", "holder_id", "item_count", "total_bytes", "inventory_sha256",
	}, []string{})
	return err
}

func holdingAckFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"contract", "receipt_id", "job_id", "manifest_sha256", "received_at",
	}, []string{})
	return err
}

func releaseCommandFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"contract", "command_id", "expected_revision", "manifest_sha256", "hold_receipt_id",
	}, []string{})
	return err
}

func releaseTombstoneFields(raw []byte) error {
	_, err := contentLifecycleFields(raw, []string{
		"contract", "node_id", "node_installation_id", "job_id", "command_id", "expected_revision",
		"manifest_sha256", "hold_receipt_id", "released_item_count", "released_items_sha256", "released_at",
	}, []string{})
	return err
}

func releaseStatusFields(raw []byte) error {
	fields, err := contentLifecycleFields(raw, []string{
		"contract", "job_id", "state", "command_id", "tombstone", "cleanup",
	}, []string{"command_id", "tombstone"})
	if err != nil {
		return err
	}
	if !bytes.Equal(bytes.TrimSpace(fields["tombstone"]), []byte("null")) {
		if err := releaseTombstoneFields(fields["tombstone"]); err != nil {
			return err
		}
	}
	_, err = contentLifecycleFields(fields["cleanup"], []string{
		"pending", "shared_retained", "new_generation_retained", "deleting", "deleted", "absent_confirmed", "blocked",
	}, []string{})
	return err
}

// Every key is mandatory. Only the explicitly named nullable fields accept null.
func contentLifecycleFields(raw []byte, required, nullable []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid("content_lifecycle.object")
	}
	fields := make(map[string]json.RawMessage, len(required))
	for decoder.More() {
		token, err := decoder.Token()
		name, stringKey := token.(string)
		allowed := stringKey && slices.Contains(required, name)
		if err != nil || !allowed || fields[name] != nil {
			return nil, invalid("content_lifecycle.field")
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, invalid("content_lifecycle.field")
		}
		isNull := bytes.Equal(bytes.TrimSpace(value), []byte("null"))
		if isNull && !slices.Contains(nullable, name) {
			return nil, invalid("content_lifecycle.null")
		}
		fields[name] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, invalid("content_lifecycle.object")
	}
	if decoder.Decode(&json.RawMessage{}) != io.EOF {
		return nil, invalid("content_lifecycle.trailing")
	}
	if len(fields) != len(required) {
		return nil, invalid("content_lifecycle.missing")
	}
	return fields, nil
}

func contentLifecycleArray(raw []byte, limit int, shape func([]byte) error) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return invalid("content_lifecycle.array")
	}
	var count int
	for decoder.More() {
		if count >= limit {
			return ErrTooLarge
		}
		var element json.RawMessage
		if decoder.Decode(&element) != nil {
			return invalid("content_lifecycle.array")
		}
		if err := shape(element); err != nil {
			return err
		}
		count++
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return invalid("content_lifecycle.array")
	}
	if decoder.Decode(&json.RawMessage{}) != io.EOF {
		return invalid("content_lifecycle.trailing")
	}
	return nil
}
