// Package testspec defines the current protocol-neutral authored test contract.
// It has no transport, persistence, or application dependencies.
package testspec

import "encoding/json"

const MaxDocumentBytes = 4 << 20

type Spec struct {
	Operation  string           `json:"operation,omitempty"`
	Inputs     map[string]Input `json:"inputs"`
	Request    Request          `json:"request"`
	Assertions []Assertion      `json:"assertions"`
	Workflow   *Workflow        `json:"workflow,omitempty"`
}

type Input struct {
	Type        string            `json:"type"`
	Description string            `json:"description,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	Default     json.RawMessage   `json:"default,omitempty"`
	Required    bool              `json:"required,omitempty"`
	Minimum     *float64          `json:"minimum,omitempty"`
	Maximum     *float64          `json:"maximum,omitempty"`
	Enum        []json.RawMessage `json:"enum,omitempty"`
}

type Request struct {
	Body json.RawMessage `json:"body"`
}

// Workflow specifies protocol execution behavior, never expected outcomes.
type Workflow struct {
	Mode  string `json:"mode"`
	Steps []Step `json:"steps,omitempty"`
}

// Steps contain complete request body templates. $response nodes reference an
// earlier step's response through a JSON Pointer; request order is explicit.
type Step struct {
	ID      string  `json:"id"`
	Request Request `json:"request"`
}

type Assertion struct {
	ID           string          `json:"id"`
	Source       string          `json:"source,omitempty"`
	Pointer      string          `json:"pointer,omitempty"`
	Operator     string          `json:"operator,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	All          []Assertion     `json:"all,omitempty"`
	Any          []Assertion     `json:"any,omitempty"`
	Each         []Assertion     `json:"each,omitempty"`
	EachMode string `json:"each_mode,omitempty"`
	Transform    string          `json:"transform,omitempty"`
	ExpectedFrom *Reference      `json:"expected_from,omitempty"`
}

type Reference struct {
	Source    string `json:"source"`
	Pointer   string `json:"pointer,omitempty"`
	Transform string `json:"transform,omitempty"`
}

type RandomContext struct {
	Seed            uint64 `json:"seed"`
	ExecutionItemID string `json:"execution_item_id"`
	MemberID        string `json:"member_id"`
	Iteration       uint64 `json:"iteration"`
}

type RunSettings struct {
	TimeoutMS      uint64 `json:"timeout_ms"`
	PollIntervalMS uint64 `json:"poll_interval_ms,omitempty"`
	TaskTimeoutMS  uint64 `json:"task_timeout_ms,omitempty"`
}

type VerdictStatus string

const (
	VerdictPassed        VerdictStatus = "passed"
	VerdictFailed        VerdictStatus = "failed"
	VerdictNotApplicable VerdictStatus = "not_applicable"
	VerdictIndeterminate VerdictStatus = "indeterminate"
)

type AssertionResult struct {
	ID       string            `json:"id"`
	Source   string            `json:"source,omitempty"`
	Pointer  string            `json:"pointer,omitempty"`
	Operator string            `json:"operator,omitempty"`
	Expected json.RawMessage   `json:"expected,omitempty"`
	Actual   json.RawMessage   `json:"actual,omitempty"`
	Status   VerdictStatus     `json:"status"`
	Reason   string            `json:"reason,omitempty"`
	Children []AssertionResult `json:"children,omitempty"`
}

type Verdict struct {
	Status     VerdictStatus     `json:"status"`
	Assertions []AssertionResult `json:"assertions"`
}

type Issue struct {
	Stage string `json:"stage"`
	Code  string `json:"code"`
}

type Task struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Terminal bool   `json:"terminal"`
}

type Artifact struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

type Exchange struct {
	Step        string            `json:"step"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	RequestBody json.RawMessage   `json:"request_body,omitempty"`
	HTTPStatus  *int              `json:"http_status,omitempty"`
	Response    json.RawMessage   `json:"response,omitempty"`
	Events      []json.RawMessage `json:"events,omitempty"`
	ElapsedMS   float64           `json:"elapsed_ms"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
	StreamCompleted *bool `json:"stream_completed,omitempty"`
}

type Observation struct {
	Model string `json:"model"`
	Protocol        string             `json:"protocol"`
	HTTPStatus      *int               `json:"http_status,omitempty"`
	Response        json.RawMessage    `json:"response,omitempty"`
	Text            *string            `json:"text,omitempty"`
	StreamCompleted *bool              `json:"stream_completed,omitempty"`
	Metrics         map[string]float64 `json:"metrics"`
	Usage           json.RawMessage    `json:"usage,omitempty"`
	Task            *Task              `json:"task,omitempty"`
	Artifacts       []Artifact         `json:"artifacts"`
	Exchanges       []Exchange         `json:"exchanges"`
	Issues          []Issue            `json:"issues"`
}

type Result struct {
	Observation Observation `json:"observation"`
	Verdict     Verdict     `json:"verdict"`
}

type Metric struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Unit        string `json:"unit"`
	Scope       string `json:"scope"`
	Aggregation string `json:"aggregation"`
}
