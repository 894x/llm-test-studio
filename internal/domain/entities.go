package domain

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/url"
	"path"
	"strconv"
	"strings"
)

type Protocol string

const (
	ProtocolOpenAIChat Protocol = "openai-chat"
	ProtocolKimiK3     Protocol = "kimi-k3"
	ProtocolSeedance   Protocol = "seedance"
	ProtocolWanVideo   Protocol = "wan-video"
)

func (protocol Protocol) Validate() error {
	switch protocol {
	case ProtocolOpenAIChat, ProtocolKimiK3, ProtocolSeedance, ProtocolWanVideo:
		return nil
	default:
		return fmt.Errorf("unsupported protocol %q", protocol)
	}
}

type EntityRevisionRef struct {
	ID       string `json:"id"`
	Revision uint64 `json:"revision"`
}

func (ref EntityRevisionRef) Validate(kind string) error {
	if !IsUUID(ref.ID) {
		return fmt.Errorf("%s id must be a canonical UUID", kind)
	}
	if ref.Revision < 1 {
		return fmt.Errorf("%s revision must be positive", kind)
	}
	return nil
}

type CaseRevisionRef struct {
	CaseID   string `json:"case_id"`
	Revision uint64 `json:"revision"`
}

func (ref CaseRevisionRef) Validate() error {
	return EntityRevisionRef{ID: ref.CaseID, Revision: ref.Revision}.Validate("case")
}

type Model struct {
	EntityMeta
	Name         string   `json:"name"`
	Protocol     Protocol `json:"protocol"`
	Capabilities []string `json:"capabilities,omitempty"`
}

func (model Model) Validate() error {
	if err := model.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid model metadata: %w", err)
	}
	if strings.TrimSpace(model.Name) == "" {
		return errors.New("model name must not be empty")
	}
	return model.Protocol.Validate()
}

type Channel struct {
	EntityMeta
	Name         string   `json:"name"`
	BaseURL      string   `json:"base_url"`
	Protocol     Protocol `json:"protocol"`
	Enabled      bool     `json:"enabled"`
	CredentialID string   `json:"credential_id,omitempty"`
}

func (channel Channel) Validate() error {
	if err := channel.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid channel metadata: %w", err)
	}
	if strings.TrimSpace(channel.Name) == "" {
		return errors.New("channel name must not be empty")
	}
	if err := channel.Protocol.Validate(); err != nil {
		return err
	}
	if err := validateServiceBaseURL(channel.BaseURL); err != nil {
		return fmt.Errorf("invalid channel base URL: %w", err)
	}
	if channel.CredentialID != "" && !IsUUID(channel.CredentialID) {
		return errors.New("channel credential id must be a canonical UUID")
	}
	return nil
}

type ChannelModel struct {
	EntityMeta
	ChannelID         string `json:"channel_id"`
	ModelID           string `json:"model_id"`
	UpstreamModelName string `json:"upstream_model_name"`
}

func (mapping ChannelModel) Validate() error {
	if err := mapping.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid channel model metadata: %w", err)
	}
	if !IsUUID(mapping.ChannelID) || !IsUUID(mapping.ModelID) {
		return errors.New("channel model requires canonical channel and model UUIDs")
	}
	if strings.TrimSpace(mapping.UpstreamModelName) == "" {
		return errors.New("channel model upstream name must not be empty")
	}
	return nil
}

type CredentialPurpose string

const (
	CredentialChannelAPIKey    CredentialPurpose = "channel_api_key"
	CredentialIntegrationAdmin CredentialPurpose = "integration_admin"
)

func (purpose CredentialPurpose) Validate() error {
	switch purpose {
	case CredentialChannelAPIKey, CredentialIntegrationAdmin:
		return nil
	default:
		return fmt.Errorf("unsupported credential purpose %q", purpose)
	}
}

type CredentialRef struct {
	EntityMeta
	StoreRef     string            `json:"store_ref"`
	Purpose      CredentialPurpose `json:"purpose"`
	MaskedSuffix string            `json:"masked_suffix"`
	Fingerprint  string            `json:"fingerprint"`
}

