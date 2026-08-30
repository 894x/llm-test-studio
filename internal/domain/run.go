package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const CurrentRunSnapshotSchemaVersion = 1

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunStarting  RunStatus = "starting"
	RunRunning   RunStatus = "running"
	RunDraining  RunStatus = "draining"
	RunCompleted RunStatus = "completed"
	RunFailed    RunStatus = "failed"
	RunCancelled RunStatus = "cancelled"
)

var runTransitions = map[RunStatus]map[RunStatus]struct{}{
	RunQueued:    {RunStarting: {}, RunFailed: {}, RunCancelled: {}},
	RunStarting:  {RunRunning: {}, RunFailed: {}, RunCancelled: {}},
	RunRunning:   {RunDraining: {}, RunCompleted: {}, RunFailed: {}, RunCancelled: {}},
	RunDraining:  {RunCompleted: {}, RunFailed: {}, RunCancelled: {}},
	RunCompleted: {},
	RunFailed:    {},
	RunCancelled: {},
}

type ModelSnapshot struct {
	EntityRevisionRef
	Name         string   `json:"name"`
	Protocol     Protocol `json:"protocol"`
	Capabilities []string `json:"capabilities"`
}

func (snapshot ModelSnapshot) Validate() error {
	if err := snapshot.EntityRevisionRef.Validate("model"); err != nil {
		return err
	}
	if strings.TrimSpace(snapshot.Name) == "" {
		return errors.New("model snapshot name must not be empty")
	}
	return snapshot.Protocol.Validate()
}

func (snapshot ModelSnapshot) clone() ModelSnapshot {
	snapshot.Capabilities = append([]string(nil), snapshot.Capabilities...)
	return snapshot
}

type ChannelSnapshot struct {
	EntityRevisionRef
	Name              string   `json:"name"`
	BaseURL           string   `json:"base_url"`
	Protocol          Protocol `json:"protocol"`
	UpstreamModelName string   `json:"upstream_model_name"`
}

func (snapshot ChannelSnapshot) Validate() error {
	if err := snapshot.EntityRevisionRef.Validate("channel"); err != nil {
		return err
	}
	if strings.TrimSpace(snapshot.Name) == "" || strings.TrimSpace(snapshot.UpstreamModelName) == "" {
		return errors.New("channel snapshot requires a name and upstream model name")
	}
	if err := snapshot.Protocol.Validate(); err != nil {
		return err
	}
	return validateServiceBaseURL(snapshot.BaseURL)
}

type RunSnapshot struct {
	SchemaVersion int                 `json:"schema_version"`
	Plan          EntityRevisionRef   `json:"plan"`
	Model         ModelSnapshot       `json:"model"`
	Channel       ChannelSnapshot     `json:"channel"`
	Cases         []CaseRevisionRef   `json:"cases"`
	Load          LoadProfile         `json:"load"`
	SLA           SLAProfile          `json:"sla"`
	Environment   EnvironmentSnapshot `json:"environment"`
}

func (snapshot RunSnapshot) Validate() error {
	if snapshot.SchemaVersion != CurrentRunSnapshotSchemaVersion {
		return fmt.Errorf("unsupported run snapshot schema version %d", snapshot.SchemaVersion)
	}
	if err := snapshot.Plan.Validate("plan"); err != nil {
		return err
	}
	if err := snapshot.Model.Validate(); err != nil {
		return err
	}
	if err := snapshot.Channel.Validate(); err != nil {
		return err
	}
	if snapshot.Model.Protocol != snapshot.Channel.Protocol {
		return errors.New("run snapshot model and channel protocols must match")
	}
	if err := validateCaseRevisionRefs(snapshot.Cases); err != nil {
		return err
	}
	if err := snapshot.Load.Validate(); err != nil {
		return err
	}
	if err := snapshot.SLA.Validate(); err != nil {
		return err
	}
	return snapshot.Environment.Validate()
}

func (snapshot RunSnapshot) clone() RunSnapshot {
	snapshot.Model = snapshot.Model.clone()
	snapshot.Cases = append([]CaseRevisionRef(nil), snapshot.Cases...)
	snapshot.SLA = snapshot.SLA.clone()
	return snapshot
}

type Run struct {
	meta         EntityMeta
	planID       string
	status       RunStatus
	planSnapshot RunSnapshot
}

type serializedRun struct {
	EntityMeta
	PlanID       string      `json:"plan_id"`
	Status       RunStatus   `json:"status"`
	PlanSnapshot RunSnapshot `json:"plan_snapshot"`
}

func NewRun(meta EntityMeta, planID string, snapshot RunSnapshot) (Run, error) {
	if err := meta.Validate(); err != nil {
		return Run{}, fmt.Errorf("invalid run metadata: %w", err)
	}
	if !IsUUID(planID) {
		return Run{}, errors.New("run plan id must be a canonical UUID")
	}
	if err := snapshot.Validate(); err != nil {
		return Run{}, fmt.Errorf("invalid run plan snapshot: %w", err)
	}
	if snapshot.Plan.ID != planID {
		return Run{}, errors.New("run plan id does not match its snapshot")
	}
	return Run{
		meta: meta, planID: planID, status: RunQueued, planSnapshot: snapshot.clone(),
	}, nil
}

func (run Run) Meta() EntityMeta {
	return run.meta
}

func (run Run) PlanID() string {
	return run.planID
}

func (run Run) Status() RunStatus {
	return run.status
}

func (run Run) Snapshot() RunSnapshot {
	return run.planSnapshot.clone()
}

func (run Run) Validate() error {
	if err := run.meta.Validate(); err != nil {
		return fmt.Errorf("invalid run metadata: %w", err)
	}
	if !IsUUID(run.planID) {
		return errors.New("run plan id must be a canonical UUID")
	}
	if _, known := runTransitions[run.status]; !known {
		return fmt.Errorf("unknown run status %q", run.status)
	}
	if err := run.planSnapshot.Validate(); err != nil {
		return fmt.Errorf("invalid run plan snapshot: %w", err)
	}
	if run.planSnapshot.Plan.ID != run.planID {
		return errors.New("run plan id does not match its snapshot")
	}
	return nil
}

func (run Run) Transition(next RunStatus, at time.Time) (Run, error) {
	if err := run.Validate(); err != nil {
		return Run{}, err
	}
	allowed := runTransitions[run.status]
	if _, known := runTransitions[next]; !known {
		return Run{}, fmt.Errorf("unknown target run status %q", next)
	}
	if _, ok := allowed[next]; !ok {
		return Run{}, fmt.Errorf("run cannot transition from %q to %q", run.status, next)
	}
	meta, err := run.meta.NextRevision(at)
	if err != nil {
		return Run{}, err
	}
	run.meta = meta
	run.status = next
	run.planSnapshot = run.planSnapshot.clone()
	return run, nil
}

func (run Run) MarshalJSON() ([]byte, error) {
	if err := run.Validate(); err != nil {
		return nil, fmt.Errorf("marshal run: %w", err)
	}
	return json.Marshal(serializedRun{
		EntityMeta: run.meta, PlanID: run.planID, Status: run.status, PlanSnapshot: run.Snapshot(),
	})
}

func (run *Run) UnmarshalJSON(data []byte) error {
	var serialized serializedRun
	if err := json.Unmarshal(data, &serialized); err != nil {
		return err
	}
	candidate := Run{
		meta:         serialized.EntityMeta,
		planID:       serialized.PlanID,
		status:       serialized.Status,
		planSnapshot: serialized.PlanSnapshot.clone(),
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*run = candidate
	return nil
}
