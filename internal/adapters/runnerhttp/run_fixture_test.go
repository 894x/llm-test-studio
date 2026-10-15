package runnerhttp_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

func httpMeta(t *testing.T) domain.EntityMeta {
	t.Helper()
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return meta
}

func httpRunInput(t *testing.T, baseURL string) runs.PreparedRun {
	t.Helper()
	model, channel := httpMeta(t), httpMeta(t)
	testCase := domain.TestCase{
		EntityMeta: httpMeta(t), Key: "node", Name: "Node request", Dimension: "compatibility",
		Enabled: true, Severity: domain.CaseSeverityNormal, ExecutionMode: domain.CaseExecutionAutomatic,
		Definitions: domain.ProtocolDefinitions{domain.ProtocolOpenAIChat: json.RawMessage(
			`{"request":{"body":{"stream":true,"messages":[{"role":"user","content":"hi"}]}},"inputs":{},"assertions":[]}`,
		)},
	}
	suite := domain.Suite{
		EntityMeta: httpMeta(t), Key: "node", Name: "Node suite", Protocol: domain.ProtocolOpenAIChat,
		Inputs: []domain.SuiteInput{}, Cases: []domain.CaseRef{{CaseID: testCase.ID}},
	}
	entry := domain.PlanEntry{
		EntryID: httpMeta(t).ID, TargetKind: domain.PlanTargetSuite, TargetID: suite.ID,
		Parameters: map[string]json.RawMessage{},
		Load: domain.LoadProfile{
			Mode: domain.LoadSingle, Concurrency: 1, RequestCount: 1, RequestTimeoutMS: 10_000,
		},
		SLA: domain.SLAProfile{Thresholds: map[string]float64{"e2e_p95_ms": 10_000}},
	}
	plan := domain.Plan{
		EntityMeta: httpMeta(t), Name: "Node plan", Protocol: domain.ProtocolOpenAIChat,
		Seed: 1, Entries: []domain.PlanEntry{entry},
	}
	mapping := domain.ChannelModel{
		EntityMeta: httpMeta(t), ChannelID: channel.ID, ModelID: model.ID,
		Protocols: []domain.Protocol{domain.ProtocolOpenAIChat}, UpstreamModelName: "model",
	}
	snapshot := domain.RunSnapshot{
		SchemaVersion: domain.CurrentRunSnapshotSchemaVersion,
		Plan:          domain.EntityRevisionRef{ID: plan.ID, Revision: 1},
		Model: domain.ModelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: model.ID, Revision: 1},
			Name:              "Model", Protocol: domain.ProtocolOpenAIChat,
		},
		Channel: domain.ChannelSnapshot{
			EntityRevisionRef: domain.EntityRevisionRef{ID: channel.ID, Revision: 1},
			Name:              "Node channel", BaseURL: baseURL, Protocol: domain.ProtocolOpenAIChat,
			UpstreamModelName: "model",
		},
		Environment: domain.EnvironmentSnapshot{
			OS: "linux", Arch: "amd64", Region: "mock-node", NetworkEgress: "test",
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
	return runs.PreparedRun{Meta: httpMeta(t), PlanID: plan.ID, Snapshot: snapshot}
}
