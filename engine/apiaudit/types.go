package apiaudit

import "time"

const (
	StatusPass    = "pass"
	StatusWarning = "warning"
	StatusFail    = "fail"
	StatusUnknown = "unknown"
)

type RequestDefinition struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    map[string]any    `json:"body,omitempty"`
}

type CaseDefinition struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Dimension    string            `json:"dimension"`
	Protocol     string            `json:"protocol"`
	ModelTargets []string          `json:"model_targets,omitempty"`
	Kind         string            `json:"kind"`
	Default      bool              `json:"default"`
	Disabled     bool              `json:"disabled,omitempty"`
	Severity     string            `json:"severity,omitempty"`
	Request      RequestDefinition `json:"request"`
	Options      map[string]any    `json:"options,omitempty"`
	Dir          string            `json:"-"`
}

type RunConfig struct {
	Suite            string
	BaseURL          string
	APIKey           string
	Model            string
	Models           []string
	OutputDir        string
	DryRun           bool
	NoWait           bool
	ConfirmPaidSuite bool
	PollInterval     time.Duration
	Timeout          time.Duration
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
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Dimension   string         `json:"dimension"`
	Protocol    string         `json:"protocol"`
	Model       string         `json:"model"`
	Status      string         `json:"status"`
	Severity    string         `json:"severity"`
	ElapsedMS   int64          `json:"elapsed_ms"`
	Evidence    string         `json:"evidence"`
	HTTPStatus  int            `json:"http_status,omitempty"`
	Usage       map[string]any `json:"usage,omitempty"`
	Metrics     map[string]any `json:"metrics,omitempty"`
	Exchanges   []HTTPExchange `json:"exchanges,omitempty"`
	ArtifactDir string         `json:"artifact_dir,omitempty"`
}

type SummaryCounts struct {
	Pass    int `json:"pass"`
	Warning int `json:"warning"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
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
