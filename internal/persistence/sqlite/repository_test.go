package sqlite_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

var repositoryEpoch = time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)

type coreRepositoryContract interface {
	CreateModel(context.Context, domain.Model) error
	GetModel(context.Context, string) (domain.Model, error)
	UpdateModel(context.Context, uint64, domain.Model) error
	DeleteModel(context.Context, string, uint64) error
	ListModels(context.Context) ([]domain.Model, error)
	ResolvePlanTarget(context.Context, domain.Plan) (domain.Model, domain.Channel, domain.ChannelModel, error)
	CreateCredentialRef(context.Context, domain.CredentialRef) error
	GetCredentialRef(context.Context, string) (domain.CredentialRef, error)
	UpdateCredentialRef(context.Context, uint64, domain.CredentialRef) error
	DeleteCredentialRef(context.Context, string, uint64) error
	ListCredentialRefs(context.Context) ([]domain.CredentialRef, error)
	CreateChannel(context.Context, domain.Channel) error
	GetChannel(context.Context, string) (domain.Channel, error)
	UpdateChannel(context.Context, uint64, domain.Channel) error
	DeleteChannel(context.Context, string, uint64) error
	ListChannels(context.Context) ([]domain.Channel, error)
	CreateChannelModel(context.Context, domain.ChannelModel) error
	GetChannelModel(context.Context, string) (domain.ChannelModel, error)
	UpdateChannelModel(context.Context, uint64, domain.ChannelModel) error
	DeleteChannelModel(context.Context, string, uint64) error
	ListChannelModels(context.Context) ([]domain.ChannelModel, error)
	CreateTestCase(context.Context, domain.TestCase) error
	GetTestCase(context.Context, string) (domain.TestCase, error)
	UpdateTestCase(context.Context, uint64, domain.TestCase) error
	DeleteTestCase(context.Context, string, uint64) error
	ListTestCases(context.Context) ([]domain.TestCase, error)
	CreateSuite(context.Context, domain.Suite) error
	GetSuite(context.Context, string) (domain.Suite, error)
	UpdateSuite(context.Context, uint64, domain.Suite) error
	DeleteSuite(context.Context, string, uint64) error
	ListSuites(context.Context) ([]domain.Suite, error)
	CreatePlan(context.Context, domain.Plan) error
	GetPlan(context.Context, string) (domain.Plan, error)
	UpdatePlan(context.Context, uint64, domain.Plan) error
	DeletePlan(context.Context, string, uint64) error
	ListPlans(context.Context) ([]domain.Plan, error)
	CreateRun(context.Context, domain.Run) error
	GetRun(context.Context, string) (domain.Run, error)
	GetRunRevision(context.Context, string, uint64) (domain.Run, error)
	UpdateRun(context.Context, uint64, domain.Run) error
	ListRuns(context.Context) ([]domain.Run, error)
	CreateEvidence(context.Context, domain.Evidence) error
	GetEvidence(context.Context, string) (domain.Evidence, error)
	ListEvidence(context.Context, string) ([]domain.Evidence, error)
	AppendResult(context.Context, domain.Result) error
	GetResult(context.Context, string) (domain.Result, error)
	ListResults(context.Context, string) ([]domain.Result, error)
	CreateReport(context.Context, domain.Report) error
	GetReport(context.Context, string) (domain.Report, error)
	ListReports(context.Context) ([]domain.Report, error)
	CreateComparison(context.Context, domain.Comparison) error
	GetComparison(context.Context, string) (domain.Comparison, error)
	UpdateComparison(context.Context, uint64, domain.Comparison) error
	ListComparisons(context.Context) ([]domain.Comparison, error)
	CreateIntegration(context.Context, domain.Integration) error
	GetIntegration(context.Context, string) (domain.Integration, error)
	UpdateIntegration(context.Context, uint64, domain.Integration) error
	ListIntegrations(context.Context) ([]domain.Integration, error)
}

