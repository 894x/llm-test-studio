package remoteresults

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func TestOwnedCollectionsRejectBeforeReadingOversizedInvalidTail(t *testing.T) {
	for _, test := range []struct {
		name   string
		check  func([]byte) error
		prefix string
		count  int
	}{
		{name: "report results", check: CheckRunReportBudget, prefix: `{"case_results":[`, count: MaxReportCollectionItems},
		{name: "event exchanges", check: CheckEventBudget, prefix: `{"observation":{"exchanges":[`, count: MaxEventCollectionItems},
		{name: "accepted snapshot entries", check: CheckEventBudget, prefix: `{"spec":{"spec":{"run":{"snapshot":{"entries":[`, count: MaxEventCollectionItems},
		{name: "decoded authored assertions", check: CheckEventBudget, prefix: `{"definitions":{"openai-chat":{"assertions":[`, count: MaxEventCollectionItems},
		{name: "input data key matches raw field", check: CheckEventBudget, prefix: `{"definitions":{"openai-chat":{"inputs":{"response":{"enum":[`, count: MaxEventCollectionItems},
	} {
		t.Run(test.name, func(t *testing.T) {
			// This is intentionally not valid JSON. The budget must fail before
			// the decoder reaches the malformed next item, without typed slices.
			raw := []byte(test.prefix + strings.Repeat(`{},`, test.count) + `!`)
			if err := test.check(raw); !errors.Is(err, ErrCollectionBudget) {
				t.Fatalf("early count check: %v", err)
			}
		})
	}
}

func TestProviderJSONLeavesRemainOpaqueToOwnedCollectionBudgets(t *testing.T) {
	wide := `[` + strings.Repeat(`0,`, MaxEventCollectionItems) + `0]`
	for _, field := range []string{"response", "request_body", "actual", "expected", "body"} {
		if err := CheckEventBudget([]byte(`{"` + field + `":` + wide + `}`)); err != nil {
			t.Fatalf("opaque %s: %v", field, err)
		}
	}
	for _, field := range []string{"parameters"} {
		if err := CheckEventBudget([]byte(`{"` + field + `":{"provider":` + wide + `}}`)); err != nil {
			t.Fatalf("raw map %s: %v", field, err)
		}
	}
	if err := CheckEventBudget([]byte(`{"definitions":{"openai-chat":{"inputs":{},"request":{"body":` + wide + `},"assertions":[]}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckEventBudget([]byte(`{"observation":{"events":[` + wide + `]}}`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckEventBudget([]byte(`{"observation":{"response":{"exchanges":` + wide + `}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckEventBudget([]byte(`{"case_inputs":{"case":{"input":` + wide + `}}}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCaseInputContainersAreCountedBeforeOpaqueProviderValues(t *testing.T) {
	var members strings.Builder
	for index := range MaxEventCollectionItems {
		members.WriteString(`"` + strconv.Itoa(index) + `":{},`)
	}
	for _, prefix := range []string{`{"case_inputs":{`, `{"case_inputs":{"case":{`, `{"case_inputs":{"response":{`} {
		if err := CheckEventBudget([]byte(prefix + members.String() + `!`)); !errors.Is(err, ErrCollectionBudget) {
			t.Fatalf("case/input container escaped early budget: %v", err)
		}
	}
}

func TestDocumentBudgetsRejectDeepMalformedAndTrailingValues(t *testing.T) {
	for _, raw := range []string{`{`, `{} {}`, `{"items":[1}`, strings.Repeat(`[`, maxDocumentDepth+2) + `0` + strings.Repeat(`]`, maxDocumentDepth+2)} {
		if err := CheckEventBudget([]byte(raw)); err == nil {
			t.Fatal("invalid/deep JSON accepted")
		}
	}
}
