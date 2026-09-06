package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/domain"
	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

var repositoryEpoch = time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)

type repositoryFixture struct {
	model    domain.Model
	channel  domain.Channel
	mapping  domain.ChannelModel
	testCase domain.TestCase
	suite    domain.Suite
	plan     domain.Plan
	run      domain.Run
	evidence domain.Evidence
	result   domain.Result
	report   domain.Report
}

func newRepositoryFixture(t *testing.T) repositoryFixture {
	t.Helper()
	const (
		modelID      = "10000000-0000-4000-8000-000000000001"
		credentialID = "10000000-0000-4000-8000-000000000002"
		channelID    = "10000000-0000-4000-8000-000000000003"
		mappingID    = "10000000-0000-4000-8000-000000000004"
		caseID       = "10000000-0000-4000-8000-000000000005"
		suiteID      = "10000000-0000-4000-8000-000000000006"
		planID       = "10000000-0000-4000-8000-000000000007"
		runID        = "10000000-0000-4000-8000-000000000008"
		evidenceID   = "10000000-0000-4000-8000-000000000009"
		resultID     = "10000000-0000-4000-8000-00000000000a"
		reportID     = "10000000-0000-4000-8000-00000000000b"
		artifactID   = "10000000-0000-4000-8000-00000000000c"
	)
	model := domain.Model{EntityMeta: entityMeta(modelID, 1), Name: "Fixture model", Protocol: domain.ProtocolOpenAIChat, Capabilities: []string{"chat", "streaming"}}
	channel := domain.Channel{EntityMeta: entityMeta(channelID, 1), Name: "Fixture channel", BaseURL: "https://example.test/v1", Protocol: domain.ProtocolOpenAIChat, Enabled: true, CredentialID: credentialID}
	mapping := domain.ChannelModel{EntityMeta: entityMeta(mappingID, 1), ChannelID: channelID, ModelID: modelID, UpstreamModelName: "upstream-fixture"}
	testCaseSpec := json.RawMessage(`{"assertions":[{"config":{"contains":"ok"},"kind":"text"}],"expected":{"allowed_http_statuses":[200],"stream_completion":"not_applicable"},"request":{"body":{"messages":[{"content":"hello","role":"user"}]},"headers":{"Content-Type":"application/json"},"method":"POST","path":"/chat/completions"}}`)
	testCase := domain.TestCase{
		EntityMeta: entityMeta(caseID, 1), Key: "T001", Name: "Basic chat", Dimension: "boundary",
		Protocol: domain.ProtocolOpenAIChat, Enabled: true, Default: true,
		Severity: domain.CaseSeverityCritical, ExecutionMode: domain.CaseExecutionAutomatic,
		Definition: domain.TestCaseDefinition{
			SchemaVersion: domain.CurrentTestCaseDefinitionSchemaVersion,
			Type:          domain.CaseType("request.single"),
			TypeVersion:   1,
			Spec:          testCaseSpec,
		},
	}
	caseRef := domain.CaseRevisionRef{CaseID: caseID, Revision: 1}
	suite := domain.Suite{
		EntityMeta: entityMeta(suiteID, 1), Key: "fixture-suite", Name: "Fixture suite",
		Protocol: domain.ProtocolOpenAIChat, ModelTarget: "upstream-fixture", Cases: []domain.CaseRevisionRef{caseRef},
	}
	load := domain.LoadProfile{Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 30_000}
	sla := domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 5000}}
	plan := domain.Plan{EntityMeta: entityMeta(planID, 1), Name: "Fixture plan", ModelIDs: []string{modelID}, ChannelIDs: []string{channelID}, SuiteID: suiteID, SuiteRevision: 1, Cases: []domain.CaseRevisionRef{caseRef}, Load: load, SLA: sla}
	environment := domain.EnvironmentSnapshot{OS: "windows", Arch: "amd64", Region: "local", NetworkEgress: "direct", AppVersion: "test", EngineVersion: "go-test"}
	snapshot := domain.RunSnapshot{
		SchemaVersion:   domain.CurrentRunSnapshotSchemaVersion,
		Plan:            domain.EntityRevisionRef{ID: planID, Revision: 1},
		Model:           domain.ModelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: modelID, Revision: 1}, Name: model.Name, Protocol: model.Protocol, Capabilities: append([]string(nil), model.Capabilities...)},
		Channel:         domain.ChannelSnapshot{EntityRevisionRef: domain.EntityRevisionRef{ID: channelID, Revision: 1}, Name: channel.Name, BaseURL: channel.BaseURL, Protocol: channel.Protocol, UpstreamModelName: mapping.UpstreamModelName},
		Cases:           []domain.CaseRevisionRef{caseRef},
		Load:            load,
		SLA:             sla,
		Environment:     environment,
		PlanDocument:    &plan,
		Mapping:         &mapping,
		CaseDefinitions: []domain.TestCase{testCase},
	}
	run, err := domain.NewRun(entityMeta(runID, 1), planID, snapshot)
	if err != nil {
		t.Fatalf("NewRun() error = %v", err)
	}
	evidence := domain.Evidence{EntityMeta: entityMeta(evidenceID, 1), RunID: runID, RelativePath: "evidence/response.json", SHA256: strings.Repeat("a", 64), MediaType: "application/json", Redacted: true}
	result := domain.Result{EntityMeta: entityMeta(resultID, 1), RunID: runID, CaseID: caseID, Success: domain.SuccessDimensions{Transport: true, Protocol: true, Semantic: true, SLA: true}, Metrics: map[string]float64{"e2e_ms": 123}, EvidenceIDs: []string{evidenceID}}
	report := domain.Report{
		SchemaVersion: domain.CurrentReportSchemaVersion, ID: reportID, RunID: runID, RunStatus: domain.RunCompleted,
		GeneratedAt: repositoryEpoch.Add(10 * time.Minute), PlanSnapshot: snapshot,
		Model: domain.ReportSubject{ID: modelID, Name: model.Name}, Channel: domain.ReportSubject{ID: channelID, Name: channel.Name}, Environment: environment,
		Conclusion: domain.ReportConclusion{Passed: true, Verdict: "pass", Issues: []string{}}, SLA: map[string]domain.MetricValue{}, Metrics: map[string]domain.MetricValue{},
		Timeline: []json.RawMessage{}, Distributions: []json.RawMessage{}, CaseResults: []domain.Result{result}, ErrorClusters: []json.RawMessage{}, Evidence: []domain.Evidence{evidence}, Baseline: json.RawMessage(`{}`),
		Attachments: []domain.ReportAttachment{{ArtifactID: artifactID, RunID: runID, Name: "HTML report", RelativePath: "reports/report.html", SHA256: strings.Repeat("b", 64), MediaType: "text/html", Redacted: true}},
	}
	for name, value := range map[string]interface{ Validate() error }{
		"model": model, "channel": channel, "mapping": mapping,
		"test case": testCase, "suite": suite, "plan": plan, "run": run,
		"result": result, "report": report,
	} {
		if err := value.Validate(); err != nil {
			t.Fatalf("invalid %s fixture: %v", name, err)
		}
	}
	return repositoryFixture{model: model, channel: channel, mapping: mapping, testCase: testCase, suite: suite, plan: plan, run: run, evidence: evidence, result: result, report: report}
}