func TestRepositoryDeletesOnlyAnUnreferencedCredentialRevisionSet(t *testing.T) {
	t.Parallel()
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	if err := repository.CreateCredentialRef(ctx, fixture.credential); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteCredentialRef(ctx, fixture.credential.ID, fixture.credential.Revision); err != nil {
		t.Fatalf("DeleteCredentialRef() error = %v", err)
	}
	if _, err := repository.GetCredentialRef(ctx, fixture.credential.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetCredentialRef(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryResolvesTheExactSingleTargetPinnedByAPlanRevision(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)
	creates := []struct {
		name   string
		create func() error
	}{
		{"credential", func() error { return repository.CreateCredentialRef(ctx, fixture.credential) }},
		{"model", func() error { return repository.CreateModel(ctx, fixture.model) }},
		{"channel", func() error { return repository.CreateChannel(ctx, fixture.channel) }},
		{"mapping", func() error { return repository.CreateChannelModel(ctx, fixture.mapping) }},
		{"case", func() error { return repository.CreateTestCase(ctx, fixture.testCase) }},
		{"suite", func() error { return repository.CreateSuite(ctx, fixture.suite) }},
		{"plan", func() error { return repository.CreatePlan(ctx, fixture.plan) }},
	}
	for _, item := range creates {
		if err := item.create(); err != nil {
			t.Fatalf("create %s: %v", item.name, err)
		}
	}

	model, channel, mapping, err := repository.ResolvePlanTarget(ctx, fixture.plan)
	if err != nil {
		t.Fatalf("ResolvePlanTarget() error = %v", err)
	}
	assertRoundTrip(t, "resolved model", fixture.model, model)
	assertRoundTrip(t, "resolved channel", fixture.channel, channel)
	assertRoundTrip(t, "resolved mapping", fixture.mapping, mapping)

	ambiguous := fixture.plan
	ambiguous.ModelIDs = append(ambiguous.ModelIDs, "20000000-0000-4000-8000-000000000099")
	if _, _, _, err := repository.ResolvePlanTarget(ctx, ambiguous); !errors.Is(err, persistence.ErrAmbiguousPlanTarget) {
		t.Fatalf("ResolvePlanTarget(ambiguous) error = %v, want ErrAmbiguousPlanTarget", err)
	}
}

func TestRepositoryResolvesCurrentCompatibleTargetForRuntimeTargetPlan(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)
	fixture.plan.Name = "Runtime target plan"
	fixture.plan.ModelIDs = nil
	fixture.plan.ChannelIDs = nil
	creates := []struct {
		name   string
		create func() error
	}{
		{"credential", func() error { return repository.CreateCredentialRef(ctx, fixture.credential) }},
		{"model", func() error { return repository.CreateModel(ctx, fixture.model) }},
		{"channel", func() error { return repository.CreateChannel(ctx, fixture.channel) }},
		{"mapping", func() error { return repository.CreateChannelModel(ctx, fixture.mapping) }},
		{"case", func() error { return repository.CreateTestCase(ctx, fixture.testCase) }},
		{"suite", func() error { return repository.CreateSuite(ctx, fixture.suite) }},
		{"plan", func() error { return repository.CreatePlan(ctx, fixture.plan) }},
	}
	for _, item := range creates {
		if err := item.create(); err != nil {
			t.Fatalf("create %s: %v", item.name, err)
		}
	}
	updatedModel := fixture.model
	updatedModel.Revision = 2
	updatedModel.UpdatedAt = updatedModel.UpdatedAt.Add(time.Second)
	updatedModel.Name = "Fixture model metadata update"
	if err := repository.UpdateModel(ctx, fixture.model.Revision, updatedModel); err != nil {
		t.Fatalf("UpdateModel() error = %v", err)
	}

	model, channel, mapping, err := repository.ResolvePlanTargetSelection(ctx, fixture.plan, fixture.model.ID, fixture.channel.ID)
	if err != nil {
		t.Fatalf("ResolvePlanTargetSelection() error = %v", err)
	}
	assertRoundTrip(t, "resolved model", updatedModel, model)
	assertRoundTrip(t, "resolved channel", fixture.channel, channel)
	assertRoundTrip(t, "resolved mapping", fixture.mapping, mapping)
	runSnapshot := fixture.run.Snapshot()
	runSnapshot.Model = domain.ModelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: model.Revision},
		Name:              model.Name,
		Protocol:          model.Protocol,
		Capabilities:      append([]string(nil), model.Capabilities...),
	}
	runtimeTargetRun, err := domain.NewRun(fixture.run.Meta(), fixture.plan.ID, runSnapshot)
	if err != nil {
		t.Fatalf("NewRun(runtime target) error = %v", err)
	}
	if err := repository.CreateRun(ctx, runtimeTargetRun); err != nil {
		t.Fatalf("CreateRun(targetless plan snapshot) error = %v", err)
	}
	assertRoundTrip(t, "targetless plan run", runtimeTargetRun, mustGetRun(t, repository, runtimeTargetRun.Meta().ID))
}

var _ coreRepositoryContract = (*persistence.Repository)(nil)

