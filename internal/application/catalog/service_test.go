package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/casetypes"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const (
	modelAID     = "11111111-1111-4111-8111-111111111111"
	modelBID     = "22222222-2222-4222-8222-222222222222"
	channelID    = "33333333-3333-4333-8333-333333333333"
	mappingAID   = "44444444-4444-4444-8444-444444444444"
	mappingBID   = "55555555-5555-4555-8555-555555555555"
	caseID       = "66666666-6666-4666-8666-666666666666"
	suiteID      = "77777777-7777-4777-8777-777777777777"
	planID       = "88888888-8888-4888-8888-888888888888"
	credentialID = "99999999-9999-4999-8999-999999999999"
)

var _ Repository = (*sqlite.Repository)(nil)

func TestNewRejectsNilAndTypedNilDependencies(t *testing.T) {
	var typedNilRepository *fakeRepository
	var typedNilClock *stubClock

	tests := []struct {
		name         string
		dependencies Dependencies
	}{
		{name: "nil repository", dependencies: Dependencies{Clock: &stubClock{now: fixtureTime()}}},
		{name: "typed nil repository", dependencies: Dependencies{Repository: typedNilRepository, Clock: &stubClock{now: fixtureTime()}}},
		{name: "nil clock", dependencies: Dependencies{Repository: &fakeRepository{}}},
		{name: "typed nil clock", dependencies: Dependencies{Repository: &fakeRepository{}, Clock: typedNilClock}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, err := New(test.dependencies)
			if service != nil || !errors.Is(err, ErrInvalid) {
				t.Fatalf("New() = (%v, %v), want (nil, ErrInvalid)", service, err)
			}
		})
	}
}

func TestSnapshotIsStableSecretFreeAndReadsEachCollectionOnce(t *testing.T) {
	repository := validRepository()
	repository.models[0], repository.models[1] = repository.models[1], repository.models[0]
	service := newTestService(t, repository, fixtureTime())

	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snapshot.SchemaVersion != CurrentSnapshotSchemaVersion {
		t.Fatalf("Snapshot().SchemaVersion = %d, want %d", snapshot.SchemaVersion, CurrentSnapshotSchemaVersion)
	}
	if got := []string{snapshot.Models[0].Name, snapshot.Models[1].Name}; got[0] != "Alpha" || got[1] != "Zulu" {
		t.Fatalf("Snapshot().Models order = %v, want [Alpha Zulu]", got)
	}
	if len(snapshot.Channels) != 1 || !snapshot.Channels[0].CredentialConfigured || snapshot.Channels[0].ModelCount != 2 {
		t.Fatalf("Snapshot().Channels = %#v, want configured channel with 2 models", snapshot.Channels)
	}
	if snapshot.Suites[0].CaseCount != 1 || snapshot.Plans[0].ModelCount != 2 || snapshot.Plans[0].ChannelCount != 1 || snapshot.Plans[0].CaseCount != 1 {
		t.Fatalf("Snapshot() relation counts are wrong: suite=%#v plan=%#v", snapshot.Suites[0], snapshot.Plans[0])
	}
	if got := snapshot.TestCases[0]; got.Key != "T001" || got.Dimension != "boundary" || !got.Enabled || !got.Default || got.Severity != domain.CaseSeverityCritical || got.ExecutionMode != domain.CaseExecutionAutomatic {
		t.Fatalf("Snapshot().TestCases[0] policy = %#v", got)
	}
	for name, calls := range repository.listCalls {
		if calls != 1 {
			t.Fatalf("Snapshot() %s list calls = %d, want 1", name, calls)
		}
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("json.Marshal(Snapshot()) error = %v", err)
	}
	for _, forbidden := range []string{credentialID, "credential_id", "store_ref", "api_key", "secret-material"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("Snapshot JSON contains forbidden value %q: %s", forbidden, encoded)
		}
	}
}

func TestSnapshotIncludesIndependentEditorPayloads(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())

	snapshot, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	testCase := snapshot.TestCases[0]
	if testCase.DefinitionSchemaVersion != domain.CurrentTestCaseDefinitionSchemaVersion ||
		testCase.Type != casetypes.TypeRequestSingle || testCase.TypeVersion != 1 ||
		string(testCase.Spec) != string(validRequestSingleSpec()) {
		t.Fatalf("Snapshot().TestCases[0] editor payload = %#v", testCase)
	}
	if len(snapshot.Suites[0].Cases) != 1 || snapshot.Suites[0].Cases[0].CaseID != caseID {
		t.Fatalf("Snapshot().Suites[0] editor payload = %#v", snapshot.Suites[0])
	}
	plan := snapshot.Plans[0]
	if len(plan.ModelIDs) != 2 || len(plan.ChannelIDs) != 1 || plan.SuiteID != suiteID || plan.SuiteRevision != 1 ||
		len(plan.Cases) != 1 || plan.Cases[0].CaseID != caseID || plan.SLAThresholds["p95_ms"] != 1500 {
		t.Fatalf("Snapshot().Plans[0] editor payload = %#v", plan)
	}

	testCase.Spec[0] = '['
	snapshot.Suites[0].Cases[0].Revision = 99
	plan.ModelIDs[0] = modelBID
	plan.SLAThresholds["p95_ms"] = 1

	fresh, err := service.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("second Snapshot() error = %v", err)
	}
	if string(fresh.TestCases[0].Spec) != string(validRequestSingleSpec()) ||
		fresh.Suites[0].Cases[0].Revision != 1 || fresh.Plans[0].ModelIDs[0] != modelAID || fresh.Plans[0].SLAThresholds["p95_ms"] != 1500 {
		t.Fatalf("Snapshot() editor payload aliases repository state: %#v", fresh)
	}
}