func assertRoundTrip(t *testing.T, name string, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s roundtrip = %#v, want %#v", name, got, want)
	}
}

func openRepository(t *testing.T) *persistence.Repository {
	t.Helper()
	return openOperationalRepository(t)
}

func openOperationalRepository(t *testing.T) *persistence.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operational-repository.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return reopenHardeningRepository(t, path)
}

func retireAndReopenOperationalRepository(t *testing.T, path string) *persistence.Repository {
	t.Helper()
	return reopenHardeningRepository(t, path)
}

func entityMeta(id string, revision uint64) domain.EntityMeta {
	return domain.EntityMeta{
		ID: id, SchemaVersion: domain.CurrentEntitySchemaVersion, Revision: revision,
		CreatedAt: repositoryEpoch, UpdatedAt: repositoryEpoch,
	}
}

func createRunGraph(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) {
	t.Helper()
	if err := repository.CreateRun(context.Background(), fixture.run); err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
}

func createRunningOutputs(t *testing.T, repository *persistence.Repository, fixture repositoryFixture) domain.Run {
	t.Helper()
	createRunGraph(t, repository, fixture)
	run := transitionRun(t, repository, fixture.run, domain.RunStarting, domain.RunRunning)
	if err := repository.CreateEvidence(context.Background(), fixture.evidence); err != nil {
		t.Fatalf("CreateEvidence() error = %v", err)
	}
	if err := repository.AppendResult(context.Background(), fixture.result); err != nil {
		t.Fatalf("AppendResult() error = %v", err)
	}
	return run
}

