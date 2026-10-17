package integration

import (
	"testing"
	"time"

	bridge "github.com/894x/llm-test-studio/apps/desktop/internal/bridge"
	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func productionLocalPerformance(
	t *testing.T,
	dependencies bridge.Dependencies,
	endpoint string,
) (jobs.SubmissionRecord, *credentials.Lease) {
	t.Helper()
	preparer, ok := dependencies.PerformancePreparation.(*quicktest.Service)
	if !ok {
		t.Fatal("performance preparation is unavailable")
	}
	prepared, lease, code := preparer.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: endpoint, APIKey: "local-owner-fixture-secret", ModelID: "local-fixture",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 10_000, InputTokens: 8, OutputTokens: 2,
	})
	if code != "" {
		t.Fatal(code)
	}
	t.Cleanup(func() { _ = lease.Close() })
	meta, err := domain.NewEntityMeta(time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	record, err := dependencies.Jobs.Prepare(t.Context(), jobs.PrepareCommand{
		Location: jobs.Location{Kind: jobcontract.TargetLocal}, JobID: meta.ID,
		Kind: jobcontract.KindPerformance, Payload: jobcontract.Payload{Performance: &prepared}, Assets: []jobcontract.Asset{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return record, lease
}
