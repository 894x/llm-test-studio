package jobcontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/application/comparisons"
	"github.com/894x/llm-test-studio/internal/application/quicktest"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/domain"
)

const testReportID = "77777777-7777-4777-8777-777777777777"

func performanceSpec(t *testing.T) JobSpec {
	t.Helper()
	prepared, lease, code := quicktest.New(quicktest.Dependencies{}).BuildPerformance(
		context.Background(),
		quicktest.PerformanceCommand{
			Protocol: domain.ProtocolOpenAIChat, AddressMode: quicktest.AddressModeBaseURL,
			URL: "https://example.test", ModelID: "model", APIKey: "transport-only-test-secret",
			RequestCount: 1, Concurrency: 1, TimeoutMS: 1000, InputTokens: 10, OutputTokens: 3,
		},
	)
	if code != "" {
		t.Fatal(code)
	}
	_ = lease.Close()
	prepared.ReportID = testReportID
	return JobSpec{
		Contract: CurrentContract, JobID: "77777777-7777-4777-8777-777777777770",
		NodeInstallationID: "77777777-7777-4777-8777-777777777771",
		EngineBuildID:      strings.Repeat("a", 64), Target: ExecutionTarget{Kind: TargetLocal},
		Kind: KindPerformance, Spec: Payload{Performance: &prepared}, Assets: []Asset{},
	}
}

func TestSpecValidatesIdentityAndExclusivePayload(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*JobSpec)
		want   error
	}{
		{name: "valid local"},
		{name: "valid remote", change: func(spec *JobSpec) {
			spec.Target = ExecutionTarget{Kind: TargetRemote, NodeID: spec.JobID}
		}},
		{name: "omitted contract", change: func(spec *JobSpec) { spec.Contract = 0 }, want: ErrUnsupported},
		{name: "unknown contract", change: func(spec *JobSpec) { spec.Contract = 2 }, want: ErrUnsupported},
		{name: "omitted job id", change: func(spec *JobSpec) { spec.JobID = "" }, want: ErrInvalid},
		{name: "bad installation", change: func(spec *JobSpec) { spec.NodeInstallationID = "invalid" }, want: ErrInvalid},
		{name: "uppercase id", change: func(spec *JobSpec) {
			spec.JobID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaA"
		}, want: ErrInvalid},
		{name: "missing build", change: func(spec *JobSpec) { spec.EngineBuildID = "" }, want: ErrInvalid},
		{name: "short build", change: func(spec *JobSpec) { spec.EngineBuildID = strings.Repeat("a", 63) }, want: ErrInvalid},
		{name: "long build", change: func(spec *JobSpec) { spec.EngineBuildID = strings.Repeat("a", 65) }, want: ErrInvalid},
		{name: "uppercase build", change: func(spec *JobSpec) { spec.EngineBuildID = strings.Repeat("A", 64) }, want: ErrInvalid},
		{name: "nonhex build", change: func(spec *JobSpec) { spec.EngineBuildID = strings.Repeat("g", 64) }, want: ErrInvalid},
		{name: "missing target", change: func(spec *JobSpec) { spec.Target = ExecutionTarget{} }, want: ErrInvalid},
		{name: "unknown target", change: func(spec *JobSpec) { spec.Target.Kind = "automatic" }, want: ErrInvalid},
		{name: "remote without node", change: func(spec *JobSpec) { spec.Target.Kind = TargetRemote }, want: ErrInvalid},
		{name: "remote bad node", change: func(spec *JobSpec) {
			spec.Target = ExecutionTarget{Kind: TargetRemote, NodeID: "bad"}
		}, want: ErrInvalid},
		{name: "local with node", change: func(spec *JobSpec) { spec.Target.NodeID = spec.JobID }, want: ErrInvalid},
		{name: "missing kind", change: func(spec *JobSpec) { spec.Kind = "" }, want: ErrInvalid},
		{name: "wrong kind", change: func(spec *JobSpec) { spec.Kind = KindRun }, want: ErrInvalid},
		{name: "no payload", change: func(spec *JobSpec) { spec.Spec = Payload{} }, want: ErrInvalid},
		{name: "two payloads", change: func(spec *JobSpec) { spec.Spec.Run = &runs.PreparedRun{} }, want: ErrInvalid},
		{name: "three payloads", change: func(spec *JobSpec) {
			spec.Spec.Run = &runs.PreparedRun{}
			spec.Spec.Comparison = &comparisons.PreparedComparison{}
		}, want: ErrInvalid},
		{name: "unknown kind", change: func(spec *JobSpec) { spec.Kind = "unknown" }, want: ErrInvalid},
		{name: "bad report id", change: func(spec *JobSpec) {
			spec.Spec.Performance.ReportID = "bad"
		}, want: ErrInvalid},
		{name: "endpoint mismatch", change: func(spec *JobSpec) {
			spec.Spec.Performance.Endpoint = "https://another.test/v1/chat/completions"
		}, want: ErrInvalid},
		{name: "bad profile", change: func(spec *JobSpec) {
			spec.Spec.Performance.Profile.Concurrency = 0
		}, want: ErrInvalid},
		{name: "missing assets", change: func(spec *JobSpec) { spec.Assets = nil }, want: ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec := performanceSpec(t)
			if test.change != nil {
				test.change(&spec)
			}
			if err := spec.Validate(); !errors.Is(err, test.want) {
				t.Fatalf("Validate()=%v want %v", err, test.want)
			}
		})
	}
}

