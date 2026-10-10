package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/testspec"
)

const (
	testRunID        = "123e4567-e89b-42d3-a456-426614174010"
	testPlanID       = "123e4567-e89b-42d3-a456-426614174011"
	testModelID      = "123e4567-e89b-42d3-a456-426614174012"
	testChannelID    = "123e4567-e89b-42d3-a456-426614174013"
	testCaseID       = "123e4567-e89b-42d3-a456-426614174014"
	testEvidenceID   = "123e4567-e89b-42d3-a456-426614174015"
	testAttachmentID = "123e4567-e89b-42d3-a456-426614174016"
	testSuiteID      = "123e4567-e89b-42d3-a456-426614174020"
	testPlanEntryID  = "123e4567-e89b-42d3-a456-426614174021"
)

func validEntityMeta(id string) EntityMeta {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	return EntityMeta{
		ID: id, SchemaVersion: CurrentEntitySchemaVersion, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
}

func validRunSnapshot() RunSnapshot {
	load := LoadProfile{
		Mode: LoadFixedConcurrency, Concurrency: 2, RequestCount: 10,
		RequestTimeoutMS: 30_000,
	}
	sla := SLAProfile{Thresholds: map[string]float64{
		"max_error_rate": 0.01,
		"max_ttft_ms":    2_000,
	}}
	caseRef := CaseRevisionRef{CaseID: testCaseID, Revision: 7}
	suite := Suite{
		EntityMeta: validEntityMeta(testSuiteID),
		Key:        "default-suite", Name: "Default Suite", Protocol: ProtocolOpenAIChat,
		Cases: []CaseRef{{CaseID: caseRef.CaseID}}, Inputs: []SuiteInput{},
	}
	plan := Plan{
		EntityMeta: validEntityMeta(testPlanID),
		Name:       "Plan",
		Protocol:   ProtocolOpenAIChat, Seed: 1,
		Entries: []PlanEntry{{
			EntryID: testPlanEntryID, TargetKind: PlanTargetSuite, TargetID: suite.ID,
			Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
	}
	plan.Revision = 3
	mapping := ChannelModel{Protocols: []Protocol{ProtocolOpenAIChat},
		EntityMeta:        validEntityMeta("123e4567-e89b-42d3-a456-426614174019"),
		ChannelID:         testChannelID,
		ModelID:           testModelID,
		UpstreamModelName: "model-upstream",
	}
	testCase := TestCase{
		EntityMeta: validEntityMeta(testCaseID),
		Key:        "T001", Name: "basic", Dimension: "compatibility",
		Enabled: true, Default: true,
		Severity: CaseSeverityNormal, ExecutionMode: CaseExecutionAutomatic, Definitions: validProtocolDefinitions(),
	}
	testCase.Revision = 7
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
		PlanDocument: &plan,
		Mapping:      &mapping,
		Entries: []RunEntrySnapshot{{
			EntryID: testPlanEntryID, TargetKind: PlanTargetSuite, TargetID: suite.ID, Name: suite.Name, Key: suite.Key, Suite: &suite, CaseInputs: map[string]map[string]json.RawMessage{testCaseID: {}}, Cases: []CaseRevisionRef{caseRef},
			CaseDefinitions: []TestCase{testCase}, Parameters: map[string]json.RawMessage{}, Load: load, SLA: sla,
		}},
		Environment: EnvironmentSnapshot{
			OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct",
			AppVersion: "dev", EngineVersion: "v1",
		},
	}
}

func TestRunSnapshotRejectsLegacySchema(t *testing.T) {
	snapshot := validRunSnapshot()
	snapshot.SchemaVersion = 99
	if err := snapshot.Validate(); err == nil {
		t.Fatal("legacy Run snapshot validated")
	}
}

func validProtocolDefinitions() ProtocolDefinitions {
	return ProtocolDefinitions{Protocol(CaseType("openai-chat")): json.RawMessage(`{"inputs":{},"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]}`)}
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
		RunID:      testRunID, EntryID: testPlanEntryID,
		CaseID:          testCaseID,
		RequestID:       "request-1",
		ExecutionStatus: ExecutionCompleted, Verification: testspec.Verdict{Status: testspec.VerdictPassed, Assertions: []testspec.AssertionResult{}},
		Metrics: map[string]float64{"ttft_ms": 120},
		EvidenceIDs: []string{
			testEvidenceID,
		},
	}
}

func validReport() Report {
	snapshot := validRunSnapshot()
	caseResult := validResult()
	caseResult.RequestID = ""
	suiteReport := EntryReport{
		EntryID: testPlanEntryID, TargetKind: PlanTargetSuite, TargetID: testSuiteID, Key: "default-suite", Name: "Default Suite", Protocol: ProtocolOpenAIChat, Parameters: snapshot.Entries[0].Parameters, Load: snapshot.Entries[0].Load, Seed: 1, Verification: VerificationSummary{Status: testspec.VerdictPassed, Passed: 1}, Status: EntryReportCompleted,
		Conclusion: ReportConclusion{Passed: true, Verdict: "pass", Issues: []string{}},
		SLA:        map[string]MetricValue{}, Metrics: map[string]MetricValue{},
		Timeline: []json.RawMessage{}, Distributions: []json.RawMessage{}, CaseResults: []Result{caseResult},
	}
	return Report{
		SchemaVersion: CurrentReportSchemaVersion, Protocol: ProtocolOpenAIChat, Verification: VerificationSummary{Status: testspec.VerdictPassed, Passed: 1},
		ID:            "123e4567-e89b-42d3-a456-426614174018",
		RunID:         testRunID,
		RunStatus:     RunCompleted,
		GeneratedAt:   time.Date(2026, 8, 30, 10, 5, 0, 0, time.UTC),
		PlanSnapshot:  snapshot,
		Model:         ReportSubject{ID: testModelID, Name: "Model"},
		Channel:       ReportSubject{ID: testChannelID, Name: "primary"},
		Environment:   snapshot.Environment,
		Conclusion:    ReportConclusion{Passed: true, Verdict: "pass", Issues: []string{}},
		SLA:           map[string]MetricValue{"max_ttft": {Value: 2_000, Unit: "ms", Samples: 1}},
		Metrics:       map[string]MetricValue{"ttft_p50": {Value: 120, Unit: "ms", Samples: 10}},
		Timeline:      []json.RawMessage{},
		Distributions: []json.RawMessage{},
		CaseResults:   []Result{caseResult},
		EntryReports:  []EntryReport{suiteReport},
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

func TestChannelModelAndTestCaseValidateTheirOwnedIdentity(t *testing.T) {
	mapping := ChannelModel{Protocols: []Protocol{ProtocolOpenAIChat},
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174023"),
		ChannelID:  testChannelID, ModelID: testModelID, UpstreamModelName: "model-upstream",
	}
	if err := mapping.Validate(); err != nil {
		t.Fatalf("ChannelModel.Validate() error = %v", err)
	}

	testCase := TestCase{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174024"),
		Key:        "T001", Name: "chat smoke", Dimension: "boundary",
		Enabled: true, Default: true,
		Severity: CaseSeverityCritical, ExecutionMode: CaseExecutionAutomatic, Definitions: validProtocolDefinitions(),
	}
	if err := testCase.Validate(); err != nil {
		t.Fatalf("TestCase.Validate() error = %v", err)
	}
	testCase.Definitions[ProtocolOpenAIChat] = json.RawMessage(`[]`)
	if err := testCase.Validate(); err == nil {
		t.Fatal("test case with a non-object request body validated")
	}
}

func TestTestCaseValidatesCatalogPolicyFields(t *testing.T) {
	t.Parallel()

	valid := TestCase{
		EntityMeta: validEntityMeta("123e4567-e89b-42d3-a456-426614174024"),
		Key:        "must.tool_call", Name: "tool call", Dimension: "tools",
		Enabled:  true,
		Severity: CaseSeverityNormal, ExecutionMode: CaseExecutionAutomatic, Definitions: validProtocolDefinitions(),
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

func TestProtocolDefinitionsRejectsCredentialsInOpaqueSpec(t *testing.T) {
	definition := validProtocolDefinitions()
	if err := definition.Validate(); err != nil {
		t.Fatalf("ProtocolDefinitions.Validate() error = %v", err)
	}

	for _, spec := range []string{
		`{"api_key":"plaintext"}`,
		`{"key":"sk-plaintext"}`,
		`{"token":"opaque-plaintext"}`,
		`{"nested":{"client_secret":"plaintext"}}`,
		`{"password":"plaintext"}`,
	} {
		candidate := validProtocolDefinitions()
		candidate[ProtocolOpenAIChat] = json.RawMessage(spec)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("secret-bearing spec validated: %s", spec)
		}
	}
}

func TestProtocolDefinitionsRejectsCredentialLikeValueUnderBenignName(t *testing.T) {
	candidate := validProtocolDefinitions()
	candidate[ProtocolOpenAIChat] = json.RawMessage(`{"value":"sk-plaintext"}`)
	if err := candidate.Validate(); err == nil {
		t.Fatal("credential-like value under a benign field name validated")
	}
}

func TestProtocolDefinitionsRejectInvalidMaps(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"unknown":{}}`,
		`{"openai-chat":{}}`, `{"openai-chat":[]}`,
		`{"openai-chat":{"inputs":{},"request":{"body":{}},"assertions":[]},"openai-chat":{"inputs":{},"request":{"body":{}},"assertions":[]}}`,
	} {
		original := validProtocolDefinitions()
		before, _ := json.Marshal(original)
		if err := json.Unmarshal([]byte(raw), &original); err == nil {
			t.Fatalf("accepted %s", raw)
		}
		after, _ := json.Marshal(original)
		if string(before) != string(after) {
			t.Fatal("rejected definitions were mutated")
		}
	}
}

func TestProtocolDefinitionsJSONRejectsUnknownCredentialFields(t *testing.T) {
	raw := `{"schema_version":2,"type":"request.single","type_version":1,"spec":{"api_key":"plaintext"},"request":{"method":"POST"}}`
	var definition ProtocolDefinitions
	if err := json.Unmarshal([]byte(raw), &definition); err == nil {
		t.Fatal("unknown credential field in request JSON was accepted")
	}
}

func TestProtocolDefinitionsMarshalRejectsSecretBearingBody(t *testing.T) {
	definition := validProtocolDefinitions()
	definition[ProtocolOpenAIChat] = json.RawMessage(`{"api_key":"plaintext"}`)
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

func TestWanVideoIsAValidCatalogProtocol(t *testing.T) {
	if err := Protocol("wan-video").Validate(); err != nil {
		t.Fatalf("wan-video protocol rejected: %v", err)
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

	snapshot.Entries[0].Cases[0].Revision = 99
	snapshot.Entries[0].SLA.Thresholds["max_ttft_ms"] = 99
	stored := run.Snapshot()
	if stored.Entries[0].Cases[0].Revision != 7 || stored.Entries[0].SLA.Thresholds["max_ttft_ms"] != 2_000 {
		t.Fatalf("run retained caller aliases: %#v", stored)
	}

	stored.Entries[0].Cases[0].Revision = 88
	stored.Entries[0].SLA.Thresholds["max_ttft_ms"] = 88
	again := run.Snapshot()
	if again.Entries[0].Cases[0].Revision != 7 || again.Entries[0].SLA.Thresholds["max_ttft_ms"] != 2_000 {
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
	if decoded.Snapshot().Entries[0].Cases[0].Revision != 7 {
		t.Fatalf("decoded snapshot = %#v", decoded.Snapshot())
	}
}

func TestRunReadValidationDoesNotDecodeFrozenCasesAgain(t *testing.T) {
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, validRunSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		if err := run.Validate(); err != nil {
			t.Fatal(err)
		}
	})
	if allocations > 10 {
		t.Fatalf("immutable Run validation allocated %v times", allocations)
	}
}

func TestDecodedRunAndTransitionOwnFrozenSnapshot(t *testing.T) {
	snapshot := validRunSnapshot()
	minimum := 1.0
	snapshot.Entries[0].Suite.Inputs = []SuiteInput{{
		Key: "size", Label: "Size",
		Input:    testspec.Input{Type: "integer", Default: json.RawMessage(`2`), Minimum: &minimum},
		Bindings: []SuiteInputBinding{{CaseID: testCaseID, Input: "size"}},
	}}
	snapshot.Entries[0].Parameters["size"] = json.RawMessage(`2`)
	snapshot.PlanDocument.Entries[0].Parameters["size"] = json.RawMessage(`2`)
	snapshot.Entries[0].CaseInputs[testCaseID]["size"] = json.RawMessage(`2`)
	snapshot.Entries[0].CaseDefinitions[0].Definitions[ProtocolOpenAIChat] = json.RawMessage(`{
		"inputs":{"size":{"type":"integer","default":2}},
		"request":{"body":{"messages":[{"role":"user","content":"hello"}]}},
		"assertions":[{"id":"status","source":"http.status","operator":"equals","value":200}]
	}`)
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Run
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	next, err := decoded.Transition(RunStarting, decoded.Meta().UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(decoded)
	nextBefore, _ := json.Marshal(next)
	for index := range encoded {
		encoded[index] = 'x'
	}
	for _, value := range []Run{decoded, next} {
		copy := value.Snapshot()
		copy.Entries[0].CaseDefinitions[0].Definitions[ProtocolOpenAIChat][0] = 'x'
		copy.Entries[0].Parameters["size"][0] = '9'
		copy.PlanDocument.Entries[0].Parameters["size"][0] = '9'
		copy.Entries[0].CaseInputs[testCaseID]["size"][0] = '9'
		copy.Entries[0].Suite.Inputs[0].Default[0] = '9'
		*copy.Entries[0].Suite.Inputs[0].Minimum = 9
		copy.Entries[0].Suite.Inputs[0].Bindings[0].Input = "changed"
	}
	actual, err := json.Marshal(decoded)
	nextActual, nextErr := json.Marshal(next)
	if err != nil || nextErr != nil || string(actual) != string(before) || string(nextActual) != string(nextBefore) {
		t.Fatal("decoded Run or its transition retained a mutable snapshot alias")
	}
}

func TestRunUnmarshalStillRejectsInvalidSnapshotWithoutChangingValidatedRun(t *testing.T) {
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, validRunSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	original, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(original, &document); err != nil {
		t.Fatal(err)
	}
	document["plan_snapshot"] = json.RawMessage(`{"schema_version":0}`)
	invalid, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(invalid, &run); err == nil {
		t.Fatal("a previously validated Run accepted an invalid replacement snapshot")
	}
	actual, err := json.Marshal(run)
	if err != nil || string(actual) != string(original) {
		t.Fatal("rejected snapshot changed the existing Run")
	}
}

func BenchmarkRunValidate(b *testing.B) {
	run, err := NewRun(validEntityMeta(testRunID), testPlanID, validRunSnapshot())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := run.Validate(); err != nil {
			b.Fatal(err)
		}
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

func TestProviderDetailIsRedactedAndBounded(t *testing.T) {
	failed := validResult()
	failed.Verification.Status = testspec.VerdictFailed
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
	if err := success.Validate(); err != nil {
		t.Fatal("redacted observation should be independent of verdict")
	}
}

func TestReportV2SerializesEveryRequiredSectionAndValidatesNestedData(t *testing.T) {
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

func TestReportRejectsSchemaOne(t *testing.T) {
	report := validReport()
	report.SchemaVersion = 99
	if err := report.Validate(); err == nil {
		t.Fatal("schema 1 report validated")
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
	if err := report.Validate(); err == nil {
		t.Fatal("final report accepted a request observation as a case summary")
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
		report.EntryReports[0].CaseResults = []Result{}
		report.EntryReports[0].Conclusion.Passed = false
		if status == RunCancelled {
			report.EntryReports[0].Status = EntryReportCancelled
			report.EntryReports[0].Conclusion.Verdict = "cancelled"
		} else {
			report.EntryReports[0].Status = EntryReportFailed
			report.EntryReports[0].Conclusion.Verdict = "fail"
		}
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

	report = validReport()
	report.RunStatus = RunFailed
	report.Conclusion = ReportConclusion{Passed: false, Verdict: "failed", Issues: []string{"failed"}}
	report.CaseResults = []Result{}
	report.EntryReports[0].Status = EntryReportCancelled
	report.EntryReports[0].Conclusion = ReportConclusion{Passed: false, Verdict: "cancelled", Issues: []string{"cancelled"}}
	report.EntryReports[0].CaseResults = []Result{}
	if err := report.Validate(); err == nil {
		t.Fatal("failed run accepted a cancelled Suite report")
	}
}

func TestReportRejectsEntryExecutionAfterCancellationBoundary(t *testing.T) {
	report := validReport()
	const secondEntryID = "123e4567-e89b-42d3-a456-426614174099"

	secondPlanEntry := report.PlanSnapshot.PlanDocument.Entries[0]
	secondPlanEntry.EntryID = secondEntryID
	report.PlanSnapshot.PlanDocument.Entries = append(report.PlanSnapshot.PlanDocument.Entries, secondPlanEntry)
	secondSnapshot := report.PlanSnapshot.Entries[0]
	secondSnapshot.EntryID = secondEntryID
	report.PlanSnapshot.Entries = append(report.PlanSnapshot.Entries, secondSnapshot)

	report.RunStatus = RunCancelled
	report.Conclusion = ReportConclusion{Passed: false, Verdict: "cancelled", Issues: []string{"cancelled"}}
	report.CaseResults = []Result{}
	first := report.EntryReports[0]
	first.Status = EntryReportCancelled
	first.Conclusion = ReportConclusion{Passed: false, Verdict: "cancelled", Issues: []string{"cancelled"}}
	first.CaseResults = []Result{}
	second := first
	second.EntryID = secondEntryID
	second.Status = EntryReportFailed
	second.Conclusion = ReportConclusion{Passed: false, Verdict: "failed", Issues: []string{"failed"}}
	report.EntryReports = []EntryReport{first, second}

	if err := report.Validate(); err == nil {
		t.Fatal("report accepted suite execution after a cancelled suite")
	}

	second.Status = EntryReportNotStarted
	second.Conclusion = ReportConclusion{Passed: false, Verdict: "not_started", Issues: []string{"not_started"}}
	report.EntryReports[1] = second
	if err := report.Validate(); err != nil {
		t.Fatalf("cancelled suite followed by not_started was rejected: %v", err)
	}
}

func TestEntryReportConclusionMatchesExecutionStatus(t *testing.T) {
	base := validReport()
	snapshot := base.PlanSnapshot.Entries[0]
	for name, mutate := range map[string]func(*EntryReport){
		"passing completed with fail verdict": func(report *EntryReport) {
			report.Conclusion.Verdict = "fail"
		},
		"failing completed with pass verdict": func(report *EntryReport) {
			report.Conclusion.Passed = false
		},
		"failed with cancelled verdict": func(report *EntryReport) {
			report.Status = EntryReportFailed
			report.Conclusion = ReportConclusion{Passed: false, Verdict: "cancelled", Issues: []string{}}
			report.CaseResults = []Result{}
		},
		"cancelled with fail verdict": func(report *EntryReport) {
			report.Status = EntryReportCancelled
			report.Conclusion = ReportConclusion{Passed: false, Verdict: "fail", Issues: []string{}}
			report.CaseResults = []Result{}
		},
		"not started with fail verdict": func(report *EntryReport) {
			report.Status = EntryReportNotStarted
			report.Conclusion = ReportConclusion{Passed: false, Verdict: "fail", Issues: []string{}}
			report.CaseResults = []Result{}
		},
		"not started with a case result": func(report *EntryReport) {
			report.Status = EntryReportNotStarted
			report.Conclusion = ReportConclusion{Passed: false, Verdict: "not_started", Issues: []string{}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			report := base.EntryReports[0]
			mutate(&report)
			if err := validateEntryReport(report, snapshot, base.RunID, RunFailed); err == nil {
				t.Fatal("suite report accepted a status/conclusion mismatch")
			}
		})
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
