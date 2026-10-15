package runnerartifacts

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestManifestRejectsMissingDuplicateUnknownAndSupersededFields(t *testing.T) {
	_, source, _ := artifactFixture(t)
	base := string(source.documents[ManifestName])
	for name, document := range map[string]string{
		"format": strings.Replace(
			base,
			`"format":1`,
			`"format":2`,
			1,
		),
		"contract": strings.Replace(
			base,
			`"contract":1`,
			`"contract":2`,
			1,
		),
		"missing false": strings.Replace(
			base,
			`"source_dirty":false,`,
			"",
			1,
		),
		"null false": strings.Replace(
			base,
			`"source_dirty":false`,
			`"source_dirty":null`,
			1,
		),
		"duplicate": strings.Replace(
			base,
			`"format":1`,
			`"format":1,"format":1`,
			1,
		),
		"case": strings.Replace(
			base,
			`"format":1`,
			`"Format":1`,
			1,
		),
		"unknown": strings.Replace(
			base,
			`"format":1`,
			`"format":1,"url":"https://invalid.test"`,
			1,
		),
		"null entries": strings.Replace(
			base,
			`"entries":[`,
			`"entries":null,"ignored":[`,
			1,
		),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeManifest([]byte(document)); err == nil {
				t.Fatal("unsupported manifest accepted")
			}
		})
	}
	maximum := base + strings.Repeat(" ", MaxManifestBytes-len(base))
	if _, err := DecodeManifest([]byte(maximum)); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeManifest([]byte(maximum + " ")); !errors.Is(err, ErrInvalid) {
		t.Fatal("oversized manifest accepted")
	}
}

func TestManifestRequiresBothArchitecturesAndBoundedCanonicalContent(t *testing.T) {
	_, source, _ := artifactFixture(t)
	for _, mutate := range []func(*Manifest){
		func(value *Manifest) { value.Entries = value.Entries[:1] },
		func(value *Manifest) { value.Entries[1] = value.Entries[0] },
		func(value *Manifest) { value.Entries[0], value.Entries[1] = value.Entries[1], value.Entries[0] },
		func(value *Manifest) { value.Entries[0].OS = "windows" },
		func(value *Manifest) { value.Entries[0].FileName = "../runner" },
		func(value *Manifest) { value.Entries[0].Size = 0 },
		func(value *Manifest) { value.Entries[0].Size = MaxArtifactBytes + 1 },
		func(value *Manifest) { value.Entries[0].SHA256 = strings.Repeat("A", 64) },
		func(value *Manifest) { value.SourceCommit = strings.Repeat("A", 40) },
		func(value *Manifest) { value.SourceCommit = strings.Repeat("0", 41) },
		func(value *Manifest) { value.Release = "../../another-release" },
		func(value *Manifest) { value.Release = strings.Repeat("x", 129) },
	} {
		manifest, err := DecodeManifest(source.documents[ManifestName])
		if err != nil {
			t.Fatal(err)
		}
		mutate(&manifest)
		if err := manifest.Validate(); err == nil {
			t.Fatal("invalid identity or artifact accepted")
		}
	}
	for _, size := range []int64{1, MaxArtifactBytes} {
		manifest, _ := DecodeManifest(source.documents[ManifestName])
		manifest.Entries[0].Size = size
		manifest.Release = strings.Repeat("x", 128)
		if err := manifest.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	manifest, _ := DecodeManifest(source.documents[ManifestName])
	manifest.SourceDirty = true
	document, _ := json.Marshal(manifest)
	decoded, err := DecodeManifest(document)
	if err != nil || !decoded.SourceDirty {
		t.Fatal("explicit dirty build provenance was lost")
	}
}
