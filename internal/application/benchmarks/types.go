// Package benchmarks owns local dataset evaluations and their frozen reports.
package benchmarks

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/domain"
)

var (
	ErrInvalid     = errors.New("benchmark_invalid")
	ErrUnavailable = errors.New("benchmark_unavailable")
	ErrRuntime     = errors.New("benchmark_runtime_unavailable")
	ErrBusy        = errors.New("benchmark_busy")
	ErrNotFound    = errors.New("benchmark_not_found")
	ErrFormat      = errors.New("benchmark_format_unsupported")
)

type Dataset struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	Category         string `json:"category"`
	SourceURL        string `json:"source_url"`
	DefaultMaxTokens int    `json:"default_max_tokens"`
}

type Options struct {
	SampleLimit     int      `json:"sample_limit"`
	Epochs          int      `json:"epochs"`
	MaxTokens       int      `json:"max_tokens"`
	Concurrency     int      `json:"concurrency"`
	TimeoutSeconds  int      `json:"timeout_seconds"`
	Temperature     *float64 `json:"temperature"`
	Seed            int      `json:"seed"`
	Stream          bool     `json:"stream"`
	ReasoningEffort string   `json:"reasoning_effort"`
}

func (o Options) Validate() error {
	validSamples := o.SampleLimit >= 0 && o.SampleLimit <= 10000
	validEpochs := o.Epochs >= 1 && o.Epochs <= 32
	validConcurrency := o.Concurrency >= 1 && o.Concurrency <= 64
	validTimeout := o.TimeoutSeconds >= 1 && o.TimeoutSeconds <= 86400
	validTokens := o.MaxTokens >= 1 && o.MaxTokens <= 1048576
	validSeed := o.Seed >= 0 && o.Seed <= 2147483647
	if !validSamples || !validEpochs || !validConcurrency || !validTimeout || !validTokens || !validSeed {
		return ErrInvalid
	}
	if o.Temperature != nil && (math.IsNaN(*o.Temperature) || math.IsInf(*o.Temperature, 0) || *o.Temperature < 0 || *o.Temperature > 2) {
		return ErrInvalid
	}
	switch o.ReasoningEffort {
	case "", "minimal", "low", "medium", "high", "max":
		return nil
	default:
		return ErrInvalid
	}
}

type Connection struct {
	Protocol  domain.Protocol `json:"protocol"`
	Model     string          `json:"model"`
	ChannelID string          `json:"channel_id"`
	BaseURL   string          `json:"base_url"`
	APIKey    string          `json:"api_key"`
}

func (Connection) String() string   { return "Connection{redacted}" }
func (Connection) GoString() string { return "Connection{redacted}" }

type StartCommand struct {
	DatasetIDs []string   `json:"dataset_ids"`
	Connection Connection `json:"connection"`
	Options    Options    `json:"options"`
}

func (StartCommand) String() string   { return "StartCommand{redacted}" }
func (StartCommand) GoString() string { return "StartCommand{redacted}" }

type Target struct {
	Protocol    domain.Protocol `json:"protocol"`
	Model       string          `json:"model"`
	ChannelID   string          `json:"channel_id"`
	ChannelName string          `json:"channel_name"`
	BaseURL     string          `json:"base_url"`
}

type ResolvedConnection struct {
	Target Target
	APIKey []byte
}

type Resolver func(context.Context, Connection) (ResolvedConnection, error)

func ValidateConnection(c Connection) error {
	if !safeText(c.Model, 256, false) || !supportedProtocol(c.Protocol) {
		return ErrInvalid
	}
	if c.ChannelID != "" {
		if !domain.IsUUID(c.ChannelID) || c.APIKey != "" || c.BaseURL != "" {
			return ErrInvalid
		}
		return nil
	}
	if ValidateBaseURL(c.BaseURL) != nil || !safeText(c.APIKey, 16384, false) {
		return ErrInvalid
	}
	return nil
}

func ValidateBaseURL(value string) error {
	u, err := url.Parse(value)
	if err != nil || u == nil {
		return ErrInvalid
	}
	validScheme := u.Scheme == "http" || u.Scheme == "https"
	unsafeParts := u.User != nil || strings.ContainsAny(value, "?#\\") || u.Opaque != ""
	if !validScheme || unsafeParts || u.Hostname() == "" || !safeText(value, 2048, false) {
		return ErrInvalid
	}
	return nil
}

