package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/remoteresults"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

type k3PreflightTransport struct{ calls atomic.Int64 }

func (transport *k3PreflightTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.calls.Add(1)
	return nil, errors.New("preflight must not access a provider")
}

// Full authored definitions, including their assertions and protocol variants,
// must fit both the request envelope and the accepted-event import budget.
func TestRemoteJobEnvelopeFitsCompleteAuthoredK3Suites(t *testing.T) {
	transport := &k3PreflightTransport{}
	configurationRoot, executableRoot := t.TempDir(), t.TempDir()
	dependencies, err := newProductionInitializer(productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(executableRoot, "preflight.exe"), nil },
		appVersion:     "k3-preflight", credentialStore: credentials.NewMemoryStore(), protocolTransport: transport,
	})(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer dependencies.close()
	catalog, err := dependencies.catalog.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	service, ok := dependencies.commands.(*runs.Service)
	if !ok {
		t.Fatal("preflight cannot obtain the production preparer")
	}
	for key, expectedCases := range map[string]int{
		"kimi-k3.official-foundation": 84, "openai-responses.kimi-k3.official-foundation": 230,
	} {
		t.Run(key, func(t *testing.T) {
			suiteID := ""
			for _, suite := range catalog.Suites {
				if suite.Key == key {
					suiteID = suite.ID
				}
			}
			if suiteID == "" {
				t.Fatal("authored K3 suite is missing from the production catalog")
			}
			prepared, lease, err := service.Preparer.BuildQuickTask(t.Context(), runs.QuickTaskCommand{
				SuiteID: suiteID, Model: "kimi-k3", BaseURL: "https://provider.example.test",
				APIKey: "preflight-placeholder-only", RequestTimeoutMS: 120_000, CaseConcurrency: 1,
				Inputs: map[string]json.RawMessage{},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			caseCount := 0
			for _, entry := range prepared.Snapshot.Entries {
				caseCount += len(entry.CaseDefinitions)
			}
			if caseCount != expectedCases {
				t.Fatalf("prepared %d cases, expected full suite of %d", caseCount, expectedCases)
			}
			meta, err := domain.NewEntityMeta(time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			spec, err := (jobcontract.JobSpec{
				Contract: jobcontract.CurrentContract, JobID: meta.ID,
				NodeInstallationID: "83000000-0000-4000-8000-000000000001", EngineBuildID: strings.Repeat("a", 64),
				Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote,
					NodeID: "83000000-0000-4000-8000-000000000002"},
				Kind: jobcontract.KindRun, Spec: jobcontract.Payload{Run: &prepared}, Assets: []jobcontract.Asset{},
			}).Freeze()
			if err != nil {
				t.Fatalf("full K3 preparation cannot be frozen: %v", err)
			}
			secret, err := lease.Bytes()
			if err != nil {
				t.Fatal(err)
			}
			submission := &jobcontract.Submission{Job: spec,
				Secrets: []jobcontract.Secret{{Slot: prepared.Meta.ID, Value: secret}}}
			defer submission.Clear()
			document, err := json.Marshal(submission)
			if err != nil {
				t.Fatal(err)
			}
			defer clear(document)
			decoded, err := jobcontract.DecodeSubmission(bytes.NewReader(document))
			if err != nil {
				t.Fatalf("full K3 submission cannot cross the management boundary: %v", err)
			}
			decoded.Clear()
			snapshot, err := spec.InitialSnapshot(meta.CreatedAt, domain.ExecutionOrigin{
				Kind: "remote", NodeID: spec.Target.NodeID, InstallationID: spec.NodeInstallationID,
				EngineBuildID: spec.EngineBuildID, RunnerVersion: "preflight", OS: "linux", Arch: "amd64",
			})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := json.Marshal(jobcontract.AcceptedEvent{Spec: spec, Snapshot: snapshot})
			if err != nil || remoteresults.CheckEventBudget(accepted) != nil {
				t.Fatal("full K3 accepted event cannot be imported")
			}
			t.Logf(
				"full suite: cases=%d submission_bytes=%d accepted_bytes=%d",
				caseCount,
				len(document),
				len(accepted),
			)
		})
	}
	t.Run("minimum-performance", func(t *testing.T) {
		service, ok := dependencies.performancePreparation.(*quicktest.Service)
		if !ok {
			t.Fatal("preflight cannot obtain the production performance preparer")
		}
		prepared, lease, code := service.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://provider.example.test", APIKey: "preflight-placeholder-only", ModelID: "kimi-k3",
			LoadMode: domain.LoadFixedConcurrency, WorkloadMode: quicktest.PerformanceWorkloadFixed,
			RequestCount: 3, Concurrency: 1, TimeoutMS: 120_000, InputTokens: 32, OutputTokens: 128,
		})
		if code != "" {
			t.Fatalf("minimum performance preparation: %s", code)
		}
		defer lease.Close()
		profile := prepared.Profile
		extraTraffic := profile.WarmupRequests > 0 || profile.RampDurationMS > 0 || profile.CapacityEnabled
		if profile.RequestCount != 3 || profile.Concurrency != 1 || extraTraffic {
			t.Fatal("minimum performance unexpectedly adds traffic or concurrency")
		}
		meta, err := domain.NewEntityMeta(time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		spec, err := (jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: meta.ID,
			NodeInstallationID: "83000000-0000-4000-8000-000000000001", EngineBuildID: strings.Repeat("a", 64),
			Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote,
				NodeID: "83000000-0000-4000-8000-000000000002"},
			Kind: jobcontract.KindPerformance, Spec: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
		}).Freeze()
		if err != nil {
			t.Fatal(err)
		}
		secret, err := lease.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		submission := &jobcontract.Submission{Job: spec,
			Secrets: []jobcontract.Secret{{Slot: prepared.ReportID, Value: secret}}}
		defer submission.Clear()
		document, err := json.Marshal(submission)
		if err != nil {
			t.Fatal(err)
		}
		defer clear(document)
		decoded, err := jobcontract.DecodeSubmission(bytes.NewReader(document))
		if err != nil {
			t.Fatal(err)
		}
		decoded.Clear()
		t.Logf(
			"minimum performance: requests=%d concurrency=%d input_target=%d output_cap=%d submission_bytes=%d",
			profile.RequestCount,
			profile.Concurrency,
			profile.InputTokens,
			profile.OutputTokens,
			len(document),
		)
	})
	if transport.calls.Load() != 0 {
		t.Fatal("T0 preparation made a provider request")
	}
	workspace, err := dependencies.query.Snapshot(t.Context())
	if err != nil || len(workspace.Runs) != 0 {
		t.Fatal("preflight started or persisted an execution")
	}
}