func testAssets(count int, size int64) []Asset {
	assets := make([]Asset, count)
	for index := range assets {
		assets[index] = Asset{
			Ref: fmt.Sprintf("asset-%d", index), SHA256: fmt.Sprintf("%064x", index),
			Size: size, MediaType: "application/octet-stream",
		}
	}
	return assets
}

func TestAssetManifestBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		assets []Asset
		change func([]Asset)
		valid  bool
	}{
		{name: "empty", assets: []Asset{}, valid: true},
		{name: "one byte", assets: testAssets(1, 1), valid: true},
		{name: "maximum file", assets: testAssets(1, MaxAssetBytes), valid: true},
		{name: "zero bytes", assets: testAssets(1, 0)},
		{name: "oversized file", assets: testAssets(1, MaxAssetBytes+1)},
		{name: "maximum total", assets: testAssets(4, MaxAssetBytes), valid: true},
		{name: "oversized total", assets: append(testAssets(4, MaxAssetBytes), testAssets(5, 1)[4])},
		{name: "maximum count", assets: testAssets(MaxAssets, 1), valid: true},
		{name: "oversized count", assets: testAssets(MaxAssets+1, 1)},
		{name: "empty ref", assets: testAssets(1, 1), change: func(assets []Asset) { assets[0].Ref = "" }},
		{name: "one byte ref", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].Ref = "x"
		}, valid: true},
		{name: "maximum ref", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].Ref = strings.Repeat("x", 80)
		}, valid: true},
		{name: "oversized ref", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].Ref = strings.Repeat("x", 81)
		}},
		{name: "path traversal", assets: testAssets(1, 1), change: func(assets []Asset) { assets[0].Ref = "../escape" }},
		{name: "duplicate ref", assets: testAssets(2, 1), change: func(assets []Asset) { assets[1].Ref = assets[0].Ref }},
		{name: "duplicate hash", assets: testAssets(2, 1), change: func(assets []Asset) {
			assets[1].SHA256 = assets[0].SHA256
		}},
		{name: "bad hash", assets: testAssets(1, 1), change: func(assets []Asset) { assets[0].SHA256 = "bad" }},
		{name: "empty media", assets: testAssets(1, 1), change: func(assets []Asset) { assets[0].MediaType = "" }},
		{name: "invalid media", assets: testAssets(1, 1), change: func(assets []Asset) { assets[0].MediaType = "wrong" }},
		{name: "media parameters", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].MediaType = "text/plain; charset=utf-8"
		}},
		{name: "uppercase media", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].MediaType = "IMAGE/PNG"
		}},
		{name: "maximum media length", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].MediaType = "application/" + strings.Repeat("a", 115)
		}, valid: true},
		{name: "oversized media length", assets: testAssets(1, 1), change: func(assets []Asset) {
			assets[0].MediaType = "application/" + strings.Repeat("a", 116)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.change != nil {
				test.change(test.assets)
			}
			err := validateAssets(test.assets)
			if (err == nil) != test.valid {
				t.Fatalf("validateAssets()=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestSubmissionRequiresExactBoundedSecretSlots(t *testing.T) {
	for _, test := range []struct {
		name    string
		secrets []Secret
		valid   bool
	}{
		{name: "valid", secrets: []Secret{{Slot: testReportID, Value: []byte("x")}}, valid: true},
		{name: "missing", secrets: []Secret{}},
		{name: "empty", secrets: []Secret{{Slot: testReportID, Value: []byte{}}}},
		{name: "blank", secrets: []Secret{{Slot: testReportID, Value: []byte(" \t\n")}}},
		{name: "maximum", secrets: []Secret{{Slot: testReportID, Value: bytes.Repeat([]byte("x"), MaxSecretBytes)}}, valid: true},
		{name: "oversized", secrets: []Secret{{Slot: testReportID, Value: bytes.Repeat([]byte("x"), MaxSecretBytes+1)}}},
		{name: "wrong slot", secrets: []Secret{{Slot: "unknown", Value: []byte("x")}}},
		{name: "extra slot", secrets: []Secret{
			{Slot: testReportID, Value: []byte("x")}, {Slot: "unknown", Value: []byte("x")},
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			submission := Submission{Job: performanceSpec(t), Secrets: test.secrets}
			err := submission.Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate()=%v valid=%v", err, test.valid)
			}
		})
	}
}

func TestSubmissionDecodeLimitsAndRejectsUnknownFields(t *testing.T) {
	submission := Submission{Job: performanceSpec(t), Secrets: []Secret{{Slot: testReportID, Value: []byte("test-secret")}}}
	raw, err := json.Marshal(submission)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		raw  []byte
		want error
	}{
		{name: "valid", raw: raw},
		{name: "maximum bytes", raw: append(bytes.Clone(raw), bytes.Repeat([]byte(" "), MaxSubmissionBytes-len(raw))...)},
		{name: "oversized", raw: bytes.Repeat([]byte(" "), MaxSubmissionBytes+1), want: ErrTooLarge},
		{name: "trailing document", raw: append(bytes.Clone(raw), []byte(`{}`)...), want: ErrInvalid},
		{name: "unknown field", raw: bytes.Replace(raw, []byte(`"job":`), []byte(`"unknown":true,"job":`), 1), want: ErrInvalid},
		{
			name: "wrong contract type", want: ErrInvalid,
			raw: bytes.Replace(raw, []byte(`"contract":1`), []byte(`"contract":"1"`), 1),
		},
		{
			name: "unknown contract", want: ErrUnsupported,
			raw: bytes.Replace(raw, []byte(`"contract":1`), []byte(`"contract":2`), 1),
		},
		{name: "invalid base64", raw: bytes.Replace(raw, []byte(`dGVzdC1zZWNyZXQ=`), []byte(`test-secret!`), 1), want: ErrInvalid},
		{name: "byte array is not base64", raw: bytes.Replace(raw,
			[]byte(`"value":"dGVzdC1zZWNyZXQ="`), []byte(`"value":[115,101,99,114,101,116]`), 1), want: ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			decoded, err := DecodeSubmission(bytes.NewReader(test.raw))
			defer decoded.Clear()
			if !errors.Is(err, test.want) {
				t.Fatalf("DecodeSubmission()=%v want %v", err, test.want)
			}
			if err != nil && strings.Contains(err.Error(), "test-secret") {
				t.Fatal("validation error leaked a secret")
			}
		})
	}
}

