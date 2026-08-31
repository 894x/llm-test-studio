package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type ComparisonStatus string

const (
	ComparisonRunning   ComparisonStatus = "running"
	ComparisonCompleted ComparisonStatus = "completed"
	ComparisonFailed    ComparisonStatus = "failed"
	ComparisonCancelled ComparisonStatus = "cancelled"
)

type ComparisonRunRef struct {
	Channel EntityRevisionRef `json:"channel"`
	RunID   string            `json:"run_id"`
}

type Comparison struct {
	meta   EntityMeta
	plan   EntityRevisionRef
	model  EntityRevisionRef
	runs   []ComparisonRunRef
	status ComparisonStatus
}

type serializedComparison struct {
	EntityMeta
	Plan   EntityRevisionRef  `json:"plan"`
	Model  EntityRevisionRef  `json:"model"`
	Runs   []ComparisonRunRef `json:"runs"`
	Status ComparisonStatus   `json:"status"`
}

func NewComparison(meta EntityMeta, plan, model EntityRevisionRef, runs []ComparisonRunRef) (Comparison, error) {
	candidate := Comparison{meta: meta, plan: plan, model: model, runs: append([]ComparisonRunRef(nil), runs...), status: ComparisonRunning}
	if err := candidate.Validate(); err != nil {
		return Comparison{}, err
	}
	return candidate, nil
}

func (comparison Comparison) Meta() EntityMeta         { return comparison.meta }
func (comparison Comparison) Plan() EntityRevisionRef  { return comparison.plan }
func (comparison Comparison) Model() EntityRevisionRef { return comparison.model }
func (comparison Comparison) Status() ComparisonStatus { return comparison.status }
func (comparison Comparison) Runs() []ComparisonRunRef {
	return append([]ComparisonRunRef(nil), comparison.runs...)
}

func (comparison Comparison) Validate() error {
	if err := comparison.meta.Validate(); err != nil {
		return fmt.Errorf("invalid comparison metadata: %w", err)
	}
	if err := comparison.plan.Validate("plan"); err != nil {
		return err
	}
	if err := comparison.model.Validate("model"); err != nil {
		return err
	}
	if len(comparison.runs) < 2 {
		return errors.New("comparison requires at least two channel runs")
	}
	channels := make(map[string]struct{}, len(comparison.runs))
	runIDs := make(map[string]struct{}, len(comparison.runs))
	for _, run := range comparison.runs {
		if err := run.Channel.Validate("channel"); err != nil {
			return err
		}
		if !IsUUID(run.RunID) {
			return errors.New("comparison run id must be a canonical UUID")
		}
		if _, duplicate := channels[run.Channel.ID]; duplicate {
			return errors.New("comparison channel ids must be unique")
		}
		if _, duplicate := runIDs[run.RunID]; duplicate {
			return errors.New("comparison run ids must be unique")
		}
		channels[run.Channel.ID] = struct{}{}
		runIDs[run.RunID] = struct{}{}
	}
	switch comparison.status {
	case ComparisonRunning, ComparisonCompleted, ComparisonFailed, ComparisonCancelled:
		return nil
	default:
		return fmt.Errorf("unknown comparison status %q", comparison.status)
	}
}

func (comparison Comparison) Transition(next ComparisonStatus, at time.Time) (Comparison, error) {
	if err := comparison.Validate(); err != nil {
		return Comparison{}, err
	}
	if comparison.status != ComparisonRunning || (next != ComparisonCompleted && next != ComparisonFailed && next != ComparisonCancelled) {
		return Comparison{}, fmt.Errorf("comparison cannot transition from %q to %q", comparison.status, next)
	}
	meta, err := comparison.meta.NextRevision(at)
	if err != nil {
		return Comparison{}, err
	}
	comparison.meta = meta
	comparison.status = next
	comparison.runs = append([]ComparisonRunRef(nil), comparison.runs...)
	return comparison, nil
}

func (comparison Comparison) MarshalJSON() ([]byte, error) {
	if err := comparison.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(serializedComparison{
		EntityMeta: comparison.meta, Plan: comparison.plan, Model: comparison.model,
		Runs: comparison.Runs(), Status: comparison.status,
	})
}

func (comparison *Comparison) UnmarshalJSON(data []byte) error {
	var serialized serializedComparison
	if err := json.Unmarshal(data, &serialized); err != nil {
		return err
	}
	candidate := Comparison{
		meta: serialized.EntityMeta, plan: serialized.Plan, model: serialized.Model,
		runs: append([]ComparisonRunRef(nil), serialized.Runs...), status: serialized.Status,
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*comparison = candidate
	return nil
}
