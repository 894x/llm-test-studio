package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/testspec"
)

func addFileInputFixture(run *runs.PreparedRun) {
	entry := &run.Snapshot.Entries[0]
	testCase := &entry.CaseDefinitions[0]
	testCase.Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{
		"inputs":{"image":{"type":"file","required":true,"file":{"encoding":"image_data_url","media_types":["image/png"],"max_bytes":1048576}}},
		"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`)
	entry.Suite.Inputs = []domain.SuiteInput{{
		Key: "image", Label: "Image",
		Input: testspec.Input{Type: "file", Required: true, File: &testspec.FileInput{
			Encoding: "image_data_url", MediaTypes: []string{"image/png"}, MaxBytes: testspec.MaxFileInputBytes,
		}},
		Bindings: []domain.SuiteInputBinding{{CaseID: testCase.ID, Input: "image"}},
	}}
	entry.Parameters["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
	entry.CaseInputs[testCase.ID]["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
	run.Snapshot.PlanDocument.Entries[0].Parameters["image"] = json.RawMessage(`{"asset_ref":"image-1"}`)
}

func TestAdmissionRejectsFileRunAndComparisonWithoutAssetResolver(t *testing.T) {
	for _, kind := range []Kind{KindRun, KindComparison} {
		t.Run(string(kind), func(t *testing.T) {
			spec := performanceSpec(t)
			if kind == KindRun {
				run := preparedRunFixture(t)
				addFileInputFixture(&run)
				if err := run.Snapshot.Validate(); err != nil {
					t.Fatalf("file fixture is not a valid logical snapshot: %v", err)
				}
				spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
			} else {
				spec = comparisonSpec(t, 2)
				for index := range spec.Spec.Comparison.Runs {
					addFileInputFixture(&spec.Spec.Comparison.Runs[index])
				}
				if _, err := spec.Spec.Comparison.Comparison(); err != nil {
					t.Fatalf("file comparison fixture is invalid: %v", err)
				}
			}
			before, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if err := spec.Validate(); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("file admission error = %v, want unsupported", err)
			}
			if _, err := spec.Freeze(); !errors.Is(err, ErrUnsupported) {
				t.Fatalf("file freeze error = %v, want unsupported", err)
			}
			after, err := json.Marshal(spec)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("rejected file submission mutated its snapshot")
			}
		})
	}
}

func TestAdmissionRejectsMismatchedSuiteFileDeclaration(t *testing.T) {
	run := preparedRunFixture(t)
	addFileInputFixture(&run)
	run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = json.RawMessage(`{
		"inputs":{"image":{"type":"object","required":true}},
		"request":{"body":{"image":{"$input":"image"}}},"assertions":[]}`)
	if err := run.Snapshot.Validate(); err == nil {
		t.Fatal("mismatched logical file declaration accepted")
	}
	spec := performanceSpec(t)
	spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
	if err := spec.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched suite file admission error = %v", err)
	}
}