func TestDeleteCommandsValidateIdentityAndDelegateByEntityKind(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*Service, context.Context, DeleteCommand) error
		kind   string
		id     string
	}{
		{name: "model", invoke: (*Service).DeleteModel, kind: "model", id: modelAID},
		{name: "channel", invoke: (*Service).DeleteChannel, kind: "channel", id: channelID},
		{name: "channel model", invoke: (*Service).DeleteChannelModel, kind: "channel_model", id: mappingAID},
		{name: "test case", invoke: (*Service).DeleteTestCase, kind: "test_case", id: caseID},
		{name: "suite", invoke: (*Service).DeleteSuite, kind: "suite", id: suiteID},
		{name: "plan", invoke: (*Service).DeletePlan, kind: "plan", id: planID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := validRepository()
			service := newTestService(t, repository, fixtureTime())
			command := DeleteCommand{ID: test.id, ExpectedRevision: 1}

			if err := test.invoke(service, context.Background(), command); err != nil {
				t.Fatalf("delete error = %v", err)
			}
			if got := repository.deleted[test.kind]; got != command {
				t.Fatalf("repository delete command = %#v, want %#v", got, command)
			}

			if err := test.invoke(service, context.Background(), DeleteCommand{ID: test.id, ExpectedRevision: 0}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid delete error = %v, want ErrInvalid", err)
			}
			if len(repository.deleted) != 1 {
				t.Fatalf("invalid delete reached repository: %#v", repository.deleted)
			}
		})
	}
}

func TestSnapshotFailsClosedOnDuplicateBadReferenceAndProtocolMismatch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeRepository)
	}{
		{name: "duplicate id", mutate: func(repository *fakeRepository) {
			repository.models = append(repository.models, repository.models[0])
		}},
		{name: "mapping references missing model", mutate: func(repository *fakeRepository) {
			repository.mappings[0].ModelID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		}},
		{name: "mapping protocol mismatch", mutate: func(repository *fakeRepository) {
			repository.models[0].Protocol = domain.ProtocolKimiK3
		}},
		{name: "plan case protocol mismatch", mutate: func(repository *fakeRepository) {
			repository.testCases[0].Protocol = domain.ProtocolKimiK3
		}},
		{name: "suite references future case revision", mutate: func(repository *fakeRepository) {
			repository.suites[0].Cases[0].Revision = 2
		}},
		{name: "plan references missing channel", mutate: func(repository *fakeRepository) {
			repository.plans[0].ChannelIDs[0] = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := validRepository()
			test.mutate(repository)
			service := newTestService(t, repository, fixtureTime())
			if _, err := service.Snapshot(context.Background()); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Snapshot() error = %v, want ErrCorrupt", err)
			}
		})
	}
}

func TestListMethodsReturnSortedIndependentAllowListCopies(t *testing.T) {
	repository := validRepository()
	repository.models[0], repository.models[1] = repository.models[1], repository.models[0]
	service := newTestService(t, repository, fixtureTime())
	ctx := context.Background()

	models, err := service.ListModels(ctx)
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	channels, err := service.ListChannels(ctx)
	if err != nil {
		t.Fatalf("ListChannels() error = %v", err)
	}
	mappings, err := service.ListChannelModels(ctx)
	if err != nil {
		t.Fatalf("ListChannelModels() error = %v", err)
	}
	testCases, err := service.ListTestCases(ctx)
	if err != nil {
		t.Fatalf("ListTestCases() error = %v", err)
	}
	suites, err := service.ListSuites(ctx)
	if err != nil {
		t.Fatalf("ListSuites() error = %v", err)
	}
	plans, err := service.ListPlans(ctx)
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}
	if models[0].Name != "Alpha" || len(channels) != 1 || len(mappings) != 2 || len(testCases) != 1 || len(suites) != 1 || len(plans) != 1 {
		t.Fatalf("list summaries are incomplete: %#v %#v %#v %#v %#v %#v", models, channels, mappings, testCases, suites, plans)
	}
	models[0].Capabilities[0] = "caller-mutated"
	testCases[0].Spec[0] = '['
	fresh, err := service.ListModels(ctx)
	if err != nil {
		t.Fatalf("second ListModels() error = %v", err)
	}
	if fresh[0].Capabilities[0] == "caller-mutated" || repository.models[1].Capabilities[0] == "caller-mutated" {
		t.Fatal("ListModels() returned an aliased capabilities slice")
	}
	freshCases, err := service.ListTestCases(ctx)
	if err != nil || string(freshCases[0].Spec) != string(validRequestSingleSpec()) {
		t.Fatal("ListTestCases() returned an aliased spec")
	}
}