func (ref CredentialRef) Validate() error {
	if err := ref.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid credential metadata: %w", err)
	}
	if strings.TrimSpace(ref.StoreRef) == "" {
		return errors.New("credential store ref must not be empty")
	}
	if err := ref.Purpose.Validate(); err != nil {
		return err
	}
	if !isSafeCredentialSuffix(ref.MaskedSuffix) {
		return errors.New("credential masked suffix must contain exactly four ASCII letters or digits")
	}
	if !strings.HasPrefix(ref.Fingerprint, "sha256:") || !isLowerHexDigest(strings.TrimPrefix(ref.Fingerprint, "sha256:")) {
		return errors.New("credential fingerprint must be sha256 followed by 64 lowercase hex characters")
	}
	return nil
}

type Suite struct {
	EntityMeta
	Key         string            `json:"key"`
	Name        string            `json:"name"`
	Protocol    Protocol          `json:"protocol"`
	ModelTarget string            `json:"model_target"`
	Cases       []CaseRevisionRef `json:"cases"`
}

func (suite Suite) Validate() error {
	if err := suite.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid suite metadata: %w", err)
	}
	if !isSafeCaseKey(suite.Key) {
		return errors.New("suite key must contain only letters, digits, dot, underscore, or hyphen")
	}
	if strings.TrimSpace(suite.Name) == "" {
		return errors.New("suite name must not be empty")
	}
	if err := suite.Protocol.Validate(); err != nil {
		return err
	}
	if !isSafeModelTarget(suite.ModelTarget) {
		return errors.New("suite model target must be a trimmed, non-empty identifier without control characters")
	}
	return validateCaseRevisionRefs(suite.Cases)
}

type LoadMode string

const (
	LoadSingle           LoadMode = "single"
	LoadFixedConcurrency LoadMode = "fixed_concurrency"
	LoadOpenLoop         LoadMode = "open_loop"
)

type LoadProfile struct {
	Mode             LoadMode `json:"mode"`
	Concurrency      uint32   `json:"concurrency"`
	RequestCount     uint64   `json:"request_count"`
	RatePerSecond    float64  `json:"rate_per_second"`
	DurationMS       uint64   `json:"duration_ms"`
	RequestTimeoutMS uint64   `json:"request_timeout_ms"`
}

func (profile LoadProfile) Validate() error {
	switch profile.Mode {
	case LoadSingle, LoadFixedConcurrency, LoadOpenLoop:
	default:
		return fmt.Errorf("unsupported load mode %q", profile.Mode)
	}
	if profile.Concurrency < 1 {
		return errors.New("load concurrency must be positive")
	}
	if profile.RequestCount == 0 && profile.DurationMS == 0 {
		return errors.New("load profile requires a request count or duration")
	}
	if math.IsNaN(profile.RatePerSecond) || math.IsInf(profile.RatePerSecond, 0) || profile.RatePerSecond < 0 {
		return errors.New("load rate must be finite and non-negative")
	}
	if profile.Mode == LoadOpenLoop && profile.RatePerSecond <= 0 {
		return errors.New("open-loop load requires a positive rate")
	}
	if profile.RequestTimeoutMS == 0 {
		return errors.New("load request timeout must be positive")
	}
	return nil
}

type SLAProfile struct {
	Thresholds map[string]float64 `json:"thresholds"`
}

func (profile SLAProfile) Validate() error {
	if len(profile.Thresholds) == 0 {
		return errors.New("SLA profile requires at least one threshold")
	}
	for name, threshold := range profile.Thresholds {
		if strings.TrimSpace(name) == "" || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold < 0 {
			return fmt.Errorf("invalid SLA threshold %q", name)
		}
	}
	return nil
}

func (profile SLAProfile) clone() SLAProfile {
	cloned := SLAProfile{Thresholds: make(map[string]float64, len(profile.Thresholds))}
	for name, value := range profile.Thresholds {
		cloned.Thresholds[name] = value
	}
	return cloned
}

