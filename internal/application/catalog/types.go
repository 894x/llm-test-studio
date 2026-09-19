// Package catalog owns the versioned, secret-free application contract for
// catalog administration. Adapters receive summaries and explicit commands;
// domain entities and their metadata never cross the application boundary.
package catalog

import (
	"encoding/json"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

const CurrentSnapshotSchemaVersion = 1

type Snapshot struct {
	SchemaVersion int                    `json:"schema_version"`
	CaseTypes     []casetypes.Descriptor `json:"case_types"`
	Models        []ModelSummary         `json:"models"`
	Channels      []ChannelSummary       `json:"channels"`
	ChannelModels []ChannelModelSummary  `json:"channel_models"`
	TestCases     []TestCaseSummary      `json:"test_cases"`
	Suites        []SuiteSummary         `json:"suites"`
	Plans         []PlanSummary          `json:"plans"`
}

type ModelSummary struct {
	ID           string          `json:"id"`
	Revision     uint64          `json:"revision"`
	Name         string          `json:"name"`
	Protocol     domain.Protocol `json:"protocol"`
	Capabilities []string        `json:"capabilities"`
}

type ChannelSummary struct {
	ID                   string          `json:"id"`
	Revision             uint64          `json:"revision"`
	Name                 string          `json:"name"`
	BaseURL              string          `json:"base_url"`
	Protocol             domain.Protocol `json:"protocol"`
	Enabled              bool            `json:"enabled"`
	CredentialConfigured bool            `json:"credential_configured"`
	ModelCount           int             `json:"model_count"`
}

type ChannelModelSummary struct {
	ID                string `json:"id"`
	Revision          uint64 `json:"revision"`
	ChannelID         string `json:"channel_id"`
	ModelID           string `json:"model_id"`
	UpstreamModelName string `json:"upstream_model_name"`
}

type TestCaseSummary struct {
	ID                      string                   `json:"id"`
	Revision                uint64                   `json:"revision"`
	Key                     string                   `json:"key"`
	Name                    string                   `json:"name"`
	Dimension               string                   `json:"dimension"`
	Protocol                domain.Protocol          `json:"protocol"`
	Enabled                 bool                     `json:"enabled"`
	Default                 bool                     `json:"default"`
	Severity                domain.CaseSeverity      `json:"severity"`
	ExecutionMode           domain.CaseExecutionMode `json:"execution_mode"`
	DefinitionSchemaVersion int                      `json:"definition_schema_version"`
	Type                    domain.CaseType          `json:"type"`
	TypeVersion             uint32                   `json:"type_version"`
	Spec                    json.RawMessage          `json:"spec"`
}

type SuiteSummary struct {
	ID          string              `json:"id"`
	Revision    uint64              `json:"revision"`
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Protocol    domain.Protocol     `json:"protocol"`
	Description string              `json:"description"`
	CaseCount   int                 `json:"case_count"`
	Cases       []CaseInput         `json:"cases"`
	Inputs      []domain.SuiteInput `json:"inputs"`
}

type PlanSummary struct {
	ID         string             `json:"id"`
	Revision   uint64             `json:"revision"`
	Name       string             `json:"name"`
	Protocol   domain.Protocol    `json:"protocol"`
	Seed       uint64             `json:"seed"`
	EntryCount int                `json:"entry_count"`
	CaseCount  int                `json:"case_count"`
	Entries    []PlanEntrySummary `json:"entries"`
}

type PlanEntryInput struct {
	WarmupCount      uint32                     `json:"warmup_count"`
	Settings         testspec.RunSettings       `json:"settings"`
	EntryID          string                     `json:"entry_id,omitempty"`
	TargetKind       domain.PlanTargetKind      `json:"target_kind"`
	TargetID         string                     `json:"target_id"`
	LoadMode         domain.LoadMode            `json:"load_mode"`
	Concurrency      uint32                     `json:"concurrency"`
	RequestCount     uint64                     `json:"request_count"`
	RatePerSecond    float64                    `json:"rate_per_second"`
	DurationMS       uint64                     `json:"duration_ms"`
	RequestTimeoutMS uint64                     `json:"request_timeout_ms"`
	SLAThresholds    map[string]float64         `json:"sla_thresholds"`
	Parameters       map[string]json.RawMessage `json:"parameters"`
}

type PlanEntrySummary struct {
	PlanEntryInput
	TargetKey  string `json:"target_key"`
	TargetName string `json:"target_name"`
	CaseCount  int    `json:"case_count"`
}

type MutationResult struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

type DeleteCommand struct {
	ID               string `json:"id"`
	ExpectedRevision uint64 `json:"expected_revision"`
}

type CreateModelCommand struct {
	Name         string          `json:"name"`
	Protocol     domain.Protocol `json:"protocol"`
	Capabilities []string        `json:"capabilities"`
}

type UpdateModelCommand struct {
	ID               string          `json:"id"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Name             string          `json:"name"`
	Protocol         domain.Protocol `json:"protocol"`
	Capabilities     []string        `json:"capabilities"`
}

type CreateChannelCommand struct {
	Name         string          `json:"name"`
	BaseURL      string          `json:"base_url"`
	APIKey       string          `json:"api_key"`
	Protocol     domain.Protocol `json:"protocol"`
	Enabled      bool            `json:"enabled"`
	CredentialID string          `json:"credential_id,omitempty"`
}

type UpdateChannelCommand struct {
	ID               string          `json:"id"`
	ExpectedRevision uint64          `json:"expected_revision"`
	Name             string          `json:"name"`
	BaseURL          string          `json:"base_url"`
	APIKey           string          `json:"api_key"`
	Protocol         domain.Protocol `json:"protocol"`
	Enabled          bool            `json:"enabled"`
}

type CreateChannelModelCommand struct {
	ChannelID         string `json:"channel_id"`
	ModelID           string `json:"model_id"`
	UpstreamModelName string `json:"upstream_model_name"`
}

// UpdateChannelModelCommand deliberately omits ChannelID and ModelID because a
// binding's two owners are immutable after creation.
type UpdateChannelModelCommand struct {
	ID                string `json:"id"`
	ExpectedRevision  uint64 `json:"expected_revision"`
	UpstreamModelName string `json:"upstream_model_name"`
}

type CreateTestCaseCommand struct {
	Key                     string                   `json:"key"`
	Name                    string                   `json:"name"`
	Dimension               string                   `json:"dimension"`
	Protocol                domain.Protocol          `json:"protocol"`
	Enabled                 bool                     `json:"enabled"`
	Default                 bool                     `json:"default"`
	Severity                domain.CaseSeverity      `json:"severity"`
	ExecutionMode           domain.CaseExecutionMode `json:"execution_mode"`
	DefinitionSchemaVersion int                      `json:"definition_schema_version"`
	Type                    domain.CaseType          `json:"type"`
	TypeVersion             uint32                   `json:"type_version"`
	Spec                    json.RawMessage          `json:"spec"`
}

type UpdateTestCaseCommand struct {
	ID                      string                   `json:"id"`
	ExpectedRevision        uint64                   `json:"expected_revision"`
	Key                     string                   `json:"key"`
	Name                    string                   `json:"name"`
	Dimension               string                   `json:"dimension"`
	Protocol                domain.Protocol          `json:"protocol"`
	Enabled                 bool                     `json:"enabled"`
	Default                 bool                     `json:"default"`
	Severity                domain.CaseSeverity      `json:"severity"`
	ExecutionMode           domain.CaseExecutionMode `json:"execution_mode"`
	DefinitionSchemaVersion int                      `json:"definition_schema_version"`
	Type                    domain.CaseType          `json:"type"`
	TypeVersion             uint32                   `json:"type_version"`
	Spec                    json.RawMessage          `json:"spec"`
}

type CaseInput struct {
	CaseID string `json:"case_id"`
}

type CreateSuiteCommand struct {
	Key         string              `json:"key"`
	Name        string              `json:"name"`
	Protocol    domain.Protocol     `json:"protocol"`
	Description string              `json:"description"`
	Cases       []CaseInput         `json:"cases"`
	Inputs      []domain.SuiteInput `json:"inputs"`
}

type UpdateSuiteCommand struct {
	ID               string              `json:"id"`
	ExpectedRevision uint64              `json:"expected_revision"`
	Key              string              `json:"key"`
	Name             string              `json:"name"`
	Protocol         domain.Protocol     `json:"protocol"`
	Description      string              `json:"description"`
	Cases            []CaseInput         `json:"cases"`
	Inputs           []domain.SuiteInput `json:"inputs"`
}

type CreatePlanCommand struct {
	Name     string           `json:"name"`
	Protocol domain.Protocol  `json:"protocol"`
	Seed     uint64           `json:"seed"`
	Entries  []PlanEntryInput `json:"entries"`
}

type UpdatePlanCommand struct {
	ID               string           `json:"id"`
	ExpectedRevision uint64           `json:"expected_revision"`
	Name             string           `json:"name"`
	Protocol         domain.Protocol  `json:"protocol"`
	Seed             uint64           `json:"seed"`
	Entries          []PlanEntryInput `json:"entries"`
}