func supportedProtocol(p domain.Protocol) bool {
	return p == domain.ProtocolOpenAIChat || p == domain.ProtocolOpenAIResponses || p == domain.ProtocolAnthropicMessages
}

func safeText(value string, limit int, empty bool) bool {
	if len(value) > limit || (!empty && strings.TrimSpace(value) == "") {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

type Metric struct {
	Scorer string  `json:"scorer"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
}

type Score struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	Answer      string `json:"answer"`
	Explanation string `json:"explanation"`
}

type Sample struct {
	ID           string  `json:"id"`
	Epoch        int     `json:"epoch"`
	Status       string  `json:"status"`
	Scores       []Score `json:"scores"`
	Input        string  `json:"input"`
	Target       string  `json:"target"`
	Output       string  `json:"output"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	DurationMS   int64   `json:"duration_ms"`
	ErrorCode    string  `json:"error_code"`
}

type Provenance struct {
	InspectAI       string `json:"inspect_ai"`
	InspectEvals    string `json:"inspect_evals"`
	Task            string `json:"task"`
	TaskVersion     string `json:"task_version"`
	DatasetName     string `json:"dataset_name"`
	DatasetLocation string `json:"dataset_location"`
	InputSHA256     string `json:"input_sha256"`
}

type DatasetRun struct {
	ReportID   string      `json:"report_id"`
	Dataset    Dataset     `json:"dataset"`
	Status     string      `json:"status"`
	Stage      string      `json:"stage"`
	Total      int         `json:"total"`
	Completed  int         `json:"completed"`
	Failed     int         `json:"failed"`
	InFlight   int         `json:"in_flight"`
	Metrics    []Metric    `json:"metrics"`
	Provenance *Provenance `json:"provenance"`
	ErrorCode  string      `json:"error_code"`
	StartedAt  string      `json:"started_at"`
	FinishedAt string      `json:"finished_at"`
}

type Run struct {
	SchemaVersion int          `json:"schema_version"`
	ID            string       `json:"id"`
	Target        Target       `json:"target"`
	Options       Options      `json:"options"`
	Status        string       `json:"status"`
	CreatedAt     string       `json:"created_at"`
	FinishedAt    string       `json:"finished_at"`
	Datasets      []DatasetRun `json:"datasets"`
}

type Page struct {
	SchemaVersion int    `json:"schema_version"`
	Runs          []Run  `json:"runs"`
	NextCursor    string `json:"next_cursor"`
}

type Report struct {
	SchemaVersion int        `json:"schema_version"`
	RunID         string     `json:"run_id"`
	Target        Target     `json:"target"`
	Options       Options    `json:"options"`
	Result        DatasetRun `json:"result"`
	Samples       []Sample   `json:"samples"`
	NextCursor    string     `json:"next_cursor"`
}

type RuntimeStatus struct {
	State     string `json:"state"`
	ErrorCode string `json:"error_code"`
}

type Catalog struct {
	SchemaVersion int           `json:"schema_version"`
	Datasets      []Dataset     `json:"datasets"`
	Runtime       RuntimeStatus `json:"runtime"`
}

type WorkerRequest struct {
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	ReportID      string          `json:"report_id"`
	DatasetID     string          `json:"dataset_id"`
	Protocol      domain.Protocol `json:"protocol"`
	Model         string          `json:"model"`
	BaseURL       string          `json:"base_url"`
	APIKey        string          `json:"api_key"`
	Options       Options         `json:"options"`
	LogDir        string          `json:"log_dir"`
}

func (WorkerRequest) String() string   { return "WorkerRequest{redacted}" }
func (WorkerRequest) GoString() string { return "WorkerRequest{redacted}" }

type Event struct {
	SchemaVersion int         `json:"schema_version"`
	RunID         string      `json:"run_id"`
	ReportID      string      `json:"report_id"`
	Sequence      int         `json:"sequence"`
	Kind          string      `json:"kind"`
	Total         int         `json:"total"`
	Completed     int         `json:"completed"`
	Failed        int         `json:"failed"`
	InFlight      int         `json:"in_flight"`
	Sample        *Sample     `json:"sample"`
	Status        string      `json:"status"`
	Metrics       []Metric    `json:"metrics"`
	Provenance    *Provenance `json:"provenance"`
	Code          string      `json:"code"`
}

type Runtime interface {
	Ready() bool
	Prepare(context.Context) error
	Execute(context.Context, WorkerRequest, func(Event) error) error
}

func Active(status string) bool {
	return status == "queued" || status == "running" || status == "cancelling"
}
