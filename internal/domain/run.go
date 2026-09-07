package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const (
	legacyRunSnapshotSchemaVersion  = 1
	CurrentRunSnapshotSchemaVersion = 2
)

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

type RunFailure struct {
	Phase     ErrorCode `json:"phase"`
	ErrorCode ErrorCode `json:"error_code"`
}

func (failure RunFailure) Validate() error {
	if err := failure.Phase.Validate(); err != nil {
		return fmt.Errorf("invalid run failure phase: %w", err)
	}
	if err := failure.ErrorCode.Validate(); err != nil {
		return fmt.Errorf("invalid run failure error code: %w", err)
	}
	return nil
}

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
	SchemaVersion   int                 `json:"schema_version"`
	Plan            EntityRevisionRef   `json:"plan"`
	Model           ModelSnapshot       `json:"model"`
	Channel         ChannelSnapshot     `json:"channel"`
	Cases           []CaseRevisionRef   `json:"cases"`
	Load            LoadProfile         `json:"load"`
	SLA             SLAProfile          `json:"sla"`
	Environment     EnvironmentSnapshot `json:"environment"`
	PlanDocument    *Plan               `json:"plan_document,omitempty"`
	Mapping         *ChannelModel       `json:"mapping,omitempty"`
	CaseDefinitions []TestCase          `json:"case_definitions,omitempty"`
	QuickTask       *QuickTaskSnapshot  `json:"quick_task,omitempty"`
}