func TestCreateCommandsOwnMetadataAndDoNotAcceptEntityMeta(t *testing.T) {
	repository := validRepository()
	now := fixtureTime().Add(10 * time.Minute)
	ids := []string{
		"a1111111-1111-4111-8111-111111111111", "a2222222-2222-4222-8222-222222222222",
		"a3333333-3333-4333-8333-333333333333", "a4444444-4444-4444-8444-444444444444",
		"a5555555-5555-4555-8555-555555555555", "a6666666-6666-4666-8666-666666666666",
	}
	service := newTestServiceWithFactory(t, repository, now, sequentialMetaFactory(ids))
	ctx := context.Background()
	encodedCommand, err := json.Marshal(CreateModelCommand{Name: "New model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}})
	if err != nil {
		t.Fatalf("json.Marshal(CreateModelCommand) error = %v", err)
	}
	if string(encodedCommand) != `{"name":"New model","protocol":"openai-chat","capabilities":["chat"]}` {
		t.Fatalf("CreateModelCommand JSON = %s, want stable allow-list fields", encodedCommand)
	}

	modelResult, err := service.CreateModel(ctx, CreateModelCommand{Name: "New model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}})
	assertMutation(t, modelResult, ids[0], err)
	channelResult, err := service.CreateChannel(ctx, CreateChannelCommand{Name: "New channel", BaseURL: "https://example.com/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true})
	assertMutation(t, channelResult, ids[1], err)
	mappingResult, err := service.CreateChannelModel(ctx, CreateChannelModelCommand{ChannelID: channelID, ModelID: modelAID, UpstreamModelName: "upstream"})
	assertMutation(t, mappingResult, ids[2], err)
	caseResult, err := service.CreateTestCase(ctx, validCreateTestCaseCommand("New case"))
	assertMutation(t, caseResult, ids[3], err)
	suiteResult, err := service.CreateSuite(ctx, CreateSuiteCommand{
		Key: "gpt-smoke", Name: "New suite", Protocol: domain.ProtocolOpenAIChat, ModelTarget: "alpha-upstream",
		Cases: []CaseRevisionInput{{CaseID: caseID, Revision: 1}},
	})
	assertMutation(t, suiteResult, ids[4], err)
	planResult, err := service.CreatePlan(ctx, validCreatePlanCommand("New plan"))
	assertMutation(t, planResult, ids[5], err)

	for _, meta := range []domain.EntityMeta{
		repository.createdModel.EntityMeta, repository.createdChannel.EntityMeta, repository.createdMapping.EntityMeta,
		repository.createdTestCase.EntityMeta, repository.createdSuite.EntityMeta, repository.createdPlan.EntityMeta,
	} {
		if meta.SchemaVersion != domain.CurrentEntitySchemaVersion || meta.Revision != 1 || !meta.CreatedAt.Equal(now.UTC()) || !meta.UpdatedAt.Equal(now.UTC()) {
			t.Fatalf("created metadata = %#v, want schema 1 revision 1 at %s", meta, now.UTC())
		}
	}
}

func TestUpdateCommandsPreserveCreatedAtAndAdvanceRevisionAndTime(t *testing.T) {
	repository := validRepository()
	now := fixtureTime()
	service := newTestService(t, repository, now)
	ctx := context.Background()

	modelResult, err := service.UpdateModel(ctx, UpdateModelCommand{ID: modelAID, ExpectedRevision: 1, Name: "Renamed model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}})
	assertUpdate(t, modelResult, modelAID, err, repository.updatedModel.EntityMeta)
	channelResult, err := service.UpdateChannel(ctx, UpdateChannelCommand{ID: channelID, ExpectedRevision: 1, Name: "Renamed channel", BaseURL: "https://example.com/v2", Protocol: domain.ProtocolOpenAIChat, Enabled: false})
	assertUpdate(t, channelResult, channelID, err, repository.updatedChannel.EntityMeta)
	if repository.updatedChannel.CredentialID != credentialID {
		t.Fatalf("UpdateChannel() credential id = %q, want existing binding preserved", repository.updatedChannel.CredentialID)
	}
	mappingResult, err := service.UpdateChannelModel(ctx, UpdateChannelModelCommand{ID: mappingAID, ExpectedRevision: 1, UpstreamModelName: "renamed-upstream"})
	assertUpdate(t, mappingResult, mappingAID, err, repository.updatedMapping.EntityMeta)
	caseCommand := validUpdateTestCaseCommand(caseID, "Renamed case")
	caseResult, err := service.UpdateTestCase(ctx, caseCommand)
	assertUpdate(t, caseResult, caseID, err, repository.updatedTestCase.EntityMeta)
	suiteResult, err := service.UpdateSuite(ctx, UpdateSuiteCommand{
		ID: suiteID, ExpectedRevision: 1, Key: "gpt-smoke", Name: "Renamed suite",
		Protocol: domain.ProtocolOpenAIChat, ModelTarget: "alpha-upstream",
		Cases: []CaseRevisionInput{{CaseID: caseID, Revision: 1}},
	})
	assertUpdate(t, suiteResult, suiteID, err, repository.updatedSuite.EntityMeta)
	planCommand := validUpdatePlanCommand(planID, "Renamed plan")
	planResult, err := service.UpdatePlan(ctx, planCommand)
	assertUpdate(t, planResult, planID, err, repository.updatedPlan.EntityMeta)

	if repository.updatedMapping.ChannelID != channelID || repository.updatedMapping.ModelID != modelAID {
		t.Fatalf("UpdateChannelModel() changed immutable binding identity: %#v", repository.updatedMapping)
	}
}

func TestUpdatesRejectStaleRevisionWithoutWriting(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime().Add(time.Hour))

	_, err := service.UpdateModel(context.Background(), UpdateModelCommand{
		ID: modelAID, ExpectedRevision: 7, Name: "stale", Protocol: domain.ProtocolOpenAIChat,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateModel() error = %v, want ErrConflict", err)
	}
	if repository.updateModelCalls != 0 {
		t.Fatalf("UpdateModel() write calls = %d, want 0", repository.updateModelCalls)
	}

	repository.models[0].Revision = math.MaxUint64
	_, err = service.UpdateModel(context.Background(), UpdateModelCommand{
		ID: modelAID, ExpectedRevision: math.MaxUint64, Name: "overflow", Protocol: domain.ProtocolOpenAIChat,
	})
	if !errors.Is(err, ErrConflict) || repository.updateModelCalls != 0 {
		t.Fatalf("UpdateModel(overflow) = %v calls=%d, want ErrConflict and no write", err, repository.updateModelCalls)
	}

	t.Run("protocol identity is immutable", func(t *testing.T) {
		repository := validRepository()
		service := newTestService(t, repository, fixtureTime().Add(time.Hour))
		if _, err := service.UpdateModel(context.Background(), UpdateModelCommand{ID: modelAID, ExpectedRevision: 1, Name: "model", Protocol: domain.ProtocolKimiK3}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("UpdateModel(protocol change) error = %v, want ErrInvalid", err)
		}
		if _, err := service.UpdateChannel(context.Background(), UpdateChannelCommand{ID: channelID, ExpectedRevision: 1, Name: "channel", BaseURL: "https://example.com/v1", Protocol: domain.ProtocolKimiK3}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("UpdateChannel(protocol change) error = %v, want ErrInvalid", err)
		}
		command := validUpdateTestCaseCommand(caseID, "case")
		command.Protocol = domain.ProtocolKimiK3
		if _, err := service.UpdateTestCase(context.Background(), command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("UpdateTestCase(protocol change) error = %v, want ErrInvalid", err)
		}
		if repository.updateModelCalls != 0 || repository.updateChannelCalls != 0 || repository.updateTestCaseCalls != 0 {
			t.Fatalf("protocol change writes = model:%d channel:%d case:%d, want zero", repository.updateModelCalls, repository.updateChannelCalls, repository.updateTestCaseCalls)
		}
	})
}

func TestValidationAndRepositoryErrorsAreStableAndSecretFree(t *testing.T) {
	repository := validRepository()
	portNotFound := errors.New("driver missing secret-material")
	portConflict := errors.New("driver conflict secret-material")
	portCorrupt := errors.New("driver corrupt secret-material")
	service, err := New(Dependencies{
		Repository:       repository,
		Clock:            &stubClock{now: fixtureTime()},
		RepositoryErrors: RepositoryErrorSet{NotFound: portNotFound, Conflict: portConflict, Corrupt: portCorrupt},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	_, err = service.CreateChannel(context.Background(), CreateChannelCommand{
		Name: "unsafe", BaseURL: "https://user:super-secret@example.com", Protocol: domain.ProtocolOpenAIChat,
	})
	if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("CreateChannel() error = %q, want safe ErrInvalid", err)
	}
	missingHeaders := validCreateTestCaseCommand("missing headers")
	missingHeaders.Spec = json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":null,"body":{}},"expected":{"allowed_http_statuses":[200],"stream_completion":"required"},"assertions":[{"kind":"text","config":{"contains":"ok"}}]}`)
	if _, err := service.CreateTestCase(context.Background(), missingHeaders); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateTestCase(nil headers) error = %v, want ErrInvalid", err)
	}
	protocolMismatch := validRepository()
	protocolMismatch.testCases[0].Protocol = domain.ProtocolKimiK3
	protocolService := newTestService(t, protocolMismatch, fixtureTime())
	if _, err := protocolService.CreatePlan(context.Background(), validCreatePlanCommand("bad protocol plan")); !errors.Is(err, ErrInvalid) || err == ErrInvalid || err.Error() != "catalog: invalid input: plan target protocol mismatch" {
		t.Fatalf("CreatePlan(case protocol mismatch) error = %v, want safe protocol-specific ErrInvalid", err)
	}
	if protocolMismatch.createPlanCalls != 0 {
		t.Fatalf("CreatePlan(case protocol mismatch) writes = %d, want 0", protocolMismatch.createPlanCalls)
	}
	if _, err := protocolService.UpdatePlan(context.Background(), validUpdatePlanCommand(planID, "bad protocol update")); !errors.Is(err, ErrInvalid) || err == ErrInvalid || err.Error() != "catalog: invalid input: plan target protocol mismatch" {
		t.Fatalf("UpdatePlan(case protocol mismatch) error = %v, want safe protocol-specific ErrInvalid", err)
	}
	if protocolMismatch.updatePlanCalls != 0 {
		t.Fatalf("UpdatePlan(case protocol mismatch) writes = %d, want 0", protocolMismatch.updatePlanCalls)
	}

	for _, test := range []struct {
		portError error
		want      error
	}{
		{portError: portNotFound, want: ErrNotFound},
		{portError: portConflict, want: ErrConflict},
		{portError: portCorrupt, want: ErrCorrupt},
		{portError: errors.New("driver unavailable secret-material"), want: ErrUnavailable},
	} {
		repository.listModelsErr = test.portError
		_, err := service.Snapshot(context.Background())
		if !errors.Is(err, test.want) || strings.Contains(err.Error(), "secret-material") {
			t.Fatalf("Snapshot() error = %q, want safe %v", err, test.want)
		}
	}
}

func TestCatalogEnforcesCaseTypeCreationPolicy(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())

	create := validCreateTestCaseCommand("reserved case")
	create.Type = casetypes.TypeLegacyAPIAudit
	create.Spec = json.RawMessage(`{"kind":"chat_sync","request":{"method":"POST","path":"/v1/chat/completions","headers":{},"body":{"messages":[{"role":"user","content":"hello"}]}},"options":{}}`)
	if _, err := service.CreateTestCase(context.Background(), create); !errors.Is(err, ErrInvalid) {
		t.Fatalf("CreateTestCase(non-creatable type) error = %v, want ErrInvalid", err)
	}

	update := validUpdateTestCaseCommand(caseID, "reserved transition")
	update.Type = casetypes.TypeLegacyAPIAudit
	update.Spec = create.Spec
	if _, err := service.UpdateTestCase(context.Background(), update); !errors.Is(err, ErrInvalid) {
		t.Fatalf("UpdateTestCase(non-creatable transition) error = %v, want ErrInvalid", err)
	}
	if repository.createTestCaseCalls != 0 || repository.updateTestCaseCalls != 0 {
		t.Fatalf("reserved case type writes = create:%d update:%d, want zero", repository.createTestCaseCalls, repository.updateTestCaseCalls)
	}
}

func TestContextCancellationWinsBeforeAndAfterRepositoryCalls(t *testing.T) {
	repository := validRepository()
	service := newTestService(t, repository, fixtureTime())
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.CreateModel(cancelled, CreateModelCommand{Name: "", Protocol: "secret-protocol"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CreateModel(cancelled) error = %v, want context.Canceled", err)
	}
	if repository.createModelCalls != 0 {
		t.Fatalf("CreateModel(cancelled) calls = %d, want 0", repository.createModelCalls)
	}

	afterCall, cancelAfterCall := context.WithCancel(context.Background())
	repository.afterListModels = cancelAfterCall
	_, err = service.Snapshot(afterCall)
	if !errors.Is(err, context.Canceled) || repository.listCalls["channels"] != 0 {
		t.Fatalf("Snapshot(cancel after first read) error/calls = (%v, %#v), want cancellation before second read", err, repository.listCalls)
	}

	repository = validRepository()
	service = newTestService(t, repository, fixtureTime())
	cancelDuringProjection := &cancelAfterErrChecks{cancelAt: 8}
	if _, err := service.Snapshot(cancelDuringProjection); !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot(cancel during projection) error = %v, want context.Canceled", err)
	}

	t.Run("cancellation beats corrupt and stale update state", func(t *testing.T) {
		for _, corrupt := range []bool{false, true} {
			repository := validRepository()
			if corrupt {
				repository.models[0].Name = ""
			}
			ctx, cancel := context.WithCancel(context.Background())
			repository.afterGetModel = cancel
			service := newTestService(t, repository, fixtureTime())
			expected := uint64(7)
			if corrupt {
				expected = 1
			}
			_, err := service.UpdateModel(ctx, UpdateModelCommand{ID: modelAID, ExpectedRevision: expected, Name: "updated", Protocol: domain.ProtocolOpenAIChat})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("UpdateModel(cancelled, corrupt=%t) error = %v, want context.Canceled", corrupt, err)
			}
		}
	})
}

func TestMutableCommandDataIsDeepCopiedAndFactoryFailuresFailClosed(t *testing.T) {
	repository := validRepository()
	now := fixtureTime().Add(time.Hour)
	service := newTestServiceWithFactory(t, repository, now, sequentialMetaFactory([]string{"aaaaaaaa-1111-4111-8111-111111111111", "aaaaaaaa-2222-4222-8222-222222222222"}))

	caseCommand := validCreateTestCaseCommand("copy case")
	planCommand := validCreatePlanCommand("copy plan")
	if _, err := service.CreateTestCase(context.Background(), caseCommand); err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	if _, err := service.CreatePlan(context.Background(), planCommand); err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	caseCommand.Spec[2] = 'X'
	planCommand.ModelIDs[0] = modelBID
	planCommand.Cases[0].CaseID = modelAID
	planCommand.SLAThresholds["p95_ms"] = 9999
	if string(repository.createdTestCase.Definition.Spec) != string(validRequestSingleSpec()) {
		t.Fatalf("CreateTestCase() retained caller aliases: %#v", repository.createdTestCase)
	}
	if repository.createdPlan.ModelIDs[0] != modelAID || repository.createdPlan.Cases[0].CaseID != caseID || repository.createdPlan.SLA.Thresholds["p95_ms"] != 1500 {
		t.Fatalf("CreatePlan() retained caller aliases: %#v", repository.createdPlan)
	}

	badFactoryService, err := New(Dependencies{
		Repository: validRepository(), Clock: &stubClock{now: now},
		MetaFactory: func(time.Time) (domain.EntityMeta, error) { return domain.EntityMeta{}, nil },
	})
	if err != nil {
		t.Fatalf("New(bad factory) error = %v", err)
	}
	if _, err := badFactoryService.CreateModel(context.Background(), CreateModelCommand{Name: "model", Protocol: domain.ProtocolOpenAIChat}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("CreateModel(bad factory) error = %v, want ErrCorrupt", err)
	}
}

type stubClock struct{ now time.Time }

func (clock *stubClock) Now() time.Time { return clock.now }

type cancelAfterErrChecks struct {
	checks   int
	cancelAt int
}

func (*cancelAfterErrChecks) Deadline() (time.Time, bool) { return time.Time{}, false }
func (*cancelAfterErrChecks) Done() <-chan struct{}       { return nil }
func (ctx *cancelAfterErrChecks) Err() error {
	ctx.checks++
	if ctx.checks >= ctx.cancelAt {
		return context.Canceled
	}
	return nil
}
func (*cancelAfterErrChecks) Value(any) any { return nil }

type fakeRepository struct {
	models    []domain.Model
	channels  []domain.Channel
	mappings  []domain.ChannelModel
	testCases []domain.TestCase
	suites    []domain.Suite
	plans     []domain.Plan

	listCalls       map[string]int
	listModelsErr   error
	afterListModels func()

	createdModel        domain.Model
	createdChannel      domain.Channel
	createdMapping      domain.ChannelModel
	createdTestCase     domain.TestCase
	createdSuite        domain.Suite
	createdPlan         domain.Plan
	updatedModel        domain.Model
	updatedChannel      domain.Channel
	updatedMapping      domain.ChannelModel
	updatedTestCase     domain.TestCase
	updatedSuite        domain.Suite
	updatedPlan         domain.Plan
	createModelCalls    int
	createTestCaseCalls int
	createPlanCalls     int
	updateModelCalls    int
	updateChannelCalls  int
	updateTestCaseCalls int
	updatePlanCalls     int
	afterGetModel       func()
	deleted             map[string]DeleteCommand
}

func (repository *fakeRepository) ListModels(context.Context) ([]domain.Model, error) {
	repository.count("models")
	if repository.afterListModels != nil {
		repository.afterListModels()
	}
	return repository.models, repository.listModelsErr
}
func (repository *fakeRepository) ListChannels(context.Context) ([]domain.Channel, error) {
	repository.count("channels")
	return repository.channels, nil
}
func (repository *fakeRepository) ListChannelModels(context.Context) ([]domain.ChannelModel, error) {
	repository.count("mappings")
	return repository.mappings, nil
}
func (repository *fakeRepository) ListTestCases(context.Context) ([]domain.TestCase, error) {
	repository.count("test_cases")
	return repository.testCases, nil
}
func (repository *fakeRepository) ListSuites(context.Context) ([]domain.Suite, error) {
	repository.count("suites")
	return repository.suites, nil
}
func (repository *fakeRepository) ListPlans(context.Context) ([]domain.Plan, error) {
	repository.count("plans")
	return repository.plans, nil
}

func (repository *fakeRepository) GetModel(_ context.Context, id string) (domain.Model, error) {
	for _, value := range repository.models {
		if value.ID == id {
			if repository.afterGetModel != nil {
				repository.afterGetModel()
			}
			return value, nil
		}
	}
	return domain.Model{}, ErrNotFound
}
func (repository *fakeRepository) GetChannel(_ context.Context, id string) (domain.Channel, error) {
	for _, value := range repository.channels {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.Channel{}, ErrNotFound
}
func (repository *fakeRepository) GetChannelModel(_ context.Context, id string) (domain.ChannelModel, error) {
	for _, value := range repository.mappings {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.ChannelModel{}, ErrNotFound
}
func (repository *fakeRepository) GetTestCase(_ context.Context, id string) (domain.TestCase, error) {
	for _, value := range repository.testCases {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.TestCase{}, ErrNotFound
}
func (repository *fakeRepository) GetSuite(_ context.Context, id string) (domain.Suite, error) {
	for _, value := range repository.suites {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.Suite{}, ErrNotFound
}
func (repository *fakeRepository) GetPlan(_ context.Context, id string) (domain.Plan, error) {
	for _, value := range repository.plans {
		if value.ID == id {
			return value, nil
		}
	}
	return domain.Plan{}, ErrNotFound
}

func (repository *fakeRepository) CreateModel(_ context.Context, value domain.Model) error {
	repository.createModelCalls++
	repository.createdModel = value
	return nil
}
func (repository *fakeRepository) CreateChannel(_ context.Context, value domain.Channel) error {
	repository.createdChannel = value
	return nil
}
func (repository *fakeRepository) CreateChannelModel(_ context.Context, value domain.ChannelModel) error {
	repository.createdMapping = value
	return nil
}
func (repository *fakeRepository) CreateTestCase(_ context.Context, value domain.TestCase) error {
	repository.createTestCaseCalls++
	repository.createdTestCase = value
	return nil
}
func (repository *fakeRepository) CreateSuite(_ context.Context, value domain.Suite) error {
	repository.createdSuite = value
	return nil
}
func (repository *fakeRepository) CreatePlan(_ context.Context, value domain.Plan) error {
	repository.createPlanCalls++
	repository.createdPlan = value
	return nil
}

func (repository *fakeRepository) UpdateModel(_ context.Context, _ uint64, value domain.Model) error {
	repository.updateModelCalls++
	repository.updatedModel = value
	return nil
}
func (repository *fakeRepository) UpdateChannel(_ context.Context, _ uint64, value domain.Channel) error {
	repository.updateChannelCalls++
	repository.updatedChannel = value
	return nil
}
func (repository *fakeRepository) UpdateChannelModel(_ context.Context, _ uint64, value domain.ChannelModel) error {
	repository.updatedMapping = value
	return nil
}
func (repository *fakeRepository) UpdateTestCase(_ context.Context, _ uint64, value domain.TestCase) error {
	repository.updateTestCaseCalls++
	repository.updatedTestCase = value
	return nil
}
func (repository *fakeRepository) UpdateSuite(_ context.Context, _ uint64, value domain.Suite) error {
	repository.updatedSuite = value
	return nil
}
func (repository *fakeRepository) UpdatePlan(_ context.Context, _ uint64, value domain.Plan) error {
	repository.updatePlanCalls++
	repository.updatedPlan = value
	return nil
}

func (repository *fakeRepository) DeleteModel(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("model", id, expectedRevision)
	return nil
}
func (repository *fakeRepository) DeleteChannel(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("channel", id, expectedRevision)
	return nil
}
func (repository *fakeRepository) DeleteChannelModel(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("channel_model", id, expectedRevision)
	return nil
}
func (repository *fakeRepository) DeleteTestCase(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("test_case", id, expectedRevision)
	return nil
}
func (repository *fakeRepository) DeleteSuite(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("suite", id, expectedRevision)
	return nil
}
func (repository *fakeRepository) DeletePlan(_ context.Context, id string, expectedRevision uint64) error {
	repository.recordDelete("plan", id, expectedRevision)
	return nil
}

func (repository *fakeRepository) recordDelete(kind, id string, expectedRevision uint64) {
	if repository.deleted == nil {
		repository.deleted = make(map[string]DeleteCommand)
	}
	repository.deleted[kind] = DeleteCommand{ID: id, ExpectedRevision: expectedRevision}
}

func (repository *fakeRepository) count(name string) {
	if repository.listCalls == nil {
		repository.listCalls = make(map[string]int)
	}
	repository.listCalls[name]++
}

func validRepository() *fakeRepository {
	now := fixtureTime().Add(-time.Hour)
	meta := func(id string) domain.EntityMeta {
		return domain.EntityMeta{ID: id, SchemaVersion: 1, Revision: 1, CreatedAt: now, UpdatedAt: now}
	}
	definition := domain.TestCaseDefinition{
		SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:          casetypes.TypeRequestSingle,
		TypeVersion:   1,
		Spec:          validRequestSingleSpec(),
	}
	return &fakeRepository{
		models: []domain.Model{
			{EntityMeta: meta(modelAID), Name: "Zulu", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat", "tools"}},
			{EntityMeta: meta(modelBID), Name: "Alpha", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat"}},
		},
		channels: []domain.Channel{{EntityMeta: meta(channelID), Name: "Primary", BaseURL: "https://example.com/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true, CredentialID: credentialID}},
		mappings: []domain.ChannelModel{
			{EntityMeta: meta(mappingBID), ChannelID: channelID, ModelID: modelBID, UpstreamModelName: "alpha-upstream"},
			{EntityMeta: meta(mappingAID), ChannelID: channelID, ModelID: modelAID, UpstreamModelName: "zulu-upstream"},
		},
		testCases: []domain.TestCase{{
			EntityMeta: meta(caseID), Key: "T001", Name: "Chat", Dimension: "boundary",
			Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
			Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
			Definition: definition,
		}},
		suites: []domain.Suite{{
			EntityMeta: meta(suiteID), Key: "gpt-smoke", Name: "Smoke", Protocol: domain.ProtocolOpenAIChat,
			ModelTarget: "alpha-upstream", Cases: []domain.CaseRevisionRef{{CaseID: caseID, Revision: 1}},
		}},
		plans: []domain.Plan{{
			EntityMeta: meta(planID), Name: "Baseline", ModelIDs: []string{modelAID, modelBID}, ChannelIDs: []string{channelID},
			SuiteID: suiteID, SuiteRevision: 1, Cases: []domain.CaseRevisionRef{{CaseID: caseID, Revision: 1}},
			Load: domain.LoadProfile{Mode: domain.LoadFixedConcurrency, Concurrency: 2, RequestCount: 10, RequestTimeoutMS: 30_000},
			SLA:  domain.SLAProfile{Thresholds: map[string]float64{"p95_ms": 1500}},
		}},
		listCalls: make(map[string]int),
	}
}

func validCreateTestCaseCommand(name string) CreateTestCaseCommand {
	return CreateTestCaseCommand{
		Key: "T001", Name: name, Dimension: "boundary", Protocol: domain.ProtocolOpenAIChat,
		Enabled: true, Default: true, Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		DefinitionSchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
		Type:                    casetypes.TypeRequestSingle,
		TypeVersion:             1,
		Spec:                    validRequestSingleSpec(),
	}
}

func validRequestSingleSpec() json.RawMessage {
	return json.RawMessage(`{"request":{"method":"POST","path":"/v1/chat/completions","headers":{"X-Test":"safe"},"body":{"model":"gpt-5"}},"expected":{"allowed_http_statuses":[200],"stream_completion":"required"},"assertions":[{"kind":"text","config":{"contains":"ok"}}]}`)
}

func validUpdateTestCaseCommand(id, name string) UpdateTestCaseCommand {
	create := validCreateTestCaseCommand(name)
	return UpdateTestCaseCommand{
		ID: id, ExpectedRevision: 1, Key: create.Key, Name: create.Name, Dimension: create.Dimension,
		Protocol: create.Protocol, Enabled: create.Enabled, Default: create.Default, Severity: create.Severity, ExecutionMode: create.ExecutionMode,
		DefinitionSchemaVersion: create.DefinitionSchemaVersion,
		Type:                    create.Type, TypeVersion: create.TypeVersion, Spec: create.Spec,
	}
}

func validCreatePlanCommand(name string) CreatePlanCommand {
	return CreatePlanCommand{
		Name: name, ModelIDs: []string{modelAID}, ChannelIDs: []string{channelID}, SuiteID: suiteID, SuiteRevision: 1,
		Cases: []CaseRevisionInput{{CaseID: caseID, Revision: 1}}, LoadMode: domain.LoadFixedConcurrency, Concurrency: 2,
		RequestCount: 10, RequestTimeoutMS: 30_000, SLAThresholds: map[string]float64{"p95_ms": 1500},
	}
}

func validUpdatePlanCommand(id, name string) UpdatePlanCommand {
	create := validCreatePlanCommand(name)
	return UpdatePlanCommand{
		ID: id, ExpectedRevision: 1, Name: create.Name, ModelIDs: create.ModelIDs, ChannelIDs: create.ChannelIDs,
		SuiteID: create.SuiteID, SuiteRevision: create.SuiteRevision, Cases: create.Cases, LoadMode: create.LoadMode,
		Concurrency: create.Concurrency, RequestCount: create.RequestCount, RatePerSecond: create.RatePerSecond,
		DurationMS: create.DurationMS, RequestTimeoutMS: create.RequestTimeoutMS, SLAThresholds: create.SLAThresholds,
	}
}

func fixtureTime() time.Time { return time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC) }

func newTestService(t *testing.T, repository Repository, now time.Time) *Service {
	t.Helper()
	return newTestServiceWithFactory(t, repository, now, nil)
}

func newTestServiceWithFactory(t *testing.T, repository Repository, now time.Time, factory MetaFactory) *Service {
	t.Helper()
	service, err := New(Dependencies{Repository: repository, Clock: &stubClock{now: now}, MetaFactory: factory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

func sequentialMetaFactory(ids []string) MetaFactory {
	index := 0
	return func(now time.Time) (domain.EntityMeta, error) {
		if index >= len(ids) {
			return domain.EntityMeta{}, errors.New("factory exhausted")
		}
		meta := domain.EntityMeta{ID: ids[index], SchemaVersion: 1, Revision: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		index++
		return meta, nil
	}
}

func assertMutation(t *testing.T, result MutationResult, id string, err error) {
	t.Helper()
	if err != nil || result.ID != id || result.Revision != 1 {
		t.Fatalf("mutation = (%#v, %v), want id %s revision 1", result, err, id)
	}
}

func assertUpdate(t *testing.T, result MutationResult, id string, err error, meta domain.EntityMeta) {
	t.Helper()
	created := fixtureTime().Add(-time.Hour)
	if err != nil || result.ID != id || result.Revision != 2 {
		t.Fatalf("update = (%#v, %v), want id %s revision 2", result, err, id)
	}
	if !meta.CreatedAt.Equal(created) || meta.Revision != 2 || !meta.UpdatedAt.After(created) {
		t.Fatalf("updated metadata = %#v, want preserved creation and advanced revision/time", meta)
	}
}
