package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode"

	"github.com/894x/llm-test-studio/internal/testspec"
)

const CurrentTestCaseDefinitionSchemaVersion = 2

type CaseType string

type RequestMethod string

const (
	RequestGET    RequestMethod = "GET"
	RequestPOST   RequestMethod = "POST"
	RequestPUT    RequestMethod = "PUT"
	RequestPATCH  RequestMethod = "PATCH"
	RequestDELETE RequestMethod = "DELETE"
)

func (method RequestMethod) Validate() error {
	switch method {
	case RequestGET, RequestPOST, RequestPUT, RequestPATCH, RequestDELETE:
		return nil
	default:
		return fmt.Errorf("unsupported test request method %q", method)
	}
}

type TestRequest struct {
	Method  RequestMethod     `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Body    json.RawMessage   `json:"body"`
}

func (request TestRequest) Validate() error {
	if err := request.Method.Validate(); err != nil {
		return err
	}
	if err := validateRequestPath(request.Path); err != nil {
		return err
	}
	if request.Headers == nil {
		return errors.New("test request headers must not be nil")
	}
	for name, value := range request.Headers {
		if !isHTTPHeaderName(name) || strings.TrimSpace(value) != value {
			return fmt.Errorf("invalid test request header %q", name)
		}
		if isCredentialHeader(name) || looksLikeCredentialValue(value) {
			return fmt.Errorf("credential-bearing header %q is forbidden in a test definition", name)
		}
	}
	body := bytes.TrimSpace(request.Body)
	if len(body) == 0 || bytes.Equal(body, []byte("null")) {
		return nil
	}
	if err := validateSafeJSONObject(body); err != nil {
		return fmt.Errorf("invalid test request body: %w", err)
	}
	return nil
}

type StreamCompletionExpectation string

const (
	StreamCompletionNotApplicable StreamCompletionExpectation = "not_applicable"
	StreamCompletionRequired      StreamCompletionExpectation = "required"
	StreamCompletionForbidden     StreamCompletionExpectation = "forbidden"
)

type TestExpected struct {
	AllowedHTTPStatuses []int                       `json:"allowed_http_statuses"`
	StreamCompletion    StreamCompletionExpectation `json:"stream_completion"`
}

func (expected TestExpected) Validate() error {
	if len(expected.AllowedHTTPStatuses) == 0 {
		return errors.New("test expected requires at least one HTTP status")
	}
	seen := make(map[int]struct{}, len(expected.AllowedHTTPStatuses))
	for _, status := range expected.AllowedHTTPStatuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("invalid expected HTTP status %d", status)
		}
		if _, duplicate := seen[status]; duplicate {
			return fmt.Errorf("duplicate expected HTTP status %d", status)
		}
		seen[status] = struct{}{}
	}
	switch expected.StreamCompletion {
	case StreamCompletionNotApplicable, StreamCompletionRequired, StreamCompletionForbidden:
		return nil
	default:
		return fmt.Errorf("unsupported stream completion expectation %q", expected.StreamCompletion)
	}
}

type AssertionKind string

const (
	AssertionResponseSchema AssertionKind = "response_schema"
	AssertionStreamEnd      AssertionKind = "stream_end"
	AssertionText           AssertionKind = "text"
	AssertionJSON           AssertionKind = "json"
	AssertionToolCall       AssertionKind = "tool_call"
	AssertionMultimodal     AssertionKind = "multimodal"
	AssertionCustom         AssertionKind = "custom"
)

type TestAssertion struct {
	Kind   AssertionKind   `json:"kind"`
	Config json.RawMessage `json:"config"`
}

func (assertion TestAssertion) Validate() error {
	switch assertion.Kind {
	case AssertionResponseSchema, AssertionStreamEnd, AssertionText, AssertionJSON,
		AssertionToolCall, AssertionMultimodal, AssertionCustom:
	default:
		return fmt.Errorf("unsupported test assertion kind %q", assertion.Kind)
	}
	object, err := decodeSafeJSONObject(assertion.Config)
	if err != nil {
		return fmt.Errorf("invalid %s assertion config: %w", assertion.Kind, err)
	}
	if len(object) == 0 {
		return fmt.Errorf("%s assertion config must not be empty", assertion.Kind)
	}
	return nil
}

type TestCaseDefinition struct {
	SchemaVersion int             `json:"schema_version"`
	Type          CaseType        `json:"type"`
	TypeVersion   uint32          `json:"type_version"`
	Spec          json.RawMessage `json:"spec"`
}

type serializedTestCaseDefinition TestCaseDefinition

func (definition TestCaseDefinition) Validate() error {
	if definition.SchemaVersion != CurrentTestCaseDefinitionSchemaVersion {
		return fmt.Errorf("unsupported test case definition schema version %d", definition.SchemaVersion)
	}
	if err := Protocol(definition.Type).Validate(); err != nil {
		return errors.New("unsupported case type; use a registered protocol type")
	}
	if definition.TypeVersion != 1 {
		return errors.New("unsupported case type format; use type_version 1 with the current protocol spec")
	}
	object, err := decodeSafeJSONObject(definition.Spec)
	if err != nil {
		return fmt.Errorf("invalid test case definition spec: %w", err)
	}
	if len(object) == 0 {
		return errors.New("test case definition spec must not be empty")
	}
	if _, err := testspec.Decode(definition.Spec); err != nil {
		return err
	}
	return nil
}

func isSafeCaseType(value CaseType) bool {
	text := string(value)
	if text == "" || strings.TrimSpace(text) != text || strings.HasPrefix(text, ".") || strings.HasSuffix(text, ".") {
		return false
	}
	for _, segment := range strings.Split(text, ".") {
		if segment == "" {
			return false
		}
		for index, character := range segment {
			if character >= 'a' && character <= 'z' || index > 0 && (character >= '0' && character <= '9' || character == '_' || character == '-') {
				continue
			}
			return false
		}
	}
	return true
}

func (definition TestCaseDefinition) MarshalJSON() ([]byte, error) {
	if err := definition.Validate(); err != nil {
		return nil, fmt.Errorf("marshal test case definition: %w", err)
	}
	return json.Marshal(serializedTestCaseDefinition(definition))
}

func (definition *TestCaseDefinition) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var serialized serializedTestCaseDefinition
	if err := decoder.Decode(&serialized); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("test case definition JSON must contain exactly one value")
		}
		return err
	}
	candidate := TestCaseDefinition(serialized)
	if err := candidate.Validate(); err != nil {
		return err
	}
	*definition = candidate
	return nil
}

type TestCase struct {
	EntityMeta
	Key           string             `json:"key"`
	Name          string             `json:"name"`
	Dimension     string             `json:"dimension"`
	Protocol      Protocol           `json:"protocol"`
	Enabled       bool               `json:"enabled"`
	Default       bool               `json:"default"`
	Severity      CaseSeverity       `json:"severity"`
	ExecutionMode CaseExecutionMode  `json:"execution_mode"`
	Definition    TestCaseDefinition `json:"definition"`
}

type CaseSeverity string

const (
	CaseSeverityNormal   CaseSeverity = "normal"
	CaseSeverityCritical CaseSeverity = "critical"
)

func (severity CaseSeverity) Validate() error {
	switch severity {
	case CaseSeverityNormal, CaseSeverityCritical:
		return nil
	default:
		return fmt.Errorf("unsupported test case severity %q", severity)
	}
}

type CaseExecutionMode string

const (
	CaseExecutionAutomatic CaseExecutionMode = "automatic"
	CaseExecutionManual    CaseExecutionMode = "manual"
)

func (mode CaseExecutionMode) Validate() error {
	switch mode {
	case CaseExecutionAutomatic, CaseExecutionManual:
		return nil
	default:
		return fmt.Errorf("unsupported test case execution mode %q", mode)
	}
}

func (testCase TestCase) Validate() error {
	if err := testCase.EntityMeta.Validate(); err != nil {
		return fmt.Errorf("invalid test case metadata: %w", err)
	}
	if !isSafeCaseKey(testCase.Key) {
		return errors.New("test case key must contain only letters, digits, dot, underscore, or hyphen")
	}
	if strings.TrimSpace(testCase.Name) == "" {
		return errors.New("test case name must not be empty")
	}
	if strings.TrimSpace(testCase.Dimension) == "" || strings.TrimSpace(testCase.Dimension) != testCase.Dimension {
		return errors.New("test case dimension must be a trimmed non-empty value")
	}
	if err := testCase.Protocol.Validate(); err != nil {
		return err
	}
	if testCase.Default && !testCase.Enabled {
		return errors.New("a default test case must be enabled")
	}
	if err := testCase.Severity.Validate(); err != nil {
		return err
	}
	if err := testCase.ExecutionMode.Validate(); err != nil {
		return err
	}
	if err := testCase.Definition.Validate(); err != nil {
		return fmt.Errorf("invalid test case definition: %w", err)
	}
	if string(testCase.Definition.Type) != string(testCase.Protocol) {
		return errors.New("case type must equal protocol")
	}
	return nil
}

func isSafeCaseKey(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || (index > 0 && strings.ContainsRune("._-", character)) {
			continue
		}
		return false
	}
	return true
}

func validateRequestPath(value string) error {
	if value == "" || strings.TrimSpace(value) != value || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return errors.New("test request path must be an absolute-path reference")
	}
	if strings.ContainsAny(value, "?#\\") || path.Clean(value) != value {
		return errors.New("test request path must not contain query, fragment, backslash, or traversal")
	}
	return nil
}

func isHTTPHeaderName(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z') && !(character >= 'A' && character <= 'Z') &&
			!(character >= '0' && character <= '9') && !strings.ContainsRune("!#$%&'*+-.^_`|~", character) {
			return false
		}
	}
	return true
}