func (snapshot RunSnapshot) Validate() error {
	if snapshot.SchemaVersion != legacyRunSnapshotSchemaVersion && snapshot.SchemaVersion != CurrentRunSnapshotSchemaVersion {
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
	if err := snapshot.Environment.Validate(); err != nil {
		return err
	}
	if snapshot.SchemaVersion == legacyRunSnapshotSchemaVersion {
		if snapshot.PlanDocument != nil || snapshot.Mapping != nil || len(snapshot.CaseDefinitions) != 0 || snapshot.QuickTask != nil {
			return errors.New("legacy run snapshot must not contain v2 configuration documents")
		}
		return nil
	}
	if snapshot.PlanDocument == nil || snapshot.Mapping == nil {
		return errors.New("run snapshot requires complete plan and mapping documents")
	}
	if err := snapshot.PlanDocument.Validate(); err != nil {
		return fmt.Errorf("invalid run plan document: %w", err)
	}
	if snapshot.PlanDocument.ID != snapshot.Plan.ID || snapshot.PlanDocument.Revision != snapshot.Plan.Revision ||
		!caseRefsAreOrderedSubset(snapshot.PlanDocument.Cases, snapshot.Cases) ||
		!reflect.DeepEqual(snapshot.PlanDocument.Load, snapshot.Load) ||
		!reflect.DeepEqual(snapshot.PlanDocument.SLA, snapshot.SLA) {
		return errors.New("run plan document does not match the pinned plan snapshot")
	}
	if len(snapshot.PlanDocument.ModelIDs) > 0 &&
		(!containsValue(snapshot.PlanDocument.ModelIDs, snapshot.Model.ID) || !containsValue(snapshot.PlanDocument.ChannelIDs, snapshot.Channel.ID)) {
		return errors.New("run target is outside the pinned plan document")
	}
	if err := snapshot.Mapping.Validate(); err != nil {
		return fmt.Errorf("invalid run mapping document: %w", err)
	}
	if snapshot.Mapping.ChannelID != snapshot.Channel.ID || snapshot.Mapping.ModelID != snapshot.Model.ID ||
		snapshot.Mapping.UpstreamModelName != snapshot.Channel.UpstreamModelName {
		return errors.New("run mapping document does not match the selected target")
	}
	if len(snapshot.CaseDefinitions) != len(snapshot.Cases) {
		return errors.New("run snapshot case definitions do not match case references")
	}
	for index, testCase := range snapshot.CaseDefinitions {
		if err := testCase.Validate(); err != nil {
			return fmt.Errorf("invalid run case definition at index %d: %w", index, err)
		}
		ref := snapshot.Cases[index]
		if testCase.ID != ref.CaseID || testCase.Revision != ref.Revision || testCase.Protocol != snapshot.Model.Protocol {
			return errors.New("run case definition does not match its pinned reference")
		}
	}
	if snapshot.QuickTask != nil {
		return snapshot.QuickTask.validate(snapshot)
	}
	return nil
}

func (snapshot RunSnapshot) clone() RunSnapshot {
	snapshot.Model = snapshot.Model.clone()
	snapshot.Cases = append([]CaseRevisionRef(nil), snapshot.Cases...)
	snapshot.SLA = snapshot.SLA.clone()
	if snapshot.PlanDocument != nil {
		plan := cloneRunPlan(*snapshot.PlanDocument)
		snapshot.PlanDocument = &plan
	}
	if snapshot.Mapping != nil {
		mapping := *snapshot.Mapping
		snapshot.Mapping = &mapping
	}
	snapshot.CaseDefinitions = cloneRunCases(snapshot.CaseDefinitions)
	snapshot.QuickTask = snapshot.QuickTask.clone()
	return snapshot
}

func cloneRunPlan(plan Plan) Plan {
	if plan.ModelIDs != nil {
		plan.ModelIDs = append([]string{}, plan.ModelIDs...)
	}
	if plan.ChannelIDs != nil {
		plan.ChannelIDs = append([]string{}, plan.ChannelIDs...)
	}
	if plan.Cases != nil {
		plan.Cases = append([]CaseRevisionRef{}, plan.Cases...)
	}
	plan.SLA = plan.SLA.clone()
	return plan
}

func cloneRunCases(values []TestCase) []TestCase {
	if values == nil {
		return nil
	}
	result := make([]TestCase, len(values))
	for index, value := range values {
		value.ModelTargets = append([]string(nil), value.ModelTargets...)
		value.Definition.Spec = append(json.RawMessage(nil), value.Definition.Spec...)
		result[index] = value
	}
	return result
}

func containsValue(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func caseRefsAreOrderedSubset(all, selected []CaseRevisionRef) bool {
	next := 0
	for _, candidate := range all {
		if next < len(selected) && candidate == selected[next] {
			next++
		}
	}
	return next == len(selected)
}

type Run struct {
	meta         EntityMeta
	planID       string
	status       RunStatus
	planSnapshot RunSnapshot
	failure      *RunFailure
}

type serializedRun struct {
	EntityMeta
	PlanID       string      `json:"plan_id"`
	Status       RunStatus   `json:"status"`
	PlanSnapshot RunSnapshot `json:"plan_snapshot"`
	Failure      *RunFailure `json:"failure,omitempty"`
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
	if snapshot.QuickTask != nil && planID != meta.ID {
		return Run{}, errors.New("quick run requires a run-local execution plan identity")
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

func (run Run) Failure() *RunFailure {
	if run.failure == nil {
		return nil
	}
	failure := *run.failure
	return &failure
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
	if run.failure != nil {
		if run.status != RunFailed {
			return errors.New("run failure details require failed status")
		}
		if err := run.failure.Validate(); err != nil {
			return err
		}
	}
	if err := run.planSnapshot.Validate(); err != nil {
		return fmt.Errorf("invalid run plan snapshot: %w", err)
	}
	if run.planSnapshot.Plan.ID != run.planID {
		return errors.New("run plan id does not match its snapshot")
	}
	if run.planSnapshot.QuickTask != nil && run.planID != run.meta.ID {
		return errors.New("quick run requires a run-local execution plan identity")
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

func (run Run) Fail(failure RunFailure, at time.Time) (Run, error) {
	if err := failure.Validate(); err != nil {
		return Run{}, err
	}
	failed, err := run.Transition(RunFailed, at)
	if err != nil {
		return Run{}, err
	}
	failed.failure = &failure
	return failed, nil
}

func (run Run) MarshalJSON() ([]byte, error) {
	if err := run.Validate(); err != nil {
		return nil, fmt.Errorf("marshal run: %w", err)
	}
	return json.Marshal(serializedRun{
		EntityMeta: run.meta, PlanID: run.planID, Status: run.status, PlanSnapshot: run.Snapshot(), Failure: run.Failure(),
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
		failure:      serialized.Failure,
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*run = candidate
	return nil
}
