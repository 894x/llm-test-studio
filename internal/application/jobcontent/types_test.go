package jobcontent

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func testObject() ObjectIdentity {
	return ObjectIdentity{
		StoreID: "bd9ca624-cfc3-49c5-a5a4-f96b6ae4cbb2",
		Content: jobcontract.ContentIdentity{
			SHA256: strings.Repeat("a", 64), Size: 1,
		},
		IncarnationID: "354ad895-d1bd-432d-a731-47fe268509e9",
		WitnessSHA256: strings.Repeat("b", 64),
		ChangeToken:   strings.Repeat("c", 64),
	}
}

func TestObjectIdentityRequiresPrivatePhysicalIdentity(t *testing.T) {
	if err := testObject().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*ObjectIdentity)
	}{
		{name: "store path", change: func(value *ObjectIdentity) { value.StoreID = "../content" }},
		{name: "missing incarnation", change: func(value *ObjectIdentity) { value.IncarnationID = "" }},
		{name: "upper witness", change: func(value *ObjectIdentity) { value.WitnessSHA256 = strings.Repeat("A", 64) }},
		{name: "missing change token", change: func(value *ObjectIdentity) { value.ChangeToken = "" }},
		{name: "empty bytes", change: func(value *ObjectIdentity) { value.Content.Size = 0 }},
		{name: "over limit", change: func(value *ObjectIdentity) { value.Content.Size = jobcontract.MaxAssetBytes + 1 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := testObject()
			test.change(&value)
			if !errors.Is(value.Validate(), ErrInvalid) {
				t.Fatal("accepted invalid physical identity")
			}
		})
	}
}

func TestStoreAndGenerationRequireExplicitCurrentMetadata(t *testing.T) {
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	store := Store{
		ID: testObject().StoreID, Kind: StoreKindExecutor,
		InstallationID: "19c9ca0c-b904-49d9-8de4-7056bd409bdb", CreatedAt: at,
	}
	if err := store.Validate(); err != nil {
		t.Fatal(err)
	}
	store.Kind = ""
	if !errors.Is(store.Validate(), ErrInvalid) {
		t.Fatal("missing store kind defaulted to executor")
	}
	generation := Generation{Object: testObject(), Number: 1, CreatedAt: at}
	if err := generation.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, number := range []uint64{0, math.MaxInt64 + 1} {
		generation.Number = number
		if !errors.Is(generation.Validate(), ErrInvalid) {
			t.Fatal("accepted generation outside SQLite integer domain")
		}
	}
}

func TestClaimsRequireAnExplicitExactAssetSet(t *testing.T) {
	empty := jobcontract.JobSpec{Assets: []jobcontract.Asset{}}
	if err := ValidateClaims(empty, []Claim{}); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(ValidateClaims(empty, nil), ErrInvalid) {
		t.Fatal("missing claims defaulted to an empty set")
	}
	if !errors.Is(ValidateClaims(jobcontract.JobSpec{}, []Claim{}), ErrInvalid) {
		t.Fatal("missing manifest defaulted to an empty set")
	}
	claim := Claim{Ref: "image", Object: testObject(), MediaType: "image/png"}
	spec := jobcontract.JobSpec{Assets: []jobcontract.Asset{{
		Ref: claim.Ref, SHA256: claim.Object.Content.SHA256,
		Size: claim.Object.Content.Size, MediaType: claim.MediaType,
	}}}
	if err := ValidateClaims(spec, []Claim{claim}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Claim)
	}{
		{name: "wrong ref", change: func(value *Claim) { value.Ref = "other" }},
		{name: "wrong bytes", change: func(value *Claim) { value.Object.Content.Size++ }},
		{name: "wrong mime", change: func(value *Claim) { value.MediaType = "image/jpeg" }},
		{name: "mime parameters", change: func(value *Claim) { value.MediaType = "image/png; charset=utf-8" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := claim
			test.change(&value)
			if !errors.Is(ValidateClaims(spec, []Claim{value}), ErrInvalid) {
				t.Fatal("accepted a nonmatching asset claim")
			}
		})
	}
	spec.Assets = append(spec.Assets, jobcontract.Asset{
		Ref: "second", SHA256: strings.Repeat("d", 64), Size: 1, MediaType: "image/png",
	})
	if !errors.Is(ValidateClaims(spec, []Claim{claim, claim}), ErrInvalid) {
		t.Fatal("duplicate claim replaced a missing asset")
	}
}