type Plan struct {
	EntityMeta
	Name          string            `json:"name"`
	ModelIDs      []string          `json:"model_ids"`
	ChannelIDs    []string          `json:"channel_ids"`
	SuiteID       string            `json:"suite_id,omitempty"`
	SuiteRevision uint64            `json:"suite_revision,omitempty"`
	Cases         []CaseRevisionRef `json:"cases"`
	Load          LoadProfile       `json:"load"`
	SLA           SLAProfile        `json:"sla"`
}

func (plan Plan) Validate() error {
	if err := plan.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid plan metadata: %w", err)
	}
	if strings.TrimSpace(plan.Name) == "" {
		return errors.New("plan name must not be empty")
	}
	if (len(plan.ModelIDs) == 0) != (len(plan.ChannelIDs) == 0) {
		return errors.New("plan model and channel allowlists must both be empty or both be configured")
	}
	if len(plan.ModelIDs) > 0 {
		if err := validateUUIDList("model", plan.ModelIDs); err != nil {
			return err
		}
		if err := validateUUIDList("channel", plan.ChannelIDs); err != nil {
			return err
		}
	}
	if plan.SuiteID == "" && plan.SuiteRevision != 0 {
		return errors.New("plan suite revision requires a suite id")
	}
	if plan.SuiteID != "" {
		if err := (EntityRevisionRef{ID: plan.SuiteID, Revision: plan.SuiteRevision}).Validate("suite"); err != nil {
			return err
		}
	}
	if err := validateCaseRevisionRefs(plan.Cases); err != nil {
		return err
	}
	if err := plan.Load.Validate(); err != nil {
		return err
	}
	return plan.SLA.Validate()
}

type Evidence struct {
	EntityMeta
	RunID        string `json:"run_id"`
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	MediaType    string `json:"media_type"`
	Redacted     bool   `json:"redacted"`
}

func (evidence Evidence) Validate(expectedRunID ...string) error {
	if err := evidence.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid evidence metadata: %w", err)
	}
	if err := validateArtifactMetadata(evidence.RunID, evidence.RelativePath, evidence.SHA256, evidence.MediaType, evidence.Redacted, expectedRunID...); err != nil {
		return fmt.Errorf("invalid evidence: %w", err)
	}
	return nil
}

type IntegrationKind string

const IntegrationNewAPI IntegrationKind = "new-api"

func (kind IntegrationKind) Validate() error {
	if kind != IntegrationNewAPI {
		return fmt.Errorf("unsupported integration kind %q", kind)
	}
	return nil
}

