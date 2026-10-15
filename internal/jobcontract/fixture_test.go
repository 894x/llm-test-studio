package jobcontract

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func testMeta(t *testing.T) domain.EntityMeta {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func preparedRunFixture(t *testing.T) runs.PreparedRun {
	t.Helper()
	modelMeta, channelMeta, mappingMeta := testMeta(t), testMeta(t), testMeta(t)
	testCase := domain.TestCase{
		EntityMeta: testMeta(t), Key: "contract", Name: "Contract", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definitions: domain.ProtocolDefinitions{domain.ProtocolOpenAIChat: json.RawMessage(
			`{"request":{"body":{"messages":[{"role":"user","content":"hi"}]}},"inputs":{},"assertions":[]}`,
		)},
	}
	suite := domain.Suite{
		EntityMeta: testMeta(t), Key: "contract", Name: "Contract", Protocol: domain.ProtocolOpenAIChat,
		Inputs: []domain.SuiteInput{}, Cases: []domain.CaseRef{{CaseID: testCase.ID}},
	}
	entry := domain.PlanEntry{
		EntryID: testMeta(t).ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
		Parameters: map[string]json.RawMessage{},
		Load: domain.LoadProfile{
			Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 1000,
		},
		SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 1000}},
	}
	plan := domain.Plan{
		EntityMeta: testMeta(t), Name: "Contract", Protocol: domain.ProtocolOpenAIChat,
		Seed: 1, Entries: []domain.PlanEntry{entry},
	}
	mapping := domain.ChannelModel{
		EntityMeta: mappingMeta, ChannelID: channelMeta.ID, ModelID: modelMeta.ID,
		Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, UpstreamModelName: "model",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: 1},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: modelMeta.ID, Revision: 1},
			Name:              "Model", Protocol: domain.ProtocolOpenAIChat,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channelMeta.ID, Revision: 1},
			Name:              "Channel", BaseURL: "https://example.test", Protocol: domain.ProtocolOpenAIChat,
			UpstreamModelName: "model",
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "linux", Arch: "amd64", Region: "test", NetworkEgress: "unknown",
			AppVersion: "test", EngineVersion: "test",
		},
		PlanDocument: &plan, Mapping: &mapping,
		Entries: []domain.RunEntrySnapshot{{
			EntryID: entry.EntryID, TargetKind: entry.TargetKind, TargetID: entry.TargetID,
			Name: suite.Name, Key: suite.Key, Suite: &suite,
			Cases:           []domain.CaseRevisionRef{{CaseID: testCase.ID, Revision: 1}},
			CaseDefinitions: []domain.TestCase{testCase}, Parameters: map[string]json.RawMessage{},
			CaseInputs: map[string]map[string]json.RawMessage{testCase.ID: {}}, Load: entry.Load, SLA: entry.SLA,
		}},
	}
	meta := testMeta(t)
	run, err := domain.NewRun(meta, plan.ID, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return runs.PreparedRun{Meta: meta, PlanID: plan.ID, Snapshot: run.Snapshot()}
}

func comparisonSpec(t *testing.T, count int) JobSpec {
	t.Helper()
	spec := performanceSpec(t)
	first := preparedRunFixture(t)
	input := comparisons.PreparedComparison{
		Meta: testMeta(t), Plan: first.Snapshot.Plan, Model: first.Snapshot.Model.EntityRevisionRef,
		Runs: make([]runs.PreparedRun, 0, count),
	}
	for range count {
		run, err := domain.NewRun(testMeta(t), first.PlanID, first.Snapshot)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := run.Snapshot()
		snapshot.Channel.ID = testMeta(t).ID
		snapshot.Mapping.ChannelID = snapshot.Channel.ID
		snapshot.Mapping.ID = testMeta(t).ID
		input.Runs = append(input.Runs, runs.PreparedRun{Meta: run.Meta(), PlanID: run.PlanID(), Snapshot: snapshot})
	}
	spec.Kind = KindComparison
	spec.Spec = Payload{Comparison: &input}
	return spec
}
