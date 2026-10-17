package bridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestDesktopUpgradeProjectionKeepsConfigurationAndRunnerIdentitySeparate(t *testing.T) {
	at := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	request := runnerinstall.UpgradeRequest{Contract: jobcontract.CurrentContract,
		OperationID: "82cff965-84de-4fe5-832a-199c60d5d046", NodeID: "ea60ca09-e2db-467d-9df1-1e5597607b2c",
		InstallationID: "bde4217e-9f1f-4258-a835-82294e4bceaa", ExpectedBuildID: strings.Repeat("a", 64),
		ArtifactSHA256: strings.Repeat("b", 64)}
	configurationID := "5493d7b4-ad3d-4df8-8abb-66d2e0735dd7"
	attempt := &remotenodes.Upgrade{Request: request, Release: "v-unit", Arch: "amd64",
		TemporaryDirectory: remotessh.UpgradeDirectory(request.OperationID), Stage: "uploading", ChangedAt: at}
	view := projectRemoteUpgrade(configurationID, attempt, nil)
	if view.ConfigurationID != configurationID || view.NodeID != request.NodeID || view.Confirmed || view.ActualRunner != nil {
		t.Fatal("local acceptance was confused with a confirmed server receipt")
	}
	document, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(document, &fields) != nil || len(fields) != 13 {
		t.Fatal("upgrade view is not the current thirteen-field contract")
	}
	for _, private := range []string{"request", "token", "temporary_directory", "release", "artifact_sha256"} {
		if _, present := fields[private]; present {
			t.Fatal("private intent fields crossed the binding")
		}
	}
	operation := &runnerinstall.UpgradeOperation{Request: request, Stage: "resume_attempted", ChangedAt: at,
		ResumeAttempted: true, Restorable: false}
	attempt.Operation, attempt.Stage = operation, operation.Stage
	view = projectRemoteUpgrade(configurationID, attempt, nil)
	if !view.Confirmed || !view.ResumeAttempted || view.Restorable {
		t.Fatal("irreversible resume facts were not projected from the server journal")
	}
	if projectRemoteUpgrade(configurationID, nil, nil) != nil {
		t.Fatal("no operation must project as null")
	}
	foreign := &jobcontract.RunnerInfo{NodeID: "39917fdf-74d2-4236-ab13-2b5c6d933f6a",
		NodeInstallationID: request.InstallationID}
	if projectRemoteUpgrade(configurationID, attempt, foreign).ActualRunner != nil {
		t.Fatal("an actual runner with another identity was exposed")
	}
}

func TestDesktopUpgradeBindingsUseCurrentLifecycleAndSafeIdentityCode(t *testing.T) {
	app := New(func(context.Context) (Dependencies, error) { return Dependencies{}, nil })
	if _, err := app.GetRemoteNodeUpgrade("bad"); err == nil || err.Error() != desktopCodeNotStarted {
		t.Fatal("upgrade query bypassed startup")
	}
	app.onStartup(t.Context())
	defer app.shutdown()
	if _, err := app.StartRemoteNodeUpgrade(RemoteNodeUpgradeCommand{}); err == nil || err.Error() != desktopCodeRemoteNodesMissing {
		t.Fatal("upgrade start bypassed the current dependency lease")
	}
	if err := app.safeBindingError(remotenodes.ErrUpgradeIdentity); err.Error() != "remote_node_identity_conflict" {
		t.Fatal("upgrade identity conflict lost its safe fixed meaning")
	}
}
