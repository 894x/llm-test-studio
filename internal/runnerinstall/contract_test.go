package runnerinstall

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
)

func installationRequest() Request {
	return Request{
		Contract: jobcontract.CurrentContract, OperationID: "55555555-5555-4555-8555-555555555555",
		ArtifactSHA256: strings.Repeat("b", 64), ListenPort: 44917,
	}
}

func TestProvisionRequestIsBoundedAndAcceptsOnlyCurrentExactFields(t *testing.T) {
	request := installationRequest()
	document, _ := json.Marshal(request)
	exact := string(document) + strings.Repeat(" ", MaxRequestBytes-len(document))
	if decoded, err := DecodeRequest(strings.NewReader(exact)); err != nil || decoded != request {
		t.Fatal("current request at exact byte boundary was rejected")
	}
	for _, invalid := range []string{
		exact + " ", string(document) + " {}", `null`, `{}`,
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
			`"contract":1`,
			`"contract":1,"extra":1`,
			1,
		),
		strings.Replace(
			string(document),
			`"listen_port":44917`,
			`"listen_port":null`,
			1,
		),
		strings.Replace(
			string(document),
			`"listen_port":44917`,
			`"listen_port":1023`,
			1,
		),
		strings.Replace(
			string(document),
			request.OperationID,
			"invalid",
			1,
		),
	} {
		if _, err := DecodeRequest(strings.NewReader(invalid)); err == nil {
			t.Fatal("invalid installation input was accepted")
		}
	}
	request.Contract++
	document, _ = json.Marshal(request)
	if _, err := DecodeRequest(strings.NewReader(string(document))); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unsupported installation contract was silently accepted")
	}
}

func TestOperationRejectsNoncanonicalAndUnsupportedStoredRecords(t *testing.T) {
	operation := Operation{Request: installationRequest(), Stage: "checking", ChangedAt: time.Now().UTC()}
	document, _ := json.Marshal(operation)
	if _, err := DecodeOperation(document); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		string(document) + " ",
		strings.Replace(
			string(document),
			`"result":null`,
			`"Result":null`,
			1,
		),
		strings.Replace(
			string(document),
			`"result":null`,
			`"result":null,"result":null`,
			1,
		),
		strings.Replace(
			string(document),
			`"stage":"checking"`,
			`"stage":"unknown"`,
			1,
		),
		strings.Replace(
			string(document),
			`"stage":"checking"`,
			`"stage":"completed"`,
			1,
		),
	} {
		if _, err := DecodeOperation([]byte(invalid)); err == nil {
			t.Fatal("stored operation inferred a superseded or incomplete shape")
		}
	}
	operation.Request.Contract++
	document, _ = json.Marshal(operation)
	if _, err := DecodeOperation(document); !errors.Is(err, jobcontract.ErrUnsupported) {
		t.Fatal("unknown stored contract did not require an explicit upgrade")
	}
}
