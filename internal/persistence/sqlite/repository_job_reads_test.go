package sqlite_test

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func TestJobListKeysetKeepsAcceptanceOrderAcrossOwnerGapsAndNewAdmissions(t *testing.T) {
	repository := openOperationalRepository(t)
	defer repository.Close()
	service := quicktest.New(quicktest.Dependencies{})
	prepared, lease, code := service.BuildPerformance(t.Context(), quicktest.PerformanceCommand{
		Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
		URL: "https://example.com", APIKey: "list-test-key", ModelID: "model",
		RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 32, OutputTokens: 2,
	})
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	query := jobhost.JobListQuery{
		InstallationID: "72000000-0000-4000-8000-000000000002",
		Target: jobcontract.ExecutionTarget{
			Kind: jobcontract.TargetRemote, NodeID: "72000000-0000-4000-8000-000000000003",
		},
		Limit: 200,
	}
	empty, err := repository.ListJobs(t.Context(), query)
	if err != nil || empty.Jobs == nil || len(empty.Jobs) != 0 || empty.NextCursor != "0" || empty.HasMore {
		t.Fatalf("initial page=%+v: %v", empty, err)
	}
	expected := []string{}
	for index := range 203 {
		performance := prepared
		performance.ReportID = fmt.Sprintf("73000000-0000-4000-8000-%012d", index+1)
		spec := jobcontract.JobSpec{
			Contract: jobcontract.CurrentContract, JobID: fmt.Sprintf("74000000-0000-4000-8000-%012d", index+1),
			NodeInstallationID: query.InstallationID, EngineBuildID: strings.Repeat("a", 64),
			Target: query.Target, Kind: jobcontract.KindPerformance,
			Spec: jobcontract.Payload{Performance: &performance}, Assets: []jobcontract.Asset{},
		}
		foreign := index == 1 || index == 101
		if index == 1 {
			spec.Target.NodeID = "72000000-0000-4000-8000-000000000004"
		}
		if index == 101 {
			spec.NodeInstallationID = "72000000-0000-4000-8000-000000000005"
		}
		if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(spec)); err != nil {
			t.Fatal(err)
		}
		if !foreign {
			expected = append(expected, spec.JobID)
		}
	}
	first, err := repository.ListJobs(t.Context(), query)
	if err != nil || len(first.Jobs) != 200 || !first.HasMore || first.NextCursor != "202" {
		t.Fatalf(
			"full page: count=%d cursor=%s: %v",
			len(first.Jobs),
			first.NextCursor,
			err,
		)
	}
	for index, snapshot := range first.Jobs {
		if snapshot.Receipt.JobID != expected[index] {
			t.Fatal("list sorted by a mutable field or leaked another owner")
		}
	}
	query.After, _ = strconv.ParseUint(first.NextCursor, 10, 64)
	query.Limit = 1
	last, err := repository.ListJobs(t.Context(), query)
	if err != nil || len(last.Jobs) != 1 || last.HasMore || last.Jobs[0].Receipt.JobID != expected[200] {
		t.Fatalf("last page=%+v: %v", last, err)
	}
	// A new admission must remain discoverable after the saved end cursor.
	storedSpec, err := repository.GetJobSpec(t.Context(), expected[200])
	if err != nil {
		t.Fatal(err)
	}
	storedSpec.JobID = "74000000-0000-4000-8000-000000000204"
	storedSpec.Spec.Performance.ReportID = "73000000-0000-4000-8000-000000000204"
	if _, _, err := repository.AcceptJob(t.Context(), jobAcceptance(storedSpec)); err != nil {
		t.Fatal(err)
	}
	query.After, _ = strconv.ParseUint(last.NextCursor, 10, 64)
	next, err := repository.ListJobs(t.Context(), query)
	if err != nil || len(next.Jobs) != 1 || next.Jobs[0].Receipt.JobID != storedSpec.JobID {
		t.Fatalf("new admission was skipped: %v", err)
	}
	for _, change := range []func(*jobhost.JobListQuery){
		func(query *jobhost.JobListQuery) { query.Limit = 0 },
		func(query *jobhost.JobListQuery) { query.Limit = 201 },
		func(query *jobhost.JobListQuery) { query.After = math.MaxInt64 + 1 },
		func(query *jobhost.JobListQuery) { query.InstallationID = "invalid" },
		func(query *jobhost.JobListQuery) { query.Target.NodeID = "invalid" },
	} {
		invalid := query
		change(&invalid)
		if _, err := repository.ListJobs(t.Context(), invalid); !errors.Is(err, jobhost.ErrInvalid) {
			t.Fatalf("invalid list query returned %v", err)
		}
	}
}