type IntegrationConfig struct {
	BaseURL    string `json:"base_url"`
	Region     string `json:"region,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
}

func (config IntegrationConfig) Validate() error {
	if err := validateServiceBaseURL(config.BaseURL); err != nil {
		return fmt.Errorf("invalid integration base URL: %w", err)
	}
	if strings.TrimSpace(config.Region) != config.Region || strings.TrimSpace(config.ExternalID) != config.ExternalID {
		return errors.New("integration region and external id must not contain surrounding whitespace")
	}
	return nil
}

type Integration struct {
	EntityMeta
	Kind         IntegrationKind   `json:"kind"`
	Name         string            `json:"name"`
	CredentialID string            `json:"credential_id,omitempty"`
	Config       IntegrationConfig `json:"config"`
}

type serializedIntegration struct {
	EntityMeta
	Kind         IntegrationKind   `json:"kind"`
	Name         string            `json:"name"`
	CredentialID string            `json:"credential_id,omitempty"`
	Config       IntegrationConfig `json:"config"`
}

func (integration Integration) Validate() error {
	if err := integration.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid integration metadata: %w", err)
	}
	if strings.TrimSpace(string(integration.Kind)) == "" || strings.TrimSpace(integration.Name) == "" {
		return errors.New("integration kind and name must not be empty")
	}
	if err := integration.Kind.Validate(); err != nil {
		return err
	}
	if integration.CredentialID != "" && !IsUUID(integration.CredentialID) {
		return errors.New("integration credential id must be a canonical UUID")
	}
	if err := integration.Config.Validate(); err != nil {
		return fmt.Errorf("invalid integration config: %w", err)
	}
	return nil
}

func (integration *Integration) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var serialized serializedIntegration
	if err := decoder.Decode(&serialized); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("integration JSON must contain exactly one value")
		}
		return err
	}
	candidate := Integration{
		EntityMeta:   serialized.EntityMeta,
		Kind:         serialized.Kind,
		Name:         serialized.Name,
		CredentialID: serialized.CredentialID,
		Config:       serialized.Config,
	}
	if err := candidate.Validate(); err != nil {
		return err
	}
	*integration = candidate
	return nil
}

func validateServiceBaseURL(value string) error {
	if strings.TrimSpace(value) != value || value == "" {
		return errors.New("URL must not be empty or padded with whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("URL scheme must be http or https")
	}
	if parsed.Hostname() == "" {
		return errors.New("URL host must not be empty")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return errors.New("URL port must be between 1 and 65535")
		}
	}
	if parsed.User != nil {
		return errors.New("URL userinfo is forbidden")
	}
	if parsed.RawQuery != "" {
		return errors.New("URL query is forbidden")
	}
	if parsed.Fragment != "" {
		return errors.New("URL fragment is forbidden")
	}
	return nil
}

func validateCaseRevisionRefs(refs []CaseRevisionRef) error {
	if len(refs) == 0 {
		return errors.New("at least one pinned case revision is required")
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if err := ref.Validate(); err != nil {
			return err
		}
		if _, exists := seen[ref.CaseID]; exists {
			return fmt.Errorf("duplicate case id %q", ref.CaseID)
		}
		seen[ref.CaseID] = struct{}{}
	}
	return nil
}

func validateUUIDList(kind string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("at least one %s id is required", kind)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !IsUUID(value) {
			return fmt.Errorf("%s id must be a canonical UUID", kind)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s id %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateArtifactMetadata(runID, relativePath, digest, mediaType string, redacted bool, expectedRunID ...string) error {
	if !IsUUID(runID) {
		return errors.New("run id must be a canonical UUID")
	}
	if len(expectedRunID) > 1 {
		return errors.New("at most one expected run id may be supplied")
	}
	if len(expectedRunID) == 1 {
		if !IsUUID(expectedRunID[0]) {
			return errors.New("expected run id must be a canonical UUID")
		}
		if runID != expectedRunID[0] {
			return errors.New("artifact run id does not match its owner")
		}
	}
	if err := validatePortableRelativePath(relativePath); err != nil {
		return err
	}
	if !isLowerHexDigest(digest) {
		return errors.New("SHA-256 must contain 64 lowercase hex characters")
	}
	parsedType, _, err := mime.ParseMediaType(mediaType)
	if err != nil || !strings.Contains(parsedType, "/") {
		return errors.New("media type must be a valid type/subtype")
	}
	if !redacted {
		return errors.New("artifact must be marked redacted")
	}
	return nil
}

func validatePortableRelativePath(value string) error {
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsRune(value, '\x00') {
		return errors.New("artifact path must be a non-empty relative path")
	}
	if strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return errors.New("artifact path must use portable relative slash syntax")
	}
	for _, character := range value {
		if character < 32 || character == 127 || strings.ContainsRune(`<>:"\|?*`, character) {
			return errors.New("artifact path contains a Windows-unsafe character")
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned != value || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("artifact path must not traverse or require normalization")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("artifact path contains an unsafe segment")
		}
		if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
			return errors.New("artifact path segment must not end in a dot or space")
		}
		base := strings.ToUpper(strings.TrimRight(strings.SplitN(segment, ".", 2)[0], ". "))
		if isWindowsReservedName(base) {
			return fmt.Errorf("artifact path uses reserved Windows device name %q", segment)
		}
	}
	return nil
}

func isWindowsReservedName(base string) bool {
	switch base {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}

func isLowerHexDigest(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func isSafeCredentialSuffix(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, character := range value {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9')) {
			return false
		}
	}
	return true
}
