package runnerinstall

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func uninstallRequestFixture() UninstallRequest {
	return UninstallRequest{
		Contract: jobcontract.CurrentContract, OperationID: "db2810f8-dc34-47ac-a2c0-f7823986dacf",
		NodeID: "eab57e15-3e5b-4714-8619-333db7042426", InstallationID: "0498de0e-ebbc-4419-b22b-e5001b6193c4",
		ExpectedBuildID: strings.Repeat("a", 64),
	}
}

func TestUninstallRequestIsBoundedAndExactCurrentContract(t *testing.T) {
	request := uninstallRequestFixture()
	raw, _ := json.Marshal(request)
	parsed, err := DecodeUninstallRequest(bytes.NewReader(raw))
	if err != nil || parsed != request {
		t.Fatal("current uninstall request was not accepted")
	}
	for _, invalid := range [][]byte{
		[]byte(`{}`), append(bytes.Clone(raw), []byte(`{}`)...),
		bytes.Replace(
			raw,
			[]byte(`"contract":1`),
			[]byte(`"contract":1,"contract":1`),
			1,
		),
		bytes.Replace(
			raw,
			[]byte(`"node_id":`),
			[]byte(`"Node_ID":`),
			1,
		),
		bytes.Replace(
			raw,
			[]byte(`"contract":1`),
			[]byte(`"contract":null`),
			1,
		),
		[]byte(strings.Repeat(" ", MaxRequestBytes+1)),
	} {
		if _, err := DecodeUninstallRequest(bytes.NewReader(invalid)); err == nil {
			t.Fatal("noncurrent or ambiguous uninstall request accepted")
		}
	}
	request.Contract++
	raw, _ = json.Marshal(request)
	if _, err := DecodeUninstallRequest(bytes.NewReader(raw)); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported uninstall contract was not explicit")
	}
}

func TestUninstallJournalFreezesDeletionManifestAndStages(t *testing.T) {
	request := uninstallRequestFixture()
	operation := UninstallOperation{
		Request: request, Stage: "checking", ChangedAt: time.Now().UTC(),
		Manifest: UninstallManifest{BuildIDs: []string{request.ExpectedBuildID},
			UnitSHA256: strings.Repeat("b", 64), OwnerSHA256: strings.Repeat("c", 64)},
	}
	raw, _ := json.Marshal(operation)
	if _, err := DecodeUninstallOperation(raw); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*UninstallOperation){
		func(op *UninstallOperation) { op.Stage = "removing" },
		func(op *UninstallOperation) { op.PauseConfirmed = true },
		func(op *UninstallOperation) {
			op.Manifest.BuildIDs = append(op.Manifest.BuildIDs, op.Manifest.BuildIDs[0])
		},
		func(op *UninstallOperation) { op.Manifest.BuildIDs = []string{strings.Repeat("d", 64)} },
		func(op *UninstallOperation) { op.Stage = "completed"; op.PauseConfirmed = true },
	} {
		var invalid UninstallOperation
		_ = json.Unmarshal(raw, &invalid)
		change(&invalid)
		document, _ := json.Marshal(invalid)
		if _, err := DecodeUninstallOperation(document); err == nil {
			t.Fatal("invalid persisted uninstall state accepted")
		}
	}
	if _, err := DecodeUninstallOperation(append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)); err == nil {
		t.Fatal("unknown persisted field accepted")
	}
}
