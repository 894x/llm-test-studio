package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

const (
	testRunID        = "123e4567-e89b-42d3-a456-426614174010"
	testPlanID       = "123e4567-e89b-42d3-a456-426614174011"
	testModelID      = "123e4567-e89b-42d3-a456-426614174012"
	testChannelID    = "123e4567-e89b-42d3-a456-426614174013"
	testCaseID       = "123e4567-e89b-42d3-a456-426614174014"
	testEvidenceID   = "123e4567-e89b-42d3-a456-426614174015"
	testAttachmentID = "123e4567-e89b-42d3-a456-426614174016"
)

func validEntityMeta(id string) EntityMeta {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	return EntityMeta{
		ID: id, SchemaVersion: CurrentEntitySchemaVersion, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}

func validRunSnapshot() RunSnapshot {
	return RunSnapshot{
		SchemaVersion: CurrentRunSnapshotSchemaVersion,
		Plan:          EntityRevisionRef{ID: testPlanID, Revision: 3},
		Model: ModelSnapshot{
			EntityRevisionRef: EntityRevisionRef{ID: testModelID, Revision: 2},
			Name:              "Model", Protocol: ProtocolOpenAIChat, Capabilities: []string{"chat"},
		},
		Channel: ChannelSnapshot{
			EntityRevisionRef: EntityRevisionRef{ID: testChannelID, Revision: 4},
			Name:              "primary", BaseURL: "https://api.example.test/v1",
			Protocol: ProtocolOpenAIChat, UpstreamModelName: "model-upstream",
		},
		Cases: []CaseRevisionRef{{CaseID: testCaseID, Revision: 7}},
		Load: LoadProfile{
			Mode: LoadFixedConcurrency, Concurrency: 2, RequestCount: 10,
			RequestTimeoutMS: 30_000,
		},
		SLA: SLAProfile{Thresholds: map[string]float64{
			"max_error_rate": 0.01,
			"max_ttft_ms":    2_000,
		}},
		Environment: EnvironmentSnapshot{
			OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct",
			AppVersion: "dev", EngineVersion: "v1",
		},
	}
}

func validTestCaseDefinition() TestCaseDefinition {
	return TestCaseDefinition{
		SchemaVersion: CurrentTestCaseDefinitionSchemaVersion,
		Type:          CaseType("request.single"),
		TypeVersion:   1,
		Spec:          json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{"Content-Type":"application/json"},"body":{"model":"model-upstream","max_tokens":64}},"expected":{"allowed_http_statuses":[200],"stream_completion":"required"},"assertions":[{"kind":"stream_end","config":{"marker":"[DONE]"}}]}`),
	}
}

func validEvidence() Evidence {
	return Evidence{
		EntityMeta:   validEntityMeta(testEvidenceID),
		RunID:        testRunID,
		RelativePath: "evidence/request.json",
		SHA256:       strings.Repeat("a", 64),
		MediaType:    "application/json",
		Redacted:     true,
	}
}

func validResult() Result {
	return Result{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174017"),
		RunID:      testRunID,
		CaseID:     testCaseID,
		RequestID:  "request-1",
		Success:    SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true},
		Metrics:    map[string]float64{"ttft_ms": 120},
		EvidenceIDs: []string{
			testEvidenceID,
		},
	}
}

func validReport() Report {
	snapshot := validRunSnapshot()
	return Report{
		SchemaVersion: CurrentReportSchemaVersion,
		ID:            "123e4567-e89b-42d3-a456-426614174018",
		RunID:         testRunID,
		RunStatus:     RunCompleted,
		GeneratedAt:   time.Date(2026, 8, 30, 10, 5, 0, 0, time.UTC),
		PlanSnapshot:  snapshot,
		Model:         ReportSubject{ID: testModelID, Name: "Model"},
		Channel:       ReportSubject{ID: testChannelID, Name: "primary"},
		Environment:   snapshot.Environment,
		Conclusion:    ReportConclusion{Passed: true, Verdict: "qualified", Issues: []string{}},
		SLA:           map[string]MetricValue{"max_ttft": {Value: 2_000, Unit: "ms", Samples: 1}},
		Metrics:       map[string]MetricValue{"ttft_p50": {Value: 120, Unit: "ms", Samples: 10}},
		Timeline:      []json.RawMessage{},
		Distributions: []json.RawMessage{},
		CaseResults:   []Result{validResult()},
		ErrorClusters: []json.RawMessage{},
		Evidence:      []Evidence{validEvidence()},
		Baseline:      json.RawMessage(`{}`),
		Attachments: []ReportAttachment{{
			ArtifactID: testAttachmentID, RunID: testRunID, Name: "request",
			RelativePath: "evidence/request.json", SHA256: strings.Repeat("a", 64),
			MediaType: "application/json", Redacted: true,
		}},
	}
}

func TestEntityMetaRejectsUnsupportedSchemaNonUTCAndRevisionOverflow(t *testing.T) {
	valid := validEntityMeta("123e4567-e89b-42d3-a456-426614174000")

	unsupported := valid
	unsupported.SchemaVersion++
	if err := unsupported.Validate(); err == nil {
		t.Fatal("metadata with a future schema version validated")
	}

	nonUTC := valid
	nonUTC.UpdatedAt = nonUTC.UpdatedAt.In(time.FixedZone("UTC+8", 8*60*60))
	if err := nonUTC.Validate(); err == nil {
		t.Fatal("metadata with a non-UTC timestamp validated")
	}

	overflow := valid
	overflow.Revision = math.MaxUint64
	if _, err := overflow.NextRevision(overflow.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("maximum revision wrapped around")
	}
}

func TestEntityMetaAcceptsZeroOffsetTimezoneAndUUIDRejectsNilValue(t *testing.T) {
	meta := validEntityMeta("123e4567-e89b-42d3-a456-426614174000")
	zeroOffset := time.FixedZone("GMT", 0)
	meta.CreatedAt = meta.CreatedAt.In(zeroOffset)
	meta.UpdatedAt = meta.UpdatedAt.In(zeroOffset)
	if err := meta.Validate(); err != nil {
		t.Fatalf("zero-offset timestamps were rejected: %v", err)
	}
	if IsUUID("00000000-0000-0000-0000-000000000000") {
		t.Fatal("nil UUID was accepted as an entity identity")
	}
}

func TestChannelValidationRejectsCredentialBearingBaseURL(t *testing.T) {
	base := Channel{
		EntityMeta: validEntityMeta(testChannelID), Name: "primary",
		BaseURL: "https://api.example.test/v1", Protocol: ProtocolOpenAIChat, Enabled: true,
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("safe channel Validate() error = %v", err)
	}

	for _, unsafeURL := range []string{
		"https://alice:password@api.example.test/v1",
		"https://api.example.test/v1?api_key=plaintext",
		"https://:443/v1",
		"https://api.example.test:0/v1",
		"https://api.example.test:65536/v1",
	} {
		channel := base
		channel.BaseURL = unsafeURL
		if err := channel.Validate(); err == nil {
			t.Fatalf("credential-bearing BaseURL %q validated", unsafeURL)
		}
	}
}

func TestIntegrationUsesTypedAllowlistedConfiguration(t *testing.T) {
	integration := Integration{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174020"),
		Kind:       IntegrationNewAPI, Name: "production",
		Config: IntegrationConfig{
			BaseURL: "https://admin.example.test", Region: "secretary_name", ExternalID: "credential_mode",
		},
	}
	if err := integration.Validate(); err != nil {
		t.Fatalf("typed non-secret Integration.Validate() error = %v", err)
	}
}

func TestIntegrationJSONRejectsEveryUnknownConfigField(t *testing.T) {
	for _, forbidden := range []string{"api_key", "auth", "key", "passphrase", "cookie", "pem"} {
		raw := fmt.Sprintf(`{"id":"123e4567-e89b-42d3-a456-426614174020","schema_version":1,"revision":1,"created_at":"2026-08-30T10:00:00Z","updated_at":"2026-08-30T10:00:00Z","kind":"new-api","name":"production","config":{"base_url":"https://admin.example.test",%q:"plaintext"}}`, forbidden)
		var integration Integration
		if err := json.Unmarshal([]byte(raw), &integration); err == nil {
			t.Fatalf("unknown config field %q was accepted", forbidden)
		}
	}
}

func TestCredentialReferenceOnlyKeepsFourSafeSuffixCharacters(t *testing.T) {
	base := CredentialRef{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174022"),
		StoreRef:   "llm-test-studio/channel/primary", Purpose: CredentialChannelAPIKey,
		MaskedSuffix: "aB09", Fingerprint: "sha256:" + strings.Repeat("a", 64),
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("CredentialRef.Validate() error = %v", err)
	}
	for _, unsafe := range []string{"abc", "abcde", "…abc", "ab c", "ab\n1"} {
		candidate := base
		candidate.MaskedSuffix = unsafe
		if err := candidate.Validate(); err == nil {
			t.Fatalf("unsafe masked suffix %q validated", unsafe)
		}
	}
}

func TestSuiteAndPlanPinEveryCaseRevision(t *testing.T) {
	suite := Suite{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174021"),
		Name:       "admission",
		Cases:      []CaseRevisionRef{{CaseID: testCaseID, Revision: 7}},
	}
	if err := suite.Validate(); err != nil {
		t.Fatalf("Suite.Validate() error = %v", err)
	}

	plan := Plan{
		EntityMeta: validEntityMeta(testPlanID), Name: "explicit smoke",
		ModelIDs: []string{testModelID}, ChannelIDs: []string{testChannelID},
		Cases: suite.Cases, Load: validRunSnapshot().Load, SLA: validRunSnapshot().SLA,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("explicit-case Plan.Validate() error = %v", err)
	}

	plan.Cases[0].Revision = 0
	if err := plan.Validate(); err == nil {
		t.Fatal("plan containing an unpinned case revision validated")
	}
}

func TestPlanAllowsTargetsToBeSelectedAtRunTime(t *testing.T) {
	plan := Plan{
		EntityMeta: validEntityMeta(testPlanID), Name: "runtime target smoke",
		Cases: []CaseRevisionRef{{CaseID: testCaseID, Revision: 7}},
		Load:  validRunSnapshot().Load, SLA: validRunSnapshot().SLA,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("targetless Plan.Validate() error = %v", err)
	}

	plan.ModelIDs = []string{testModelID}
	if err := plan.Validate(); err == nil {
		t.Fatal("plan with only a model allowlist validated")
	}

	plan.ModelIDs = nil
	plan.ChannelIDs = []string{testChannelID}
	if err := plan.Validate(); err == nil {
		t.Fatal("plan with only a channel allowlist validated")
	}
}

func TestChannelModelAndTestCaseValidateTheirOwnedIdentity(t *testing.T) {
	mapping := ChannelModel{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174023"),
		ChannelID:  testChannelID, ModelID: testModelID, UpstreamModelName: "model-upstream",
	}
	if err := mapping.Validate(); err != nil {
		t.Fatalf("ChannelModel.Validate() error = %v", err)
	}

	testCase := TestCase{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174024"),
		Key:        "T001", Name: "chat smoke", Dimension: "boundary",
		Protocol: ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: CaseSeverityCritical, ExecutionMode: CaseExecutionAutomatic,
		Definition: validTestCaseDefinition(),
	}
	if err := testCase.Validate(); err != nil {
		t.Fatalf("TestCase.Validate() error = %v", err)
	}
	testCase.Definition.Spec = json.RawMessage(`[]`)
	if err := testCase.Validate(); err == nil {
		t.Fatal("test case with a non-object request body validated")
	}
}

func TestTestCaseValidatesCatalogPolicyFields(t *testing.T) {
	t.Parallel()

	valid := TestCase{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174024"),
		Key:        "must.tool_call", Name: "tool call", Dimension: "tools",
		Protocol: ProtocolKimiK3, Enabled: true,
		Severity: CaseSeverityNormal, ExecutionMode: CaseExecutionAutomatic,
		Definition: validTestCaseDefinition(),
	}
	for _, test := range []struct {
		name   string
		mutate func(*TestCase)
	}{
		{name: "empty key", mutate: func(value *TestCase) { value.Key = "" }},
		{name: "unsafe key", mutate: func(value *TestCase) { value.Key = "../case" }},
		{name: "empty dimension", mutate: func(value *TestCase) { value.Dimension = "" }},
		{name: "unsupported severity", mutate: func(value *TestCase) { value.Severity = "urgent" }},
		{name: "unsupported execution", mutate: func(value *TestCase) { value.ExecutionMode = "sometimes" }},
		{name: "disabled default", mutate: func(value *TestCase) { value.Enabled = false; value.Default = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("TestCase.Validate() accepted %s", test.name)
			}
		})
	}
}

func TestTestCaseDefinitionRejectsCredentialsInOpaqueSpec(t *testing.T) {
	definition := validTestCaseDefinition()
	if err := definition.Validate(); err != nil {
		t.Fatalf("TestCaseDefinition.Validate() error = %v", err)
	}

	for _, spec := range []string{
		`{"api_key":"plaintext"}`,
		`{"key":"sk-plaintext"}`,
		`{"token":"opaque-plaintext"}`,
		`{"nested":{"client_secret":"plaintext"}}`,
		`{"password":"plaintext"}`,
	} {
		candidate := validTestCaseDefinition()
		candidate.Spec = json.RawMessage(spec)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("secret-bearing spec validated: %s", spec)
		}
	}
}

func TestTestCaseDefinitionRejectsCredentialLikeValueUnderBenignName(t *testing.T) {
	candidate := validTestCaseDefinition()
	candidate.Spec = json.RawMessage(`{"value":"sk-plaintext"}`)
	if err := candidate.Validate(); err == nil {
		t.Fatal("credential-like value under a benign field name validated")
	}
}

func TestTestCaseDefinitionValidatesV2Envelope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*TestCaseDefinition)
	}{
		{"schema", func(value *TestCaseDefinition) { value.SchemaVersion++ }},
		{"type", func(value *TestCaseDefinition) { value.Type = "Request.Single" }},
		{"type version", func(value *TestCaseDefinition) { value.TypeVersion = 0 }},
		{"empty spec", func(value *TestCaseDefinition) { value.Spec = json.RawMessage(`{}`) }},
		{"spec type", func(value *TestCaseDefinition) { value.Spec = json.RawMessage(`[]`) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := validTestCaseDefinition()
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid definition validated: %#v", candidate)
			}
		})
	}
}

func TestTestCaseDefinitionJSONRejectsUnknownCredentialFields(t *testing.T) {
	raw := `{"schema_version":2,"type":"request.single","type_version":1,"spec":{"api_key":"plaintext"},"request":{"method":"POST"}}`
	var definition TestCaseDefinition
	if err := json.Unmarshal([]byte(raw), &definition); err == nil {
		t.Fatal("unknown credential field in request JSON was accepted")
	}
}

func TestTestCaseDefinitionMarshalRejectsSecretBearingBody(t *testing.T) {
	definition := validTestCaseDefinition()
	definition.Spec = json.RawMessage(`{"api_key":"plaintext"}`)
	if _, err := json.Marshal(definition); err == nil {
		t.Fatal("secret-bearing test definition was serialized")
	}
}

func TestAssertionKindCoversV1EvaluatorExtensionPoints(t *testing.T) {
	for _, kind := range []AssertionKind{
		AssertionResponseSchema, AssertionStreamEnd, AssertionText, AssertionJSON,
		AssertionToolCall, AssertionMultimodal, AssertionCustom,
	} {
		assertion := TestAssertion{Kind: kind, Config: json.RawMessage(`{"enabled":true}`)}
		if err := assertion.Validate(); err != nil {
			t.Errorf("assertion kind %q was rejected: %v", kind, err)
		}
	}
}

func TestRunSnapshotIsCompleteAndDefensivelyCopied(t *testing.T) {
	snapshot := validRunSnapshot()
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	if run.Meta().ID != testRunID || run.PlanID() != testPlanID || run.Status() != RunQueued {
		t.Fatalf("run accessors returned inconsistent identity: meta=%#v plan=%q status=%q", run.Meta(), run.PlanID(), run.Status())
	}

	snapshot.Cases[0].Revision = 99
	snapshot.SLA.Thresholds["max_ttft_ms"] = 99
	stored := run.Snapshot()
	if stored.Cases[0].Revision != 7 || stored.SLA.Thresholds["max_ttft_ms"] != 2_000 {
		t.Fatalf("run retained caller aliases: %#v", stored)
	}

	stored.Cases[0].Revision = 88
	stored.SLA.Thresholds["max_ttft_ms"] = 88
	again := run.Snapshot()
	if again.Cases[0].Revision != 7 || again.SLA.Thresholds["max_ttft_ms"] != 2_000 {
		t.Fatalf("Snapshot() exposed mutable internal data: %#v", again)
	}

	incomplete := validRunSnapshot()
	incomplete.Model = ModelSnapshot{}
	if _, err := NewRun(validEntityMeta(testRunID), testPlanID, incomplete); err == nil {
		t.Fatal("run accepted an incomplete typed snapshot")
	}
}

func TestRunMarshalRejectsInvalidInternalState(t *testing.T) {
	if _, err := json.Marshal(Run{}); err == nil {
		t.Fatal("invalid zero Run was serialized")
	}
}

func TestRunJSONRoundTripRestoresValidatedPrivateSnapshot(t *testing.T) {
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, validRunSnapshot())
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	var decoded Run
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded Run.Validate() error = %v; JSON = %s", err, encoded)
	}
	if decoded.Snapshot().Cases[0].Revision != 7 {
		t.Fatalf("decoded snapshot = %#v", decoded.Snapshot())
	}
}

func TestEvidenceValidationEnforcesRedactedPortableArtifactMetadata(t *testing.T) {
	evidence := validEvidence()
	if err := evidence.Validate(testRunID); err != nil {
		t.Fatalf("Evidence.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Evidence)
	}{
		{"unredacted", func(value *Evidence) { value.Redacted = false }},
		{"parent traversal", func(value *Evidence) { value.RelativePath = "../secret.txt" }},
		{"absolute path", func(value *Evidence) { value.RelativePath = `C:\\secret.txt` }},
		{"invalid digest", func(value *Evidence) { value.SHA256 = "abc" }},
		{"invalid media type", func(value *Evidence) { value.MediaType = "json" }},
		{"wrong run", func(value *Evidence) { value.RunID = testPlanID }},
		{"windows reserved name", func(value *Evidence) { value.RelativePath = "evidence/CON.json" }},
		{"padded windows reserved name", func(value *Evidence) { value.RelativePath = "evidence/NUL .json" }},
		{"reserved punctuation", func(value *Evidence) { value.RelativePath = "evidence/request?.json" }},
		{"control character", func(value *Evidence) { value.RelativePath = "evidence/request\x01.json" }},
		{"trailing dot", func(value *Evidence) { value.RelativePath = "evidence/request." }},
		{"trailing space", func(value *Evidence) { value.RelativePath = "evidence/request " }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := evidence
			test.mutate(&candidate)
			if err := candidate.Validate(testRunID); err == nil {
				t.Fatalf("invalid evidence validated: %#v", candidate)
			}
		})
	}
}

func TestReportAttachmentValidationEnforcesRunAndArtifactSafety(t *testing.T) {
	attachment := validReport().Attachments[0]
	if err := attachment.Validate(testRunID); err != nil {
		t.Fatalf("ReportAttachment.Validate() error = %v", err)
	}

	attachment.Redacted = false
	if err := attachment.Validate(testRunID); err == nil {
		t.Fatal("unredacted attachment validated")
	}
}

func TestResultValidationEnforcesOutcomeEnumsAndEvidenceReferences(t *testing.T) {
	valid := validResult()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Result.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Result)
	}{
		{"unknown failure", func(value *Result) {
			value.Success.Semantic = false
			value.Failure = "mystery"
			value.ErrorCode = "semantic_failed"
		}},
		{"missing failure", func(value *Result) { value.Success.Semantic = false }},
		{"failure on success", func(value *Result) { value.Failure = FailureHTTP; value.ErrorCode = "http_error" }},
		{"protocol without transport", func(value *Result) {
			value.Success.Transport = false
			value.Failure = FailureNetwork
			value.ErrorCode = "network_error"
		}},
		{"network failure with transport success", func(value *Result) {
			value.Success.SLA = false
			value.Failure = FailureNetwork
			value.ErrorCode = "network_error"
		}},
		{"http failure without transport success", func(value *Result) {
			value.Success = SuccessDimensions{}
			value.Failure = FailureHTTP
			value.ErrorCode = "http_error"
		}},
		{"semantic failure before protocol success", func(value *Result) {
			value.Success = SuccessDimensions{Transport: true}
			value.Failure = FailureSemantic
			value.ErrorCode = "semantic_error"
		}},
		{"SLA failure before semantic success", func(value *Result) {
			value.Success = SuccessDimensions{Transport: true, Protocol: true}
			value.Failure = FailureSLA
			value.ErrorCode = "sla_error"
		}},
		{"invalid evidence id", func(value *Result) { value.EvidenceIDs = []string{"not-a-uuid"} }},
		{"non-finite metric", func(value *Result) { value.Metrics = map[string]float64{"ttft_ms": math.NaN()} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.mutate(&candidate)
			if err := candidate.Validate(); err == nil {
				t.Fatalf("invalid result validated: %#v", candidate)
			}
		})
	}
}

func TestFailureKindsMatchTheFirstFailedLayer(t *testing.T) {
	tests := []struct {
		kind       FailureKind
		dimensions SuccessDimensions
	}{
		{FailureNetwork, SuccessDimensions{}},
		{FailureTimeout, SuccessDimensions{}},
		{FailureCancelled, SuccessDimensions{}},
		{FailureHTTP, SuccessDimensions{Transport: true}},
		{FailureProtocol, SuccessDimensions{Transport: true}},
		{FailureRateLimit, SuccessDimensions{Transport: true}},
		{FailureSemantic, SuccessDimensions{Transport: true, Protocol: true}},
		{FailureSLA, SuccessDimensions{Transport: true, Protocol: true, Semantic: true}},
	}
	for _, test := range tests {
		result := validResult()
		result.Success = test.dimensions
		result.Failure = test.kind
		result.ErrorCode = "classified_failure"
		if err := result.Validate(); err != nil {
			t.Errorf("%q rejected at its first failed layer: %v", test.kind, err)
		}
	}
}

func TestErrorCodeUsesBoundedStableSnakeCase(t *testing.T) {
	for _, valid := range []ErrorCode{"http_error", "rate_limit_429", "provider2_timeout"} {
		if err := valid.Validate(); err != nil {
			t.Errorf("valid error code %q rejected: %v", valid, err)
		}
	}
	for _, invalid := range []ErrorCode{
		"", "HTTP_ERROR", "http-error", "_http_error", "http__error", "2provider_error",
		ErrorCode(strings.Repeat("a", MaxErrorCodeLength+1)),
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("invalid error code %q validated", invalid)
		}
	}
}

func TestProviderDetailIsRedactedBoundedAndAbsentOnSuccess(t *testing.T) {
	failed := validResult()
	failed.Success = SuccessDimensions{Transport: true}
	failed.Failure = FailureHTTP
	failed.ErrorCode = "http_error"
	failed.Detail = &ProviderDetail{Value: "provider rejected request", Redacted: true}
	if err := failed.Validate(); err != nil {
		t.Fatalf("failed Result.Validate() error = %v", err)
	}

	unredacted := failed
	unredacted.Detail = &ProviderDetail{Value: "plaintext provider response", Redacted: false}
	if err := unredacted.Validate(); err == nil {
		t.Fatal("unredacted provider detail validated")
	}

	tooLong := failed
	tooLong.Detail = &ProviderDetail{Value: strings.Repeat("x", MaxProviderDetailLength+1), Redacted: true}
	if err := tooLong.Validate(); err == nil {
		t.Fatal("oversized provider detail validated")
	}

	success := validResult()
	success.Detail = &ProviderDetail{Value: "should not exist", Redacted: true}
	if err := success.Validate(); err == nil {
		t.Fatal("successful result retained provider failure detail")
	}
}

func TestReportV1SerializesEveryRequiredSectionAndValidatesNestedData(t *testing.T) {
	report := validReport()
	if err := report.Validate(); err != nil {
		t.Fatalf("Report.Validate() error = %v", err)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, required := range []string{
		"schema_version", "id", "run_id", "run_status", "generated_at", "plan_snapshot", "model", "channel",
		"environment", "conclusion", "sla", "metrics", "timeline", "distributions", "case_results",
		"error_clusters", "evidence", "baseline", "attachments",
	} {
		if _, ok := fields[required]; !ok {
			t.Errorf("report JSON omitted required key %q: %s", required, encoded)
		}
	}

	report.Metrics["ttft_p50"] = MetricValue{Value: math.Inf(1), Unit: "ms", Samples: 10}
	if err := report.Validate(); err == nil {
		t.Fatal("report containing an infinite metric validated")
	}
}

func TestReportValidationRejectsCrossRunOrMissingEvidence(t *testing.T) {
	report := validReport()
	report.Evidence[0].RunID = testPlanID
	if err := report.Validate(); err == nil {
		t.Fatal("report accepted evidence from another run")
	}

	report = validReport()
	report.CaseResults[0].EvidenceIDs = []string{testAttachmentID}
	if err := report.Validate(); err == nil {
		t.Fatal("report accepted a result referencing absent evidence")
	}
}

func TestReportValidationClosesSnapshotCaseAndSubjectIdentity(t *testing.T) {
	report := validReport()
	report.Model.Name = "renamed model"
	if err := report.Validate(); err == nil {
		t.Fatal("report model name diverged from the snapshot")
	}

	report = validReport()
	report.Channel.Name = "renamed channel"
	if err := report.Validate(); err == nil {
		t.Fatal("report channel name diverged from the snapshot")
	}

	report = validReport()
	report.CaseResults[0].CaseID = "123e4567-e89b-42d3-a456-426614174099"
	if err := report.Validate(); err == nil {
		t.Fatal("report accepted a result for a case outside the snapshot")
	}

	report = validReport()
	report.CaseResults = append(report.CaseResults, report.CaseResults[0])
	if err := report.Validate(); err == nil {
		t.Fatal("report accepted duplicate result identities")
	}

	report = validReport()
	second := report.CaseResults[0]
	second.EntityMeta = validEntityMeta("123e4567-e89b-42d3-a456-426614174098")
	second.RequestID = "request-2"
	report.CaseResults = append(report.CaseResults, second)
	if err := report.Validate(); err != nil {
		t.Fatalf("performance report rejected repeated observations for one planned case: %v", err)
	}

	report = validReport()
	report.CaseResults = []Result{}
	if err := report.Validate(); err == nil {
		t.Fatal("report omitted a case pinned by the snapshot")
	}
}

func TestReportTerminalStatusControlsResultCoverage(t *testing.T) {
	for _, status := range []RunStatus{RunFailed, RunCancelled} {
		report := validReport()
		report.RunStatus = status
		report.Conclusion.Passed = false
		report.Conclusion.Verdict = "incomplete"
		report.CaseResults = []Result{}
		if err := report.Validate(); err != nil {
			t.Errorf("%s report with no fabricated results was rejected: %v", status, err)
		}
	}

	report := validReport()
	report.RunStatus = RunRunning
	if err := report.Validate(); err == nil {
		t.Fatal("non-terminal run status produced a final report")
	}

	report = validReport()
	report.RunStatus = RunFailed
	if err := report.Validate(); err == nil {
		t.Fatal("failed run produced a passing conclusion")
	}
}

func TestReportValidationRejectsNonUTCAndNilRequiredSections(t *testing.T) {
	report := validReport()
	report.GeneratedAt = report.GeneratedAt.In(time.FixedZone("UTC+8", 8*60*60))
	if err := report.Validate(); err == nil {
		t.Fatal("report with a non-UTC generation timestamp validated")
	}

	report = validReport()
	report.Timeline = nil
	if err := report.Validate(); err == nil {
		t.Fatal("report with a nil required timeline validated")
	}
}