func transitionRun(t *testing.T, repository *persistence.Repository, run domain.Run, statuses ...domain.RunStatus) domain.Run {
	t.Helper()
	for _, status := range statuses {
		previous := run.Meta().Revision
		var err error
		run, err = run.Transition(status, repositoryEpoch.Add(time.Duration(previous)*time.Minute))
		if err != nil {
			t.Fatalf("Transition(%s): %v", status, err)
		}
		if err := repository.UpdateRun(context.Background(), previous, run); err != nil {
			t.Fatalf("UpdateRun(%s): %v", status, err)
		}
	}
	return run
}

func nextEntityMeta(t *testing.T, meta domain.EntityMeta, at time.Time) domain.EntityMeta {
	t.Helper()
	next, err := meta.NextRevision(at)
	if err != nil {
		t.Fatalf("NextRevision() error = %v", err)
	}
	return next
}

func openHardeningRepository(t *testing.T) (string, *persistence.Repository, repositoryFixture) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "repository-hardening.db")
	if err := persistence.Migrate(context.Background(), path, persistence.MigrateOptions{AppVersion: "repository-test"}); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return path, reopenHardeningRepository(t, path), newRepositoryFixture(t)
}

func reopenHardeningRepository(t *testing.T, path string) *persistence.Repository {
	t.Helper()
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		t.Fatalf("OpenRepository() error = %v", err)
	}
	return repository
}

func openDatabase(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	return db
}

func queryInt(t *testing.T, db *sql.DB, statement string, args ...any) int {
	t.Helper()
	var value int
	if err := db.QueryRow(statement, args...).Scan(&value); err != nil {
		t.Fatalf("query integer: %v", err)
	}
	return value
}

func tableExists(t *testing.T, db *sql.DB, table string) bool {
	t.Helper()
	return queryInt(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table) == 1
}

func closeForTamper(t *testing.T, repository *persistence.Repository) {
	t.Helper()
	if err := repository.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func tamper(t *testing.T, path, statement string, arguments ...any) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("tamper repository row: %v", err)
	}
}

func tamperWithoutForeignKeys(t *testing.T, path, statement string, arguments ...any) {
	t.Helper()
	db := openDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatalf("disable foreign keys for tamper: %v", err)
	}
	if _, err := db.Exec(statement, arguments...); err != nil {
		t.Fatalf("tamper repository row: %v", err)
	}
}

func assertCorruptOnLegacyReopenOrOperation(
	t *testing.T,
	path string,
	operation func(*persistence.Repository) error,
) {
	t.Helper()
	repository, err := persistence.OpenRepository(context.Background(), path, persistence.RepositoryOptions{})
	if err != nil {
		if !errors.Is(err, persistence.ErrCorrupt) {
			t.Fatalf("OpenRepository() error = %v, want ErrCorrupt", err)
		}
		return
	}
	defer repository.Close()
	if err := operation(repository); !errors.Is(err, persistence.ErrCorrupt) {
		t.Fatalf("repository operation error = %v, want ErrCorrupt", err)
	}
}
