package sqlite

import (
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/canonicaljson"
)

// Assemble fixed envelopes from canonical children. Large frozen specs are not
// decoded into generic maps again just because delivery metadata changed.
func marshalCanonicalFields(values map[string]any) ([]byte, error) {
	fields := make(map[string]json.RawMessage, len(values))
	for name, value := range values {
		if raw, ok := value.(json.RawMessage); ok && canonicaljson.Known(raw) {
			fields[name] = raw
			continue
		}
		raw, err := marshalCanonical(value)
		if err != nil {
			return nil, err
		}
		fields[name] = raw
	}
	out, err := json.Marshal(fields)
	if err == nil {
		canonicaljson.Remember(out)
	}
	return out, err
}

func marshalSubmissionCanonical(record jobs.SubmissionRecord) ([]byte, error) {
	prepared, err := marshalCanonicalFields(map[string]any{"configuration_id": record.Prepared.ConfigurationID, "runner": record.Prepared.Runner, "spec": record.Prepared.Spec})
	if err != nil {
		return nil, err
	}
	return marshalCanonicalFields(map[string]any{
		"prepared": json.RawMessage(prepared), "spec_sha256": record.SpecSHA256,
		"revision": record.Revision, "state": record.State,
		"observation": record.Observation, "created_at": record.CreatedAt, "updated_at": record.UpdatedAt, "receipt": record.Receipt,
	})
}
