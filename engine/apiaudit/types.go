package apiaudit

import (
	"encoding/json"
	"github.com/894x/llm-test-studio/internal/testspec"
	"time"
)

const (
	StatusPass     = "pass"
	StatusWarning  = "warning"
	StatusFail     = "fail"
	StatusUnknown  = "unknown"
	StatusObserved = "observed"
)

type CaseDefinition struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Dimension     string        `json:"dimension"`
	Protocol      string        `json:"protocol"`
	Type          string        `json:"type"`
	Default       bool          `json:"default"`
	Disabled      bool          `json:"disabled"`
	ExecutionMode string        `json:"execution_mode"`
	Severity      string        `json:"severity"`
	Spec          testspec.Spec `json:"spec"`
}

type RunConfig struct {
	Seed         uint64
	Inputs       map[string]json.RawMessage
	Suite        string
	BaseURL      string
	APIKey       string
	Model        string
	Models       []string
	OutputDir    string
	DryRun       bool
	PollInterval time.Duration
	Timeout      time.Duration
}

type PlannedRun struct {
	Case     CaseDefinition
	Model    string
	ResultID string
}

type HTTPExchange struct {
	Method       string         `json:"method"`
	URL          string         `json:"url"`
	RequestBody  map[string]any `json:"request_body,omitempty"`
	StatusCode   int            `json:"status_code,omitempty"`
	ResponseBody string         `json:"response_body,omitempty"`
}

type CaseResult struct {
	Verification testspec.Verdict      `json:"verification"`
	Observation  *testspec.Observation `json:"observation,omitempty"`
	ID           string                `json:"id"`
	Name         string                `json:"name"`
	Dimension    string                `json:"dimension"`
	Protocol     string                `json:"protocol"`
	Model        string                `json:"model"`
	Status       string                `json:"status"`
	Severity     string                `json:"severity"`
	ElapsedMS    int64                 `json:"elapsed_ms"`
	Evidence     string                `json:"evidence"`
	HTTPStatus   int                   `json:"http_status,omitempty"`
	Usage        map[string]any        `json:"usage,omitempty"`
	Metrics      map[string]any        `json:"metrics,omitempty"`
	Exchanges    []HTTPExchange        `json:"exchanges,omitempty"`
	ArtifactDir  string                `json:"artifact_dir,omitempty"`
}

type SummaryCounts struct {
	Observed int `json:"observed"`
	Pass     int `json:"pass"`
	Warning  int `json:"warning"`
	Fail     int `json:"fail"`
	Unknown  int `json:"unknown"`
}

type DimensionSummary struct {
	Dimension string `json:"dimension"`
	SummaryCounts
}

type ReportNotice struct {
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Items []string `json:"items,omitempty"`
}

type Report struct {
	Title       string             `json:"title"`
	GeneratedAt time.Time          `json:"generated_at"`
	Suite       string             `json:"suite"`
	BaseURL     string             `json:"base_url"`
	Model       string             `json:"model"`
	Overall     string             `json:"overall"`
	Verdict     string             `json:"verdict"`
	Summary     SummaryCounts      `json:"summary"`
	Notices     []ReportNotice     `json:"notices,omitempty"`
	Dimensions  []DimensionSummary `json:"dimensions"`
	Failures    []CaseResult       `json:"failures,omitempty"`
	Warnings    []CaseResult       `json:"warnings,omitempty"`
	Results     []CaseResult       `json:"results"`
	APIKey      string             `json:"-"`
}
