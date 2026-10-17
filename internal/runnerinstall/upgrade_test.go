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

func upgradeRequestFixture() UpgradeRequest {
	return UpgradeRequest{
		Contract: jobcontract.CurrentContract, OperationID: "28ecf9e1-c3e3-4646-a6df-0e104ec7fd49",
		NodeID: "400eacb8-dbb3-47d7-8229-f00d3cc9ff9c", InstallationID: "17627262-ccbe-4dfd-a2b5-be2fab3feee0",
		ExpectedBuildID: strings.Repeat("a", 64), ArtifactSHA256: strings.Repeat("b", 64),
	}
}

func TestUpgradeRequestUsesOnlyExactCurrentNonSecretFields(t *testing.T) {
	request := upgradeRequestFixture()
	document, _ := json.Marshal(request)
	decoded, err := DecodeUpgradeRequest(bytes.NewReader(document))
	if err != nil || decoded != request {
		t.Fatal("current request rejected")
	}
	for _, invalid := range []string{
		`{}`, string(document) + `{}`, strings.Replace(string(document), `"expected_build_id"`, `"ExpectedBuildID"`, 1),
		strings.Replace(string(document), `"contract":`, `"contract":1,"contract":`, 1),
		strings.Replace(string(document), `"contract":1`, `"contract":null`, 1),
		strings.TrimSuffix(string(document), "}") + `,"password":"secret"}`,
		strings.TrimSuffix(string(document), "}") + `,"token":"secret"}`,
	} {
		if _, err := DecodeUpgradeRequest(strings.NewReader(invalid)); err == nil {
			t.Fatalf("invalid request accepted: %s", invalid)
		}
	}
	request.Contract++
	document, _ = json.Marshal(request)
	if _, err := DecodeUpgradeRequest(bytes.NewReader(document)); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported contract accepted")
	}
	if _, err := DecodeUpgradeRequest(strings.NewReader(strings.Repeat(" ", MaxRequestBytes+1))); err == nil {
		t.Fatal("unbounded input accepted")
	}
}

func TestUpgradeJournalRejectsHiddenResumeAndRestorePaths(t *testing.T) {
	operation := UpgradeOperation{Request: upgradeRequestFixture(), Stage: "checking", ChangedAt: time.Now().UTC()}
	document, _ := json.Marshal(operation)
	if _, err := DecodeUpgradeOperation(document); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*UpgradeOperation){
		func(op *UpgradeOperation) { op.Stage = "legacy" },
		func(op *UpgradeOperation) { op.Stage = "completed" },
		func(op *UpgradeOperation) { op.ResumeAttempted = true; op.Restorable = true },
		func(op *UpgradeOperation) { op.ResumeAttempted = true },
		func(op *UpgradeOperation) { op.Restorable = true },
		func(op *UpgradeOperation) { op.ErrorCode = "secret dynamic message" },
	} {
		invalid := operation
		mutate(&invalid)
		document, _ := json.Marshal(invalid)
		if _, err := DecodeUpgradeOperation(document); err == nil {
			t.Fatal("invalid upgrade state accepted")
		}
	}
	if _, err := DecodeUpgradeOperation(append(document, ' ')); err == nil {
		t.Fatal("noncanonical journal accepted")
	}
}
