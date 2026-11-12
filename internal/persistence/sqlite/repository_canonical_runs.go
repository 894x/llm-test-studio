package sqlite

import (
	"encoding/json"
	"errors"

	"github.com/894x/llm-test-studio/internal/canonicaljson"
	"github.com/894x/llm-test-studio/internal/domain"
)

// This envelope is read only from our validated canonical job encoding. Keep
// snapshots as owned raw documents; do not decode case bodies to generic maps.
func canonicalRunSnapshots(document []byte) (map[string]json.RawMessage, error) {
	type frozenRun struct {
		Meta     domain.EntityMeta `json:"meta"`
		Snapshot json.RawMessage   `json:"snapshot"`
	}
	var envelope struct {
		Spec struct {
			Run        *frozenRun `json:"run"`
			Comparison *struct {
				Runs []frozenRun `json:"runs"`
			} `json:"comparison"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(document, &envelope); err != nil {
		return nil, err
	}
	result := map[string]json.RawMessage{}
	add := func(run frozenRun) { result[run.Meta.ID] = run.Snapshot; canonicaljson.Remember(run.Snapshot) }
	if envelope.Spec.Run != nil {
		add(*envelope.Spec.Run)
	}
	if envelope.Spec.Comparison != nil {
		for _, run := range envelope.Spec.Comparison.Runs {
			add(run)
		}
	}
	return result, nil
}

func prepareCanonicalJobRun(run domain.Run, snapshot json.RawMessage) (preparedRunCreate, error) {
	if err := run.Validate(); err != nil {
		return preparedRunCreate{}, err
	}
	meta := run.Meta()
	if meta.Revision != 1 || run.Status() != domain.RunQueued || len(snapshot) == 0 {
		return preparedRunCreate{}, errors.New("invalid frozen initial run")
	}
	document, err := marshalCanonicalFields(map[string]any{
		"schema_version": meta.SchemaVersion, "id": meta.ID, "revision": meta.Revision,
		"created_at": meta.CreatedAt, "updated_at": meta.UpdatedAt,
		"plan_id": run.PlanID(), "status": run.Status(), "plan_snapshot": snapshot,
	})
	if err != nil {
		return preparedRunCreate{}, err
	}
	return preparedRunCreate{run: run, document: document, snapshot: snapshot}, nil
}
