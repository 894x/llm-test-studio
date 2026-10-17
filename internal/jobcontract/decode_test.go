package jobcontract

import (
	"errors"
	"strings"
	"testing"
)

func TestDecoderRejectsOversizedCollectionsBeforeParsingTheirRemainingElements(t *testing.T) {
	for _, test := range []struct {
		name   string
		prefix string
		limit  int
		field  string
	}{
		{name: "job assets", prefix: `{"job":{"assets":[`, limit: MaxAssets, field: "assets"},
		{name: "direct spec assets", prefix: `{"assets":[`, limit: MaxAssets, field: "assets"},
		{name: "secrets", prefix: `{"secrets":[`, limit: MaxSecrets, field: "secrets"},
		{
			name: "comparison children", prefix: `{"job":{"spec":{"comparison":{"runs":[`,
			limit: MaxSecrets, field: "spec.comparison.runs",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// The next element is deliberately invalid JSON. A streaming count
			// limit must reject it before parsing or allocating any of that tail.
			raw := test.prefix + strings.Repeat(`{},`, test.limit) + `invalid-tail`
			_, err := DecodeSubmission(strings.NewReader(raw))
			var invalidError *InvalidError
			if !errors.As(err, &invalidError) || invalidError.Field != test.field {
				t.Fatalf("did not stop at collection limit: %v", err)
			}
		})
	}
}

func TestCollectionScannerDoesNotConstrainNativeRequestArrayNames(t *testing.T) {
	raw := `{"spec":{"run":{"snapshot":{"entries":[{"parameters":{"runs":[` +
		strings.Repeat(`{},`, MaxSecrets) + `{}` + `]}}]}}}}`
	if err := checkCollectionBounds([]byte(raw), scopeRoot); err != nil {
		t.Fatalf("scanner treated native JSON as a job-owned collection: %v", err)
	}
}

func TestDecoderRejectsNoncanonicalEnvelopeFieldsBeforeTypedAllocation(t *testing.T) {
	for _, prefix := range []string{
		`{"Job":{"assets":[`, `{"job":{"Assets":[`, `{"job":{"ASSETS":[`,
		`{"job":{"aſſets":[`, `{"Secrets":[`, `{"SECRETS":[`,
		`{"job":{"Spec":{"comparison":{"runs":[`,
		`{"job":{"spec":{"Comparison":{"runs":[`,
		`{"job":{"spec":{"comparison":{"Runs":[`,
	} {
		raw := prefix + strings.Repeat(`{},`, MaxAssets+1) + `invalid-tail`
		_, err := DecodeSubmission(strings.NewReader(raw))
		var invalidError *InvalidError
		if !errors.As(err, &invalidError) || invalidError.Field != "body" {
			t.Fatalf("noncanonical own field was not rejected before its value: %s: %v", prefix, err)
		}
	}
}