func TestIdentityFieldsRequireCanonicalUUIDsAndAssetHashes(t *testing.T) {
	for _, field := range []string{"job_id", "node_installation_id"} {
		invalidIDs := []string{
			"", "short", "zzzzzzzz-zzzz-4zzz-8zzz-zzzzzzzzzzzz", "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA",
		}
		for _, value := range invalidIDs {
			t.Run(field+"/"+value, func(t *testing.T) {
				spec := performanceSpec(t)
				if field == "job_id" {
					spec.JobID = value
				} else {
					spec.NodeInstallationID = value
				}
				if !errors.Is(spec.Validate(), ErrInvalid) {
					t.Fatal("noncanonical identity was accepted")
				}
			})
		}
	}
	invalidHashes := []string{
		strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), strings.Repeat("g", 64),
	}
	for _, value := range invalidHashes {
		assets := testAssets(1, 1)
		assets[0].SHA256 = value
		if !errors.Is(validateAssets(assets), ErrInvalid) {
			t.Fatal("noncanonical asset hash was accepted")
		}
	}
}

func TestSpecFreezeAndDigestOwnMutableInputAndNormalizeAssetOrder(t *testing.T) {
	spec := performanceSpec(t)
	spec.Assets = testAssets(2, 1)
	frozen, err := spec.Freeze()
	if err != nil {
		t.Fatal(err)
	}
	first, err := frozen.Digest()
	if err != nil {
		t.Fatal(err)
	}
	spec.Assets[0], spec.Assets[1] = spec.Assets[1], spec.Assets[0]
	second, err := spec.Digest()
	if err != nil || first != second {
		t.Fatalf("asset order changed digest: %v", err)
	}
	spec.Spec.Performance.ModelID = "changed"
	third, err := spec.Digest()
	if err != nil || first == third || frozen.Spec.Performance.ModelID != "model" {
		t.Fatalf("payload mutation changed frozen spec or did not change digest: %v", err)
	}
	submission := Submission{Job: spec, Secrets: []Secret{{Slot: testReportID, Value: []byte("private")}}}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		formatted := fmt.Sprintf(format, submission) + fmt.Sprintf(format, submission.Secrets[0])
		if strings.Contains(formatted, "private") || strings.Contains(formatted, "112, 114, 105") {
			t.Fatal("formatted submission exposed its live secret")
		}
	}
	secretBytes := submission.Secrets[0].Value
	submission.Clear()
	if len(submission.Secrets) != 0 || bytes.Count(secretBytes, []byte{0}) != len(secretBytes) {
		t.Fatal("transport secret copy was not cleared")
	}
}

