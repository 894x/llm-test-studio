package jobhost_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/testspec"
)

type fileAdmissionRepository struct {
	jobhost.Repository
	lookups, accepts int
}

func (repository *fileAdmissionRepository) LookupJob(
	ctx context.Context,
	input jobhost.Acceptance,
) (jobcontract.Snapshot, error) {
	repository.lookups++
	return repository.Repository.LookupJob(ctx, input)
}

func (repository *fileAdmissionRepository) AcceptJob(
	ctx context.Context,
	input jobhost.Acceptance,
) (jobcontract.Snapshot, bool, error) {
	repository.accepts++
	return repository.Repository.AcceptJob(ctx, input)
}

func setAdmissionInputType(run *runs.PreparedRun, file bool) {
	entry := &run.Snapshot.Entries[0]
	testCase := &entry.CaseDefinitions[0]
	input := testspec.Input{Type: "object", Required: true}
	if file {
		input.Type = "file"
		input.File = &testspec.FileInput{
			Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
		}
	}
	spec := testspec.Spec{
		Inputs: map[string]testspec.Input{"image": input}, Assertions: []testspec.Assertion{},
		Request: testspec.Request{Body: json.RawMessage(`{"image":{"$input":"image"}}`)},
	}
	testCase.Definitions[domain.ProtocolOpenAIChat], _ = json.Marshal(spec)
	entry.Suite.Inputs = []domain.SuiteInput{{
		Key: "image", Label: "Image", Input: input,
		Bindings: []domain.SuiteInputBinding{{CaseID: testCase.ID, Input: "image"}},
	}}
	entry.Parameters["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
	entry.CaseInputs[testCase.ID]["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
	run.Snapshot.PlanDocument.Entries[0].Parameters["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
}

func TestHostFileAdmissionRejectsBeforeOwnershipOrPersistence(t *testing.T) {
	for _, variant := range []struct {
		name       string
		comparison bool
	}{
		{name: "run"}, {name: "comparison", comparison: true},
	} {
		t.Run(variant.name, func(t *testing.T) {
			durable := openHostRepository(t)
			repository := &fileAdmissionRepository{Repository: durable}
			core, guard := newControlledCore(), &countedGuard{}
			host := newTestHost(t, hostDependencies(repository, core, guard))
			submission := coreSubmission(t, "https://example.test", variant.comparison)
			spec := &submission.Job
			inputs := []*runs.PreparedRun{}
			if variant.comparison {
				for index := range spec.Spec.Comparison.Runs {
					inputs = append(inputs, &spec.Spec.Comparison.Runs[index])
				}
			} else {
				inputs = append(inputs, spec.Spec.Run)
			}
			for _, input := range inputs {
				setAdmissionInputType(input, true)
				if err := input.Snapshot.Validate(); err != nil {
					t.Fatalf("file snapshot fixture is invalid: %v", err)
				}
			}
			if variant.comparison {
				if _, err := spec.Spec.Comparison.Comparison(); err != nil {
					t.Fatalf("file comparison fixture is invalid: %v", err)
				}
			}
			if len(spec.Assets) != 0 {
				t.Fatal("fixture must exercise file bindings with an empty asset manifest")
			}
			_, created, err := host.Submit(t.Context(), &submission)
			if !errors.Is(err, jobcontract.ErrUnsupported) || created {
				t.Fatalf("file admission created=%v, error=%v", created, err)
			}
			if _, err := durable.GetJob(t.Context(), spec.JobID); !errors.Is(err, jobhost.ErrNotFound) {
				t.Fatalf("rejected file Job was persisted: %v", err)
			}
			runRows, err := durable.ListRuns(t.Context())
			if err != nil || len(runRows) != 0 {
				t.Fatalf("rejected file child Runs persisted: count=%d, error=%v", len(runRows), err)
			}
			comparisonRows, err := durable.ListComparisons(t.Context())
			if err != nil || len(comparisonRows) != 0 {
				t.Fatalf("rejected file Comparison persisted: count=%d, error=%v", len(comparisonRows), err)
			}
			status := host.Status()
			guard.mu.Lock()
			reserved := guard.reserved
			guard.mu.Unlock()
			core.mu.Lock()
			calls := core.calls
			core.mu.Unlock()
			if repository.lookups != 0 || repository.accepts != 0 || reserved != 0 || calls != 0 {
				t.Fatalf("rejected file touched ownership: lookups=%d, accepts=%d, reserved=%d, executed=%d",
					repository.lookups, repository.accepts, reserved, calls)
			}
			if status.Active != 0 || status.Queued != 0 || status.Pending != 0 {
				t.Fatalf("rejected file acquired Host ownership: %+v", status)
			}
			// Change only the declared type. The same asset_ref object is ordinary
			// JSON, so the same Job identity and remaining snapshot must be valid.
			for _, input := range inputs {
				setAdmissionInputType(input, false)
			}
			for _, slot := range spec.SecretSlots() {
				submission.Secrets = append(submission.Secrets, jobcontract.Secret{Slot: slot, Value: []byte("node-secret")})
			}
			if err := submission.Validate(); err != nil {
				t.Fatalf("same snapshot with ordinary JSON is invalid: %v", err)
			}
			if _, created, err := host.Submit(t.Context(), &submission); err != nil || !created {
				t.Fatalf("ordinary JSON admission created=%v, error=%v", created, err)
			}
			enteredExecution(t, core)
			close(core.release)
			waitJob(t, durable, spec.JobID, domain.RunCompleted)
		})
	}
}