func isCredentialHeader(name string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	if normalized == "authorization" || normalized == "proxy-authorization" || normalized == "authentication" ||
		normalized == "auth" || normalized == "cookie" || normalized == "set-cookie" {
		return true
	}
	return normalized == "key" || normalized == "token" ||
		strings.Contains(normalized, "api-key") || strings.HasSuffix(normalized, "-key") ||
		strings.HasSuffix(normalized, "-auth") || strings.HasSuffix(normalized, "-token")
}

func looksLikeCredentialValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(normalized, "bearer ") || strings.HasPrefix(normalized, "basic ") ||
		strings.HasPrefix(normalized, "token ") || strings.HasPrefix(normalized, "sk-") ||
		strings.HasPrefix(normalized, "-----begin ")
}

func validateSafeJSONObject(raw json.RawMessage) error {
	_, err := decodeSafeJSONObject(raw)
	return err
}

func decodeSafeJSONObject(raw json.RawMessage) (map[string]any, error) {
	if err := validateJSONObject(raw); err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("value must be a JSON object")
	}
	if err := rejectCredentialMaterial(object, "object"); err != nil {
		return nil, err
	}
	return object, nil
}

func rejectCredentialMaterial(value any, location string) error {
	switch current := value.(type) {
	case map[string]any:
		for key, nested := range current {
			if isCredentialField(key) {
				return fmt.Errorf("credential field %q is forbidden at %s", key, location)
			}
			if err := rejectCredentialMaterial(nested, location+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for index, nested := range current {
			if err := rejectCredentialMaterial(nested, fmt.Sprintf("%s[%d]", location, index)); err != nil {
				return err
			}
		}
	case string:
		if looksLikeCredentialValue(current) {
			return fmt.Errorf("credential-like value is forbidden at %s", location)
		}
	}
	return nil
}

func isCredentialField(key string) bool {
	normalized := strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return unicode.ToLower(character)
		}
		return -1
	}, key)
	switch normalized {
	case "key", "token", "apikey", "authorization", "auth", "password", "passphrase", "secret", "clientsecret",
		"accesstoken", "refreshtoken", "authtoken", "bearertoken", "privatekey", "cookie",
		"sessioncookie", "credential", "credentials", "pem":
		return true
	default:
		return false
	}
}