func TestSpecDigestCanonicalizesNativeObjectsWithoutRoundingLargeIntegers(t *testing.T) {
	spec := performanceSpec(t)
	run := preparedRunFixture(t)
	spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
	definition := run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat]
	definition = bytes.Replace(definition, []byte(`"messages":`), []byte(`"seed":9007199254740993,"messages":`), 1)
	run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = definition
	first, err := spec.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reordered := bytes.Replace(definition, []byte(`"role":"user","content":"hi"`),
		[]byte(`"content":"hi","role":"user"`), 1)
	run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = reordered
	second, err := spec.Digest()
	if err != nil || first != second {
		t.Fatalf("native object key order changed digest: %v", err)
	}
	run.Snapshot.Entries[0].CaseDefinitions[0].Definitions[domain.ProtocolOpenAIChat] = bytes.Replace(
		reordered,
		[]byte("9007199254740993"),
		[]byte("9007199254740992"),
		1,
	)
	third, err := spec.Digest()
	if err != nil || first == third {
		t.Fatalf("distinct integers rounded to the same digest: %v", err)
	}
}

func TestRunAndWholeComparisonPayloadValidation(t *testing.T) {
	spec := performanceSpec(t)
	run := preparedRunFixture(t)
	spec.Kind, spec.Spec = KindRun, Payload{Run: &run}
	if _, err := spec.Freeze(); err != nil {
		t.Fatalf("current prepared run rejected: %v", err)
	}
	run.Meta.Revision = 2
	if !errors.Is(spec.Validate(), ErrInvalid) {
		t.Fatal("non-initial run revision was accepted")
	}
	for _, count := range []int{0, 1, 2, MaxSecrets, MaxSecrets + 1} {
		t.Run(fmt.Sprintf("comparison-%d", count), func(t *testing.T) {
			spec := comparisonSpec(t, count)
			valid := count >= 2 && count <= MaxSecrets
			if err := spec.Validate(); (err == nil) != valid {
				t.Fatalf("comparison validation: count=%d err=%v", count, err)
			}
			if !valid {
				return
			}
			submission := Submission{Job: spec, Secrets: make([]Secret, 0, count)}
			for _, slot := range spec.SecretSlots() {
				submission.Secrets = append(submission.Secrets, Secret{Slot: slot, Value: []byte("x")})
			}
			if err := submission.Validate(); err != nil {
				t.Fatal(err)
			}
			submission.Secrets[1].Slot = submission.Secrets[0].Slot
			if !errors.Is(submission.Validate(), ErrInvalid) {
				t.Fatal("duplicate secret slot was accepted")
			}
			spec.Spec.Comparison.Runs[1].Meta.ID = spec.Spec.Comparison.Runs[0].Meta.ID
			if !errors.Is(spec.Validate(), ErrInvalid) {
				t.Fatal("duplicate child identity was accepted")
			}
		})
	}
}
