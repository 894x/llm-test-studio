package jobcontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jsonbudget"
)

func TestStandaloneSpecDecoderUsesCurrentContractAndSubmissionBudgets(t *testing.T) {
	spec := performanceSpec(t)
	document, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeSpec(bytes.NewReader(document))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := spec.Digest()
	got, _ := decoded.Digest()
	if want != got {
		t.Fatal("standalone decoder changed frozen input")
	}
	for _, rejected := range []string{
		strings.Replace(
			string(document),
			`"contract":1`,
			`"contract":0`,
			1,
		),
		strings.Replace(
			string(document),
			`"contract":1`,
			`"Contract":1`,
			1,
		),
		strings.Replace(
			string(document),
			`"contract":1`,
			`"contract":1,"contract":1`,
			1,
		),
		strings.Replace(
			string(document),
			`"assets":[]`,
			`"assets":null`,
			1,
		),
		strings.Replace(
			string(document),
			`"assets":[]`,
			`"assets":[`+strings.Repeat(`null,`, MaxAssets)+`null]`,
			1,
		),
		`{"old_spec":{}}`,
	} {
		if _, err := DecodeSpec(strings.NewReader(rejected)); err == nil {
			t.Fatal("unsupported or over-budget standalone spec was accepted")
		}
	}
	if _, err := DecodeSpec(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := DecodeSpec(strings.NewReader(strings.Repeat(" ", MaxSubmissionBytes+1))); !errors.Is(err, ErrTooLarge) {
		t.Fatal("standalone byte budget was not enforced")
	}
}

func TestSpecOwnedCollectionsRejectBeforeMalformedTailAndNativeBodiesRemainOpaque(t *testing.T) {
	for _, prefix := range []string{
		`{"spec":{"run":{"snapshot":{"entries":[`,
		`{"spec":{"run":{"snapshot":{"entries":[{"case_definitions":[`,
		`{"spec":{"run":{"snapshot":{"entries":[{"case_definitions":[{"definitions":{"openai-chat":{"assertions":[`,
	} {
		raw := prefix + strings.Repeat(`{},`, MaxOwnedItems) + `!`
		if _, err := DecodeSpec(strings.NewReader(raw)); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("owned collection reached invalid tail: %v", err)
		}
	}
	inputs := `{"spec":{"run":{"snapshot":{"entries":[{"case_inputs":{"case":{` +
		strings.Repeat(`"input":0,`, MaxOwnedItems) + `!`
	if _, err := DecodeSpec(strings.NewReader(inputs)); !errors.Is(err, ErrTooLarge) {
		t.Fatal("case input containers escaped the owned collection budget")
	}
	deep := strings.Repeat(`[`, jsonbudget.MaxDepth+2) + `0` + strings.Repeat(`]`, jsonbudget.MaxDepth+2)
	if _, err := DecodeSpec(strings.NewReader(deep)); !errors.Is(err, ErrTooLarge) {
		t.Fatal("owned nesting budget was not enforced")
	}
	prepared := preparedRunFixture(t)
	wide := `[` + strings.Repeat(`0,`, MaxOwnedItems) + `0]`
	prepared.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] =
		json.RawMessage(`{"request":{"body":{"provider_data":` + wide + `}},"inputs":{},"assertions":[]}`)
	spec := performanceSpec(t)
	spec.Kind, spec.Spec = KindRun, Payload{Run: &prepared}
	document, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeSpec(bytes.NewReader(document)); err != nil {
		t.Fatalf("native provider body was treated as owned DTO collections: %v", err)
	}
}