func TestRepositoryComparisonRoundTripAndTerminalRevision(t *testing.T) {
	t.Parallel()
	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)

	secondCredential := fixture.credential
	secondCredential.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000021", 1)
	secondCredential.StoreRef = "llm-test-studio/v1/channel_api_key/" + secondCredential.ID
	secondChannel := fixture.channel
	secondChannel.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000022", 1)
	secondChannel.Name = "Second channel"
	secondChannel.BaseURL = "https://second.example.test/v1"
	secondChannel.CredentialID = secondCredential.ID
	secondMapping := fixture.mapping
	secondMapping.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000023", 1)
	secondMapping.ChannelID = secondChannel.ID
	secondMapping.UpstreamModelName = "upstream-second"
	fixture.plan.ChannelIDs = append(fixture.plan.ChannelIDs, secondChannel.ID)

	creates := []func() error{
		func() error { return repository.CreateCredentialRef(ctx, fixture.credential) },
		func() error { return repository.CreateCredentialRef(ctx, secondCredential) },
		func() error { return repository.CreateModel(ctx, fixture.model) },
		func() error { return repository.CreateChannel(ctx, fixture.channel) },
		func() error { return repository.CreateChannel(ctx, secondChannel) },
		func() error { return repository.CreateChannelModel(ctx, fixture.mapping) },
		func() error { return repository.CreateChannelModel(ctx, secondMapping) },
		func() error { return repository.CreateTestCase(ctx, fixture.testCase) },
		func() error { return repository.CreateSuite(ctx, fixture.suite) },
		func() error { return repository.CreatePlan(ctx, fixture.plan) },
	}
	for index, create := range creates {
		if err := create(); err != nil {
			t.Fatalf("create comparison fixture %d: %v", index, err)
		}
	}

	firstSnapshot := fixture.run.Snapshot()
	firstSnapshot.Plan.Revision = fixture.plan.Revision
	firstRun, err := domain.NewRun(entityMeta(fixture.run.Meta().ID, 1), fixture.plan.ID, firstSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	secondSnapshot := firstSnapshot
	secondSnapshot.Channel = domain.ChannelSnapshot{
		EntityRevisionRef: domain.EntityRevisionRef{ID: secondChannel.ID, Revision: secondChannel.Revision},
		Name:              secondChannel.Name, BaseURL: secondChannel.BaseURL, Protocol: secondChannel.Protocol,
		UpstreamModelName: secondMapping.UpstreamModelName,
	}
	secondRun, err := domain.NewRun(entityMeta("10000000-0000-4000-8000-000000000024", 1), fixture.plan.ID, secondSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.Run{firstRun, secondRun} {
		if err := repository.CreateRun(ctx, run); err != nil {
			t.Fatalf("CreateRun() error = %v", err)
		}
	}
	comparison, err := domain.NewComparison(
		entityMeta("10000000-0000-4000-8000-000000000025", 1),
		domain.EntityRevisionRef{ID: fixture.plan.ID, Revision: fixture.plan.Revision},
		domain.EntityRevisionRef{ID: fixture.model.ID, Revision: fixture.model.Revision},
		[]domain.ComparisonRunRef{
			{Channel: firstSnapshot.Channel.EntityRevisionRef, RunID: firstRun.Meta().ID},
			{Channel: secondSnapshot.Channel.EntityRevisionRef, RunID: secondRun.Meta().ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateComparison(ctx, comparison); err != nil {
		t.Fatalf("CreateComparison() error = %v", err)
	}
	assertRoundTrip(t, "comparison", comparison, mustGetComparison(t, repository, comparison.Meta().ID))
	listed, err := repository.ListComparisons(ctx)
	if err != nil || len(listed) != 1 {
		t.Fatalf("ListComparisons() = %#v, %v", listed, err)
	}

	completed, err := comparison.Transition(domain.ComparisonCompleted, repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateComparison(ctx, comparison.Meta().Revision, completed); err != nil {
		t.Fatalf("UpdateComparison() error = %v", err)
	}
	assertRoundTrip(t, "completed comparison", completed, mustGetComparison(t, repository, comparison.Meta().ID))
	if err := repository.UpdateComparison(ctx, comparison.Meta().Revision, completed); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateComparison() error = %v, want ErrConflict", err)
	}
}

func TestRepositoryModelRoundTripAndOptimisticRevision(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()

	model := domain.Model{
		EntityMeta: entityMeta("10000000-0000-4000-8000-000000000001", 1),
		Name:       "Logical model",
		Protocol:   domain.ProtocolOpenAIChat,
		Capabilities: []string{
			"chat", "streaming",
		},
	}
	if err := repository.CreateModel(context.Background(), model); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	got, err := repository.GetModel(context.Background(), model.ID)
	if err != nil {
		t.Fatalf("GetModel() error = %v", err)
	}
	if !reflect.DeepEqual(got, model) {
		t.Fatalf("GetModel() = %#v, want %#v", got, model)
	}
	listed, err := repository.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels() error = %v", err)
	}
	if !reflect.DeepEqual(listed, []domain.Model{model}) {
		t.Fatalf("ListModels() = %#v, want model", listed)
	}

	nextMeta, err := model.EntityMeta.NextRevision(repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	updated := model
	updated.EntityMeta = nextMeta
	updated.Name = "Logical model v2"
	if err := repository.UpdateModel(context.Background(), 1, updated); err != nil {
		t.Fatalf("UpdateModel() error = %v", err)
	}
	if err := repository.UpdateModel(context.Background(), 1, updated); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateModel() error = %v, want ErrConflict", err)
	}
	got, err = repository.GetModel(context.Background(), model.ID)
	if err != nil {
		t.Fatalf("GetModel() after update error = %v", err)
	}
	if !reflect.DeepEqual(got, updated) {
		t.Fatalf("GetModel() after update = %#v, want %#v", got, updated)
	}
	if _, err := repository.GetModel(context.Background(), "20000000-0000-4000-8000-000000000099"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing GetModel() error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryHonorsCancelledContext(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repository.ListModels(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ListModels(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestRepositoryCatalogDeletesAreRevisionCheckedAndReferenceSafe(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)
	if err := repository.CreateCredentialRef(ctx, fixture.credential); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateModel(ctx, fixture.model); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateChannel(ctx, fixture.channel); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateChannelModel(ctx, fixture.mapping); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateTestCase(ctx, fixture.testCase); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreateSuite(ctx, fixture.suite); err != nil {
		t.Fatal(err)
	}
	if err := repository.CreatePlan(ctx, fixture.plan); err != nil {
		t.Fatal(err)
	}

	if err := repository.DeleteModel(ctx, fixture.model.ID, 1); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("DeleteModel(referenced) error = %v, want ErrConflict", err)
	}
	if _, err := repository.GetModel(ctx, fixture.model.ID); err != nil {
		t.Fatalf("GetModel() after rejected delete error = %v", err)
	}
	if err := repository.DeletePlan(ctx, fixture.plan.ID, 2); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("DeletePlan(stale) error = %v, want ErrConflict", err)
	}

	deletes := []struct {
		name   string
		id     string
		remove func(context.Context, string, uint64) error
		get    func(context.Context, string) error
	}{
		{name: "plan", id: fixture.plan.ID, remove: repository.DeletePlan, get: func(ctx context.Context, id string) error { _, err := repository.GetPlan(ctx, id); return err }},
		{name: "suite", id: fixture.suite.ID, remove: repository.DeleteSuite, get: func(ctx context.Context, id string) error { _, err := repository.GetSuite(ctx, id); return err }},
		{name: "test case", id: fixture.testCase.ID, remove: repository.DeleteTestCase, get: func(ctx context.Context, id string) error { _, err := repository.GetTestCase(ctx, id); return err }},
		{name: "channel model", id: fixture.mapping.ID, remove: repository.DeleteChannelModel, get: func(ctx context.Context, id string) error { _, err := repository.GetChannelModel(ctx, id); return err }},
		{name: "channel", id: fixture.channel.ID, remove: repository.DeleteChannel, get: func(ctx context.Context, id string) error { _, err := repository.GetChannel(ctx, id); return err }},
		{name: "model", id: fixture.model.ID, remove: repository.DeleteModel, get: func(ctx context.Context, id string) error { _, err := repository.GetModel(ctx, id); return err }},
	}
	for _, deletion := range deletes {
		if err := deletion.remove(ctx, deletion.id, 1); err != nil {
			t.Fatalf("Delete%s() error = %v", deletion.name, err)
		}
		if err := deletion.get(ctx, deletion.id); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("Get%s() after delete error = %v, want ErrNotFound", deletion.name, err)
		}
	}
}

func TestRepositoryCatalogDeletePreservesHistoryAndRetiresIdentity(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "catalog-tombstone.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	if err := repository.CreateModel(ctx, fixture.model); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteModel(ctx, fixture.model.ID, fixture.model.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetModel(ctx, fixture.model.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetModel() error = %v, want ErrNotFound", err)
	}
	if err := repository.CreateModel(ctx, fixture.model); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("CreateModel() with retired id error = %v, want ErrConflict", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatal(err)
	}

	db := openDatabase(t, path)
	defer db.Close()
	if got := queryInt(t, db, `SELECT COUNT(*) FROM models WHERE id = '`+fixture.model.ID+`'`); got != 1 {
		t.Fatalf("model history rows = %d, want 1", got)
	}
	if got := queryInt(t, db, `SELECT COUNT(*) FROM catalog_tombstones WHERE entity_table = 'models' AND entity_id = '`+fixture.model.ID+`' AND deleted_revision = 2`); got != 1 {
		t.Fatalf("model tombstone rows = %d, want 1", got)
	}
}

func TestRepositoryRejectsLiveReferencesToTombstonedCatalogEntities(t *testing.T) {
	t.Run("suite cannot use deleted case", func(t *testing.T) {
		repository := openRepository(t)
		defer repository.Close()
		fixture := newRepositoryFixture(t)
		ctx := context.Background()
		if err := repository.CreateTestCase(ctx, fixture.testCase); err != nil {
			t.Fatal(err)
		}
		if err := repository.DeleteTestCase(ctx, fixture.testCase.ID, fixture.testCase.Revision); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreateSuite(ctx, fixture.suite); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("CreateSuite() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("plan cannot use deleted suite", func(t *testing.T) {
		repository := openRepository(t)
		defer repository.Close()
		fixture := newRepositoryFixture(t)
		ctx := context.Background()
		for _, create := range []func(context.Context) error{
			func(ctx context.Context) error { return repository.CreateCredentialRef(ctx, fixture.credential) },
			func(ctx context.Context) error { return repository.CreateModel(ctx, fixture.model) },
			func(ctx context.Context) error { return repository.CreateChannel(ctx, fixture.channel) },
			func(ctx context.Context) error { return repository.CreateChannelModel(ctx, fixture.mapping) },
			func(ctx context.Context) error { return repository.CreateTestCase(ctx, fixture.testCase) },
			func(ctx context.Context) error { return repository.CreateSuite(ctx, fixture.suite) },
		} {
			if err := create(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := repository.DeleteSuite(ctx, fixture.suite.ID, fixture.suite.Revision); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreatePlan(ctx, fixture.plan); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("CreatePlan() error = %v, want ErrNotFound", err)
		}
	})

	t.Run("plan cannot use deleted mapping", func(t *testing.T) {
		repository := openRepository(t)
		defer repository.Close()
		fixture := newRepositoryFixture(t)
		ctx := context.Background()
		for _, create := range []func(context.Context) error{
			func(ctx context.Context) error { return repository.CreateCredentialRef(ctx, fixture.credential) },
			func(ctx context.Context) error { return repository.CreateModel(ctx, fixture.model) },
			func(ctx context.Context) error { return repository.CreateChannel(ctx, fixture.channel) },
			func(ctx context.Context) error { return repository.CreateChannelModel(ctx, fixture.mapping) },
			func(ctx context.Context) error { return repository.CreateTestCase(ctx, fixture.testCase) },
			func(ctx context.Context) error { return repository.CreateSuite(ctx, fixture.suite) },
		} {
			if err := create(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if err := repository.DeleteChannelModel(ctx, fixture.mapping.ID, fixture.mapping.Revision); err != nil {
			t.Fatal(err)
		}
		if err := repository.CreatePlan(ctx, fixture.plan); !errors.Is(err, persistence.ErrNotFound) {
			t.Fatalf("CreatePlan() error = %v, want ErrNotFound", err)
		}
	})
}

func TestRepositoryRejectsDeletingPlanOwnedByPersistedRun(t *testing.T) {
	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	for _, create := range []func(context.Context) error{
		func(ctx context.Context) error { return repository.CreateCredentialRef(ctx, fixture.credential) },
		func(ctx context.Context) error { return repository.CreateModel(ctx, fixture.model) },
		func(ctx context.Context) error { return repository.CreateChannel(ctx, fixture.channel) },
		func(ctx context.Context) error { return repository.CreateChannelModel(ctx, fixture.mapping) },
		func(ctx context.Context) error { return repository.CreateTestCase(ctx, fixture.testCase) },
		func(ctx context.Context) error { return repository.CreateSuite(ctx, fixture.suite) },
		func(ctx context.Context) error { return repository.CreatePlan(ctx, fixture.plan) },
		func(ctx context.Context) error { return repository.CreateRun(ctx, fixture.run) },
	} {
		if err := create(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.DeletePlan(ctx, fixture.plan.ID, fixture.plan.Revision); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("DeletePlan() error = %v, want ErrConflict", err)
	}
}

func TestRepositoryPersistsValidatedDomainGraphAndReport(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	ctx := context.Background()
	fixture := newRepositoryFixture(t)

	if err := repository.CreateCredentialRef(ctx, fixture.credential); err != nil {
		t.Fatalf("CreateCredentialRef() error = %v", err)
	}
	if err := repository.CreateCredentialRef(ctx, fixture.adminCredential); err != nil {
		t.Fatalf("CreateCredentialRef(admin) error = %v", err)
	}
	if err := repository.CreateModel(ctx, fixture.model); err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	if err := repository.CreateChannel(ctx, fixture.channel); err != nil {
		t.Fatalf("CreateChannel() error = %v", err)
	}
	if err := repository.CreateChannelModel(ctx, fixture.mapping); err != nil {
		t.Fatalf("CreateChannelModel() error = %v", err)
	}
	duplicateMapping := fixture.mapping
	duplicateMapping.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000021", 1)
	if err := repository.CreateChannelModel(ctx, duplicateMapping); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("duplicate CreateChannelModel() error = %v, want ErrConflict", err)
	}
	if err := repository.CreateTestCase(ctx, fixture.testCase); err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	if err := repository.CreateSuite(ctx, fixture.suite); err != nil {
		t.Fatalf("CreateSuite() error = %v", err)
	}
	if err := repository.CreatePlan(ctx, fixture.plan); err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if err := repository.CreateRun(ctx, fixture.run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	run := fixture.run
	for _, status := range []domain.RunStatus{domain.RunStarting, domain.RunRunning} {
		previousRevision := run.Meta().Revision
		var err error
		run, err = run.Transition(status, repositoryEpoch.Add(time.Duration(previousRevision)*time.Minute))
		if err != nil {
			t.Fatalf("Transition(%s) error = %v", status, err)
		}
		if err := repository.UpdateRun(ctx, previousRevision, run); err != nil {
			t.Fatalf("UpdateRun(%s) error = %v", status, err)
		}
	}
	if err := repository.CreateEvidence(ctx, fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence() error = %v", err)
	}
	if err := repository.AppendResult(ctx, fixture.result); err != nil {
		t.Fatalf("AppendResult() error = %v", err)
	}
	wrongPurposeIntegration := fixture.integration
	wrongPurposeIntegration.EntityMeta = entityMeta("10000000-0000-4000-8000-000000000022", 1)
	wrongPurposeIntegration.CredentialID = fixture.credential.ID
	if err := repository.CreateIntegration(ctx, wrongPurposeIntegration); err == nil {
		t.Fatal("CreateIntegration() accepted a channel API key credential")
	}
	if err := repository.CreateIntegration(ctx, fixture.integration); err != nil {
		t.Fatalf("CreateIntegration() error = %v", err)
	}

	for _, status := range []domain.RunStatus{domain.RunCompleted} {
		previousRevision := run.Meta().Revision
		var err error
		run, err = run.Transition(status, repositoryEpoch.Add(time.Duration(previousRevision)*time.Minute))
		if err != nil {
			t.Fatalf("Transition(%s) error = %v", status, err)
		}
		if err := repository.UpdateRun(ctx, previousRevision, run); err != nil {
			t.Fatalf("UpdateRun(%s) error = %v", status, err)
		}
	}
	report := fixture.report
	report.RunStatus = domain.RunCompleted
	if err := repository.CreateReport(ctx, report); err != nil {
		t.Fatalf("CreateReport() error = %v", err)
	}

	assertRoundTrip(t, "credential", fixture.credential, mustGetCredential(t, repository, fixture.credential.ID))
	assertRoundTrip(t, "admin credential", fixture.adminCredential, mustGetCredential(t, repository, fixture.adminCredential.ID))
	assertRoundTrip(t, "channel", fixture.channel, mustGetChannel(t, repository, fixture.channel.ID))
	assertRoundTrip(t, "mapping", fixture.mapping, mustGetChannelModel(t, repository, fixture.mapping.ID))
	assertRoundTrip(t, "test case", fixture.testCase, mustGetTestCase(t, repository, fixture.testCase.ID))
	assertRoundTrip(t, "suite", fixture.suite, mustGetSuite(t, repository, fixture.suite.ID))
	assertRoundTrip(t, "plan", fixture.plan, mustGetPlan(t, repository, fixture.plan.ID))
	assertRoundTrip(t, "run", run, mustGetRun(t, repository, run.Meta().ID))
	assertRoundTrip(t, "evidence", fixture.evidence, mustGetEvidence(t, repository, fixture.evidence.ID))
	assertRoundTrip(t, "result", fixture.result, mustGetResult(t, repository, fixture.result.ID))
	assertRoundTrip(t, "integration", fixture.integration, mustGetIntegration(t, repository, fixture.integration.ID))
	assertRoundTrip(t, "report", report, mustGetReport(t, repository, report.ID))

	results, err := repository.ListResults(ctx, fixture.run.Meta().ID)
	if err != nil || !reflect.DeepEqual(results, []domain.Result{fixture.result}) {
		t.Fatalf("ListResults() = %#v, %v", results, err)
	}
}

func TestRepositoryRelationFailureRollsBackAggregate(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	suite := domain.Suite{
		EntityMeta: entityMeta("10000000-0000-4000-8000-000000000020", 1),
		Name:       "Missing case suite",
		Cases: []domain.CaseRevisionRef{{
			CaseID: "10000000-0000-4000-8000-000000000099", Revision: 1,
		}},
	}
	err := repository.CreateSuite(context.Background(), suite)
	if !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("CreateSuite() error = %v, want ErrNotFound", err)
	}
	if _, err := repository.GetSuite(context.Background(), suite.ID); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetSuite() after rollback error = %v, want ErrNotFound", err)
	}
}

func TestRepositoryStoresCredentialMetadataWithoutSecretBytes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "secret-boundary.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	secret := "sk-never-store-this-sentinel-9f681b"
	credential := newRepositoryFixture(t).credential
	credential.StoreRef = secret
	fingerprint := sha256.Sum256([]byte(secret))
	credential.Fingerprint = "sha256:" + hex.EncodeToString(fingerprint[:])
	credential.MaskedSuffix = "681b"
	if err := repository.CreateCredentialRef(context.Background(), credential); err == nil {
		repository.Close()
		t.Fatal("CreateCredentialRef() accepted plaintext secret as StoreRef")
	} else if strings.Contains(err.Error(), secret) {
		repository.Close()
		t.Fatal("CreateCredentialRef() leaked plaintext credential material in its error")
	}
	credential.StoreRef = "llm-test-studio/v1/channel_api_key/" + credential.ID
	if err := repository.CreateCredentialRef(context.Background(), credential); err != nil {
		repository.Close()
		t.Fatalf("CreateCredentialRef(valid metadata) error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read database bytes: %v", err)
	}
	if strings.Contains(string(bytes), secret) {
		t.Fatal("SQLite database contains plaintext credential material")
	}
}

func TestRepositoryRetainsPinnedTestCaseRevision(t *testing.T) {
	t.Parallel()

	repository := openRepository(t)
	defer repository.Close()
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	if err := repository.CreateTestCase(ctx, fixture.testCase); err != nil {
		t.Fatalf("CreateTestCase() error = %v", err)
	}
	nextMeta, err := fixture.testCase.EntityMeta.NextRevision(repositoryEpoch.Add(time.Minute))
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	updated := fixture.testCase
	updated.EntityMeta = nextMeta
	updated.Name = "Basic chat revised"
	if err := repository.UpdateTestCase(ctx, 1, updated); err != nil {
		t.Fatalf("UpdateTestCase() error = %v", err)
	}
	if err := repository.UpdateTestCase(ctx, 1, updated); !errors.Is(err, persistence.ErrConflict) {
		t.Fatalf("stale UpdateTestCase() error = %v, want ErrConflict", err)
	}
	if err := repository.CreateSuite(ctx, fixture.suite); err != nil {
		t.Fatalf("CreateSuite() pinned to revision 1 error = %v", err)
	}
	pinned, err := repository.GetTestCaseRevision(ctx, fixture.testCase.ID, 1)
	if err != nil {
		t.Fatalf("GetTestCaseRevision(1) error = %v", err)
	}
	if !reflect.DeepEqual(pinned, fixture.testCase) {
		t.Fatalf("pinned revision = %#v, want original %#v", pinned, fixture.testCase)
	}
	latest, err := repository.GetTestCase(ctx, fixture.testCase.ID)
	if err != nil {
		t.Fatalf("GetTestCase() error = %v", err)
	}
	if !reflect.DeepEqual(latest, updated) {
		t.Fatalf("latest revision = %#v, want %#v", latest, updated)
	}
}

func TestRepositoryRejectsCorruptDomainDocument(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt-document.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	model := newRepositoryFixture(t).model
	if err := repository.CreateModel(context.Background(), model); err != nil {
		repository.Close()
		t.Fatalf("CreateModel() error = %v", err)
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec(`UPDATE models SET document_json = '{}' WHERE id = ?`, model.ID); err != nil {
		db.Close()
		t.Fatalf("tamper model document: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tampered database: %v", err)
	}
	repository, err = persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() after row tamper error = %v", err)
	}
	defer repository.Close()
	if _, err := repository.GetModel(context.Background(), model.ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetModel() corrupt error = %v, want ErrCorrupt", err)
	}
}

func TestRepositoryRejectsCorruptRunSnapshotDocument(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "corrupt-snapshot.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	fixture := newRepositoryFixture(t)
	ctx := context.Background()
	for _, step := range []struct {
		name   string
		create func() error
	}{
		{name: "credential", create: func() error { return repository.CreateCredentialRef(ctx, fixture.credential) }},
		{name: "model", create: func() error { return repository.CreateModel(ctx, fixture.model) }},
		{name: "channel", create: func() error { return repository.CreateChannel(ctx, fixture.channel) }},
		{name: "mapping", create: func() error { return repository.CreateChannelModel(ctx, fixture.mapping) }},
		{name: "case", create: func() error { return repository.CreateTestCase(ctx, fixture.testCase) }},
		{name: "suite", create: func() error { return repository.CreateSuite(ctx, fixture.suite) }},
		{name: "plan", create: func() error { return repository.CreatePlan(ctx, fixture.plan) }},
		{name: "run", create: func() error { return repository.CreateRun(ctx, fixture.run) }},
	} {
		if err := step.create(); err != nil {
			repository.Close()
			t.Fatalf("create %s: %v", step.name, err)
		}
	}
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	db := openDatabase(t, path)
	if _, err := db.Exec(`DROP TRIGGER trg_execution_run_revisions_no_update`); err != nil {
		db.Close()
		t.Fatalf("drop run immutability trigger for tamper: %v", err)
	}
	if _, err := db.Exec(`UPDATE execution_run_revisions SET snapshot_json = '{}' WHERE run_id = ?`, fixture.run.Meta().ID); err != nil {
		db.Close()
		t.Fatalf("tamper run snapshot: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TRIGGER trg_execution_run_revisions_no_update
		BEFORE UPDATE ON execution_run_revisions
		BEGIN
			SELECT RAISE(ABORT, 'execution run revisions are immutable');
		END
	`); err != nil {
		db.Close()
		t.Fatalf("restore run immutability trigger: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close tampered database: %v", err)
	}
	repository, err = persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() after row tamper error = %v", err)
	}
	defer repository.Close()
	if _, err := repository.GetRun(context.Background(), fixture.run.Meta().ID); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("GetRun() corrupt snapshot error = %v, want ErrCorrupt", err)
	}
}

type repositoryFixture struct {
	model           domain.Model
	credential      domain.CredentialRef
	adminCredential domain.CredentialRef
	channel         domain.Channel
	mapping         domain.ChannelModel
	testCase        domain.TestCase
	suite           domain.Suite
	plan            domain.Plan
	run             domain.Run
	evidence        domain.Evidence
	result          domain.Result
	integration     domain.Integration
	report          domain.Report
}

func newRepositoryFixture(t *testing.T) repositoryFixture {
	t.Helper()
	const (
		modelID       = "10000000-0000-4000-8000-000000000001"
		credentialID  = "10000000-0000-4000-8000-000000000002"
		channelID     = "10000000-0000-4000-8000-000000000003"
		mappingID     = "10000000-0000-4000-8000-000000000004"
		caseID        = "10000000-0000-4000-8000-000000000005"
		suiteID       = "10000000-0000-4000-8000-000000000006"
		planID        = "10000000-0000-4000-8000-000000000007"
		runID         = "10000000-0000-4000-8000-000000000008"
		evidenceID    = "10000000-0000-4000-8000-000000000009"
		resultID      = "10000000-0000-4000-8000-00000000000a"
		reportID      = "10000000-0000-4000-8000-00000000000b"
		artifactID    = "10000000-0000-4000-8000-00000000000c"
		integrationID = "10000000-0000-4000-8000-00000000000d"
		adminCredID   = "10000000-0000-4000-8000-00000000000e"
	)
	model := domain.Model{EntityMeta: entityMeta(modelID, 1), Name: "Fixture model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat", "streaming"}}
	digest := sha256.Sum256([]byte("credential fingerprint only"))
	credential := domain.CredentialRef{
		EntityMeta: entityMeta(credentialID, 1), StoreRef: "llm-test-studio/v1/channel_api_key/" + credentialID,
		Purpose: domain.CredentialChannelAPIKey, MaskedSuffix: "9Ab2", Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
	}
	adminCredential := credential
	adminCredential.EntityMeta = entityMeta(adminCredID, 1)
	adminCredential.StoreRef = "llm-test-studio/v1/integration_admin/" + adminCredID
	adminCredential.Purpose = domain.CredentialIntegrationAdmin
	channel := domain.Channel{EntityMeta: entityMeta(channelID, 1), Name: "Fixture channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true, CredentialID: credentialID}
	mapping := domain.ChannelModel{EntityMeta: entityMeta(mappingID, 1), ChannelID: channelID, ModelID: modelID, UpstreamModelName: "upstream-fixture"}
	testCase := domain.TestCase{
		EntityMeta: entityMeta(caseID, 1), Key: "T001", Name: "Basic chat", Dimension: "boundary",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Request:       domain.TestRequest{Method: domain.RequestPOST, Path: "/chat/completions", Headers: map[string]string{"Content-Type": "application/json"}, Body: json.RawMessage(`{"messages":[{"content":"hello","role":"user"}]}`)},
			Expected:      domain.TestExpected{AllowedHTTPStatuses: []int{200}, StreamCompletion: domain.StreamCompletionNotApplicable},
			Assertions:    []domain.TestAssertion{{Kind: domain.AssertionText, Config: json.RawMessage(`{"contains":"ok"}`)}},
		},
	}
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	suite := domain.Suite{EntityMeta: entityMeta(suiteID, 1), Name: "Fixture suite", Cases: []domain.CaseRevisionRef{caseRef}}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 5000}}
	plan := domain.Plan{EntityMeta: entityMeta(planID, 1), Name: "Fixture plan", ModelIDs: []string{modelID}, ChannelIDs: []string{channelID}, SuiteID: suiteID, SuiteRevision: 1, Cases: []domain.CaseRevisionRef{caseRef}, Load: load, SLA: sla}
	environment := domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "go-test"}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: planID, Revision: 1},
		Model:         domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1}, Name: model.Name, Protocol: model.Protocol, Capabilities: append([]string(nil), model.Capabilities...)},
		Channel:       domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1}, Name: channel.Name, BaseURL: channel.BaseURL, Protocol: channel.Protocol, UpstreamModelName: mapping.UpstreamModelName},
		Cases:         []domain.CaseRevisionRef{caseRef}, Load: load, SLA: sla, Environment: environment,
	}
	run, err := domain.NewRun(entityMeta(runID, 1), planID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	evidence := domain.Evidence{EntityMeta: entityMeta(evidenceID, 1), RunID: runID, RelativePath: "evidence/response.json", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Redacted: true}
	result := domain.Result{EntityMeta: entityMeta(resultID, 1), RunID: runID, CaseID: caseID, Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}, Metrics: map[string]float64{"e2e_ms": 123}, EvidenceIDs: []string{evidenceID}}
	integration := domain.Integration{EntityMeta: entityMeta(integrationID, 1), Kind: domain.IntegrationNewAPI, Name: "Fixture new-api", CredentialID: adminCredID, Config: domain.IntegrationConfig{BaseURL: "https://admin.example.test", Region: "local", ExternalID: "fixture"}}
	report := domain.Report{
		SchemaVersion: domain.CurrentReportSchemaVersion, ID: reportID, RunID: runID, RunStatus: domain.RunCompleted,
		GeneratedAt: repositoryEpoch.Add(10 * time.Minute), PlanSnapshot: snapshot,
		Model: domain.ReportSubject{ID: modelID, Name: model.Name}, Channel: domain.ReportSubject{ID: channelID, Name: channel.Name}, Environment: environment,
		Conclusion: domain.ReportConclusion{Passed: true, Verdict: "pass", Issues: []string{}}, SLA: map[string]domain.MetricValue{}, Metrics: map[string]domain.MetricValue{},
		Timeline: []json.RawMessage{}, Distributions: []json.RawMessage{}, CaseResults: []domain.Result{result}, ErrorClusters: []json.RawMessage{}, Evidence: []domain.Evidence{evidence}, Baseline: json.RawMessage(`{}`),
		Attachments: []domain.ReportAttachment{{ArtifactID: artifactID, RunID: runID, Name: "HTML report", RelativePath: "reports/report.html", SHA256: strings.Repeat("b", 64), MediaType: "text/html", Redacted: true}},
	}
	for name, value := range map[string]interface{ Validate() error }{
		"model": model, "credential": credential, "admin credential": adminCredential, "channel": channel, "mapping": mapping,
		"test case": testCase, "suite": suite, "plan": plan, "run": run,
		"result": result, "integration": integration, "report": report,
	} {
		if err := value.Validate(); err != nil {
			t.Fatalf("invalid %s fixture: %v", name, err)
		}
	}
	return repositoryFixture{model: model, credential: credential, adminCredential: adminCredential, channel: channel, mapping: mapping, testCase: testCase, suite: suite, plan: plan, run: run, evidence: evidence, result: result, integration: integration, report: report}
}

func assertRoundTrip(t *testing.T, name string, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s roundtrip = %#v, want %#v", name, got, want)
	}
}

func mustGetCredential(t *testing.T, repository *persistence.Repository, id string) domain.CredentialRef {
	t.Helper()
	value, err := repository.GetCredentialRef(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetChannel(t *testing.T, repository *persistence.Repository, id string) domain.Channel {
	t.Helper()
	value, err := repository.GetChannel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetChannelModel(t *testing.T, repository *persistence.Repository, id string) domain.ChannelModel {
	t.Helper()
	value, err := repository.GetChannelModel(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetTestCase(t *testing.T, repository *persistence.Repository, id string) domain.TestCase {
	t.Helper()
	value, err := repository.GetTestCase(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetSuite(t *testing.T, repository *persistence.Repository, id string) domain.Suite {
	t.Helper()
	value, err := repository.GetSuite(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetPlan(t *testing.T, repository *persistence.Repository, id string) domain.Plan {
	t.Helper()
	value, err := repository.GetPlan(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetRun(t *testing.T, repository *persistence.Repository, id string) domain.Run {
	t.Helper()
	value, err := repository.GetRun(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetEvidence(t *testing.T, repository *persistence.Repository, id string) domain.Evidence {
	t.Helper()
	value, err := repository.GetEvidence(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetResult(t *testing.T, repository *persistence.Repository, id string) domain.Result {
	t.Helper()
	value, err := repository.GetResult(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetIntegration(t *testing.T, repository *persistence.Repository, id string) domain.Integration {
	t.Helper()
	value, err := repository.GetIntegration(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetReport(t *testing.T, repository *persistence.Repository, id string) domain.Report {
	t.Helper()
	value, err := repository.GetReport(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustGetComparison(t *testing.T, repository *persistence.Repository, id string) domain.Comparison {
	t.Helper()
	value, err := repository.GetComparison(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func openRepository(t *testing.T) *persistence.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	return repository
}

func entityMeta(id string, revision uint64) domain.EntityMeta {
	return domain.EntityMeta{
		ID:            id,
		SchemaVersion: domain.CurrentEntitySchemaVersion,
		Revision:      revision,
		CreatedAt:     repositoryEpoch,
		UpdatedAt:     repositoryEpoch,
	}
}
