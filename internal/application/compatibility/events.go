package compatibility

import (
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
)

const EventSchemaVersion = 1

type EventType string

const (
	EventTypePlan     EventType = "plan"
	EventTypeProgress EventType = "progress"
	EventTypeFinal    EventType = "final"
)

type Event interface {
	isCompatibilityEvent()
	Metadata() EventMetadata
}

type EventMetadata struct {
	SchemaVersion int       `json:"schema_version"`
	EventID       string    `json:"event_id"`
	RunID         string    `json:"run_id"`
	Sequence      uint64    `json:"sequence"`
	OccurredAt    time.Time `json:"occurred_at"`
	Type          EventType `json:"type"`
}

type PlannedRun struct {
	ID        string `json:"id"`
	CaseID    string `json:"case_id"`
	Name      string `json:"name"`
	Dimension string `json:"dimension"`
	Protocol  string `json:"protocol"`
	Model     string `json:"model"`
}

type PlanPayload struct {
	Total int          `json:"total"`
	Runs  []PlannedRun `json:"runs"`
}

type PlanEvent struct {
	EventMetadata
	Payload PlanPayload `json:"payload"`
}

func (PlanEvent) isCompatibilityEvent()         {}
func (event PlanEvent) Metadata() EventMetadata { return event.EventMetadata }

type ProgressPayload struct {
	Completed int                 `json:"completed"`
	Total     int                 `json:"total"`
	Result    apiaudit.CaseResult `json:"result"`
}

type ProgressEvent struct {
	EventMetadata
	Payload ProgressPayload `json:"payload"`
}

func (ProgressEvent) isCompatibilityEvent()         {}
func (event ProgressEvent) Metadata() EventMetadata { return event.EventMetadata }

type FinalPayload struct {
	Command    string                 `json:"command"`
	ReportDir  string                 `json:"report_dir"`
	ReportJSON string                 `json:"report_json"`
	ReportHTML string                 `json:"report_html"`
	Overall    string                 `json:"overall"`
	Verdict    string                 `json:"verdict"`
	Summary    apiaudit.SummaryCounts `json:"summary"`
}

type FinalEvent struct {
	EventMetadata
	Payload FinalPayload `json:"payload"`
}

func (FinalEvent) isCompatibilityEvent()         {}
func (event FinalEvent) Metadata() EventMetadata { return event.EventMetadata }
