package casecodec

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestLegacyV2CasePreservesPublishedIdentityRevisionAndEncoding(t *testing.T) {
	raw := legacyCaseFixture(t)
	legacy, err := DecodeFilesystemCase("legacy/case.json", raw)
	if err != nil {
		t.Fatal(err)
	}
	// Frozen from data/cases/openai-chat/T001-sync-response/case.json at 0ac85c6b.
	if legacy.Revision != 386638652410759 || legacy.Description != "" {
		t.Fatalf("legacy document changed semantic meaning: revision %d, description %q", legacy.Revision, legacy.Description)
	}
	encoded, err := EncodeFilesystemCase(legacy)
	if err != nil {
		t.Fatal(err)
	}
	before, err := canonicalJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	after, err := canonicalJSON(encoded)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("unchanged v2 case was rewritten to a different format: %s, %v", encoded, err)
	}
	digest, err := MaterializedSHA256(legacy)
	if err != nil || digest != "87215fa553888b87dc0978ec57bd1d7ea45034cc238a86a0ef3ff99b3d13ed1e" {
		t.Fatalf("legacy materialized digest changed: %s, %v", digest, err)
	}
}

func TestDescribedCaseRoundTripPreservesAuthoredContextAndIdentity(t *testing.T) {
	legacy, err := DecodeFilesystemCase("legacy/case.json", legacyCaseFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	described := legacy
	described.Description = "目的：验证响应。\n前提：已配置渠道。\n通过：断言满足。\n范围：一次请求。"
	encoded, err := EncodeFilesystemCase(described)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil || string(document["schema_version"]) != "3" {
		t.Fatalf("described case did not use its explicit format: %s, %v", encoded, err)
	}
	decoded, err := DecodeFilesystemCase("portable/different/case.json", encoded)
	if err != nil {
		t.Fatal(err)
	}
	identityPreserved := decoded.ID == legacy.ID && decoded.Key == legacy.Key
	contextPreserved := decoded.Description == described.Description && decoded.Revision != legacy.Revision
	if !identityPreserved || !contextPreserved {
		t.Fatalf("described case round trip lost identity or context: %#v", decoded)
	}
	for protocol, definition := range legacy.Definitions {
		before, _ := canonicalJSON(definition)
		after, _ := canonicalJSON(decoded.Definitions[protocol])
		if !bytes.Equal(before, after) {
			t.Fatalf("adding a description changed %s execution semantics", protocol)
		}
	}
	reencoded, err := EncodeFilesystemCase(decoded)
	if err != nil || !bytes.Equal(encoded, reencoded) {
		t.Fatalf("second save changed portable description: %v", err)
	}
	described.Description = ""
	withoutDescription, err := EncodeFilesystemCase(described)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := DecodeFilesystemCase("restored/case.json", withoutDescription)
	if err != nil || restored.Revision != legacy.Revision {
		t.Fatalf("removing the explanation did not restore the original v2 semantics: %#v, %v", restored, err)
	}
}

func TestFilesystemDescriptionBoundaryRejectsWrongVersionAndMalformedContext(t *testing.T) {
	for _, test := range []struct {
		name    string
		version int
		value   any
		omit    bool
	}{
		{name: "v2 extension", version: 2, value: "explanation"},
		{name: "v2 empty extension", version: 2, value: ""},
		{name: "v2 null extension", version: 2},
		{name: "v3 absent explanation", version: 3, omit: true},
		{name: "v3 null explanation", version: 3},
		{name: "v3 empty explanation", version: 3, value: ""},
		{name: "v3 blank explanation", version: 3, value: " \n "},
		{name: "v3 numeric explanation", version: 3, value: 7},
		{name: "unknown newer format", version: 4, value: "explanation"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(legacyCaseFixture(t), &document); err != nil {
				t.Fatal(err)
			}
			document["schema_version"] = test.version
			if !test.omit {
				document["description"] = test.value
			}
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte{}, raw...)
			if _, err := DecodeFilesystemCase("invalid/case.json", raw); err == nil {
				t.Fatalf("invalid description format accepted: %s", raw)
			}
			if !bytes.Equal(raw, before) {
				t.Fatal("rejected document was modified")
			}
		})
	}
}

func TestLegacyV2RejectsCaseInsensitiveDescriptionField(t *testing.T) {
	for _, name := range []string{"Description", "DESCRIPTION", "descriptioN"} {
		t.Run(name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(legacyCaseFixture(t), &document); err != nil {
				t.Fatal(err)
			}
			document[name] = "Only schema 3 owns this field."
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeFilesystemCase("invalid/case.json", raw); err == nil {
				t.Fatalf("schema 2 accepted new field alias %q", name)
			}
		})
	}
}

func legacyCaseFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/legacy-v2-case.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
