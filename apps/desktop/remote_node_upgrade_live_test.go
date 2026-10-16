package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

const remoteUpgradeLiveEnable = "LLM_TEST_LIVE_REMOTE_UPGRADE"

type remoteUpgradeLiveInput struct {
	Action          string             `json:"action"`
	Endpoint        remotessh.Endpoint `json:"endpoint"`
	Password        []byte             `json:"password"`
	WorkspaceRoot   string             `json:"workspace_root"`
	ArtifactRoot    string             `json:"artifact_root"`
	ConfigurationID string             `json:"configuration_id"`
	OperationID     string             `json:"operation_id"`
	NodeID          string             `json:"node_id"`
	InstallationID  string             `json:"installation_id"`
	OldVersion      string             `json:"old_version"`
	OldBuild        string             `json:"old_build"`
	TargetVersion   string             `json:"target_version"`
	TargetBuild     string             `json:"target_build"`
}

func (remoteUpgradeLiveInput) String() string   { return "remoteUpgradeLiveInput{redacted}" }
func (remoteUpgradeLiveInput) GoString() string { return "remoteUpgradeLiveInput{redacted}" }
func (remoteUpgradeLiveInput) MarshalJSON() ([]byte, error) {
	return nil, errors.New("live credentials cannot be serialized")
}

// This projection contains only checked identities, release facts and fixed codes.
type remoteUpgradeLiveEvent struct {
	Stage                     string                 `json:"stage"`
	Reason                    string                 `json:"reason"`
	ConfigurationID           string                 `json:"configuration_id"`
	OperationID               string                 `json:"operation_id"`
	StartCalls                uint32                 `json:"start_calls"`
	RetryCalls                uint32                 `json:"retry_calls"`
	ProvisionCalls            uint32                 `json:"provision_calls"`
	BaselineEligible          bool                   `json:"baseline_eligible"`
	BaselineIdle              bool                   `json:"baseline_idle"`
	CheckPreservedOldBuild    bool                   `json:"check_preserved_old_build"`
	QueryPreservedReceipt     bool                   `json:"query_preserved_receipt"`
	FinalCheckPreservedTarget bool                   `json:"final_check_preserved_target"`
	Upgrade                   *RemoteNodeUpgradeView `json:"upgrade"`
}

func decodeRemoteUpgradeLiveInput(reader io.Reader) (remoteUpgradeLiveInput, error) {
	document, err := io.ReadAll(io.LimitReader(reader, (64<<10)+1))
	defer clear(document)
	if err != nil || len(document) > 64<<10 {
		return remoteUpgradeLiveInput{}, errors.New("input_invalid")
	}
	var input remoteUpgradeLiveInput
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoded := decoder.Decode(&input) == nil && decoder.Decode(&struct{}{}) == io.EOF
	validAction := input.Action == "start" || input.Action == "retry"
	validIDs := domain.IsUUID(input.ConfigurationID) && domain.IsUUID(input.OperationID) &&
		domain.IsUUID(input.NodeID) && domain.IsUUID(input.InstallationID)
	validPin := input.Endpoint.Validate() == nil && input.Endpoint.Fingerprint != "" && input.Endpoint.HostKeyType != ""
	validBuild := jobcontract.IsSHA256(input.OldBuild) && jobcontract.IsSHA256(input.TargetBuild) && input.OldBuild != input.TargetBuild
	validVersions := runnerartifacts.ValidRelease(input.OldVersion) && runnerartifacts.ValidRelease(input.TargetVersion)
	validRoots := filepath.IsAbs(input.WorkspaceRoot) && filepath.Clean(input.WorkspaceRoot) == input.WorkspaceRoot &&
		filepath.IsAbs(input.ArtifactRoot) && filepath.Clean(input.ArtifactRoot) == input.ArtifactRoot
	validPassword := len(input.Password) > 0 && len(input.Password) <= 8192
	if !decoded || !validAction || !validIDs || !validPin || !validBuild || !validVersions || !validRoots || !validPassword {
		clear(input.Password)
		return remoteUpgradeLiveInput{}, errors.New("input_invalid")
	}
	return input, nil
}

func TestLiveRemoteNodeUpgradeProductFlow(t *testing.T) {
	if os.Getenv(remoteUpgradeLiveEnable) != "1" {
		t.Skip("explicit product upgrade only; bounded transient stdin credentials")
	}
	result := remoteUpgradeLiveEvent{Stage: "failed", Reason: "input_invalid"}
	var provisionCalls atomic.Uint32
	defer func() {
		result.ProvisionCalls = provisionCalls.Load()
		_ = json.NewEncoder(os.Stdout).Encode(result)
		code := 0
		if t.Failed() || result.Stage != "completed" {
			code = 1
		}
		os.Exit(code)
	}()
	input, err := decodeRemoteUpgradeLiveInput(os.Stdin)
	if err != nil {
		t.Fatal("invalid bounded upgrade configuration")
	}
	defer clear(input.Password)
	result.ConfigurationID, result.OperationID = input.ConfigurationID, input.OperationID
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	result.Reason = "startup_failed"
	configurationRoot := filepath.Join(input.WorkspaceRoot, "configuration")
	catalogRoot := filepath.Join(input.WorkspaceRoot, "catalog")
	if input.Action == "start" {
		entries, readErr := os.ReadDir(input.WorkspaceRoot)
		if readErr != nil || len(entries) != 0 {
			t.Fatal("upgrade start requires a retained empty workspace")
		}
		for _, root := range []string{configurationRoot, catalogRoot} {
			if os.Mkdir(root, 0o700) != nil {
				t.Fatal("isolated workspace creation failed")
			}
		}
	}
	source, err := runnerartifacts.NewDirectorySource(input.ArtifactRoot)
	if err != nil {
		t.Fatal("target artifact directory is invalid")
	}
	verifyRemoteUpgradeLiveArtifact(t, ctx, source, input)
	store := credentials.NewMemoryStore()
	if input.Action != "start" {
		hydrateRemoteUpgradeSSH(t, ctx, input, configurationRoot, store, false)
	}
	options := productionOptions{
		userConfigDir:  func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) { return filepath.Join(catalogRoot, "upgrade-live.exe"), nil },
		appVersion:     "upgrade-live-distinct-client", credentialStore: store,
		runnerSource: source, runnerRelease: input.TargetVersion,
		remoteConnector: func(
			ctx context.Context,
			endpoint remotessh.Endpoint,
			authentication remotessh.Authentication,
		) (remotenodes.RemoteSession, error) {
			session, connectErr := remotenodes.ConnectSSH(ctx, endpoint, authentication)
			if connectErr != nil {
				return nil, connectErr
			}
			return &remoteUpgradeLiveSession{RemoteSession: session, provisionCalls: &provisionCalls}, nil
		},
	}
	app := startLiveK3App(t, ctx, options)
	defer func() { _ = app.shutdown() }()
	if input.Action == "start" {
		result.Reason = "pairing_failed"
		pairRemoteUpgradeLiveNode(t, ctx, app, input)
		result.Reason = "baseline_invalid"
		node := remoteUpgradeLiveNode(t, app, input.ConfigurationID)
		assertRemoteUpgradeLiveRunner(t, node.Runner, input, false)
		result.BaselineEligible = node.Eligible
		result.BaselineIdle = node.Runner.Ready && node.Runner.Healthy && node.Runner.Pending == 0 &&
			node.Runner.Active == 0 && node.Runner.Queued == 0
		if !result.BaselineEligible || !result.BaselineIdle || node.Upgrade != nil {
			t.Fatal("runner must be idle and eligible before explicit upgrade")
		}
		checkedBefore := remoteUpgradeLiveCheckedAt(node)
		if app.RefreshRemoteNode(RemoteNodeAction{ID: node.ID, ExpectedRevision: node.Revision}) != nil {
			t.Fatal("explicit baseline Check failed")
		}
		node = waitRemoteUpgradeChecked(t, ctx, app, input, false, checkedBefore)
		result.CheckPreservedOldBuild = node.Eligible && node.Runner.BuildID == input.OldBuild && node.Upgrade == nil
		if !result.CheckPreservedOldBuild {
			t.Fatal("Check altered the physical runner or lost eligibility")
		}
		if provisionCalls.Load() != 0 {
			t.Fatal("Check attempted forbidden initial provisioning")
		}
		result.Stage, result.Reason = "before_start", ""
		emitRemoteUpgradeLive(result)
		// Exactly one initial Start. Ambiguous delivery never creates a new ID.
		result.StartCalls = 1
		result.Upgrade, err = app.StartRemoteNodeUpgrade(RemoteNodeUpgradeCommand{
			ID: node.ID, ExpectedRevision: node.Revision, OperationID: input.OperationID, ExpectedBuildID: input.OldBuild,
		})
		if err != nil {
			result.Reason = "start_unconfirmed"
		}
	} else {
		result.Reason = "intent_invalid"
		view, readErr := app.GetRemoteNodeUpgrade(input.ConfigurationID)
		if readErr != nil || !remoteUpgradeLiveIdentity(view, input) {
			t.Fatal("original durable intent is unavailable or changed")
		}
		result.Upgrade = view
		if input.Action == "retry" {
			node := remoteUpgradeLiveNode(t, app, input.ConfigurationID)
			result.RetryCalls = 1
			result.Upgrade, err = app.RetryRemoteNodeUpgrade(RemoteNodeUpgradeAction{
				ID: node.ID, ExpectedRevision: node.Revision, OperationID: input.OperationID,
			})
			if err != nil {
				result.Reason = "retry_unconfirmed"
			}
		}
	}
	result.Stage = "observing"
	emitRemoteUpgradeLive(result)
	last := ""
	for {
		view, readErr := app.GetRemoteNodeUpgrade(input.ConfigurationID)
		if readErr == nil && remoteUpgradeLiveIdentity(view, input) {
			result.Upgrade = view
			key := view.Stage + "/" + view.ErrorCode + "/" + view.ChangedAt
			if key != last {
				emitRemoteUpgradeLive(result)
				last = key
			}
			if view.Stage == "rolled_back" {
				result.Reason = "operation_rolled_back"
				t.Fatal("explicit upgrade did not reach target")
			}
			if view.Stage == "completed" && view.Confirmed {
				break
			}
		}
		if ctx.Err() != nil {
			result.Reason = "observation_expired_same_operation_only"
			t.Fatal("original operation requires read-only inspection or explicit same-operation retry")
		}
		liveK3Pause(t, ctx, time.Second)
	}
	result.Reason = "receipt_invalid"
	view := result.Upgrade
	if view.ErrorCode != "" || !view.ResumeAttempted || view.Restorable {
		t.Fatal("completed journal lacks the irreversible resume receipt")
	}
	assertRemoteUpgradeLiveRunner(t, view.ActualRunner, input, true)
	if input.Action != "start" {
		hydrateRemoteUpgradeSSH(t, ctx, input, configurationRoot, store, true)
	}
	first, firstErr := app.GetRemoteNodeUpgrade(input.ConfigurationID)
	second, secondErr := app.GetRemoteNodeUpgrade(input.ConfigurationID)
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	result.QueryPreservedReceipt = firstErr == nil && secondErr == nil && bytes.Equal(firstJSON, secondJSON)
	if !result.QueryPreservedReceipt {
		t.Fatal("same-operation read changed its confirmed receipt")
	}
	node := remoteUpgradeLiveNode(t, app, input.ConfigurationID)
	if !remoteUpgradeLiveIdentity(node.Upgrade, input) || node.Upgrade.Stage != "completed" || !node.Upgrade.Confirmed {
		t.Fatal("GetRemoteNodes lost the mandatory upgrade projection")
	}
	checkedBefore := remoteUpgradeLiveCheckedAt(node)
	if app.RefreshRemoteNode(RemoteNodeAction{ID: node.ID, ExpectedRevision: node.Revision}) != nil {
		t.Fatal("final explicit Check failed")
	}
	node = waitRemoteUpgradeChecked(t, ctx, app, input, true, checkedBefore)
	result.FinalCheckPreservedTarget = node.Eligible && remoteUpgradeLiveIdentity(node.Upgrade, input) &&
		node.Upgrade.Stage == "completed" && node.Upgrade.Confirmed
	if !result.FinalCheckPreservedTarget {
		t.Fatal("Check changed the confirmed target or operation")
	}
	if provisionCalls.Load() != 0 {
		t.Fatal("product acceptance attempted forbidden initial provisioning")
	}
	result.Stage, result.Reason = "completed", ""
}

// An unexpected missing installation must fail without allowing Check to
// bootstrap another runner. Upgrade/Retry and all reads delegate unchanged.
type remoteUpgradeLiveSession struct {
	remotenodes.RemoteSession
	provisionCalls *atomic.Uint32
}

func (session *remoteUpgradeLiveSession) Provision(
	context.Context,
	runnerinstall.Request,
	remotessh.Artifact,
	func(runnerinstall.Progress),
) (runnerinstall.Result, error) {
	session.provisionCalls.Add(1)
	return runnerinstall.Result{}, runnerinstall.ErrConflict
}

func emitRemoteUpgradeLive(event remoteUpgradeLiveEvent) {
	_ = json.NewEncoder(os.Stdout).Encode(event)
}

// Resolve the approved bytes before pairing, authentication, or any mutation.
// The production cache and SSH upload independently verify the same bytes.
func verifyRemoteUpgradeLiveArtifact(
	t *testing.T,
	ctx context.Context,
	source runnerartifacts.Source,
	input remoteUpgradeLiveInput,
) {
	t.Helper()
	file, err := source.Fetch(ctx, input.TargetVersion, runnerartifacts.ManifestName)
	if err != nil {
		t.Fatal("approved target manifest unavailable")
	}
	document, err := io.ReadAll(io.LimitReader(file, runnerartifacts.MaxManifestBytes+1))
	_ = file.Close()
	if err != nil || len(document) > runnerartifacts.MaxManifestBytes {
		t.Fatal("approved target manifest invalid")
	}
	manifest, err := runnerartifacts.DecodeManifest(document)
	if err != nil || manifest.Release != input.TargetVersion || manifest.SourceDirty {
		t.Fatal("approved clean target manifest differs")
	}
	entry := manifest.Entries[0]
	if entry.SHA256 != input.TargetBuild || entry.OS != "linux" || entry.Arch != "amd64" {
		t.Fatal("approved target artifact identity differs")
	}
	artifact, err := source.Fetch(ctx, input.TargetVersion, entry.FileName)
	if err != nil {
		t.Fatal("approved target artifact unavailable")
	}
	defer artifact.Close()
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(artifact, runnerartifacts.MaxArtifactBytes+1))
	if err != nil || size != entry.Size || hex.EncodeToString(digest.Sum(nil)) != input.TargetBuild {
		t.Fatal("approved target artifact bytes differ")
	}
}

func remoteUpgradeLiveIdentity(view *RemoteNodeUpgradeView, input remoteUpgradeLiveInput) bool {
	return view != nil && view.ConfigurationID == input.ConfigurationID && view.NodeID == input.NodeID &&
		view.InstallationID == input.InstallationID && view.OperationID == input.OperationID &&
		view.ExpectedBuildID == input.OldBuild && view.TargetBuildID == input.TargetBuild
}

func remoteUpgradeLiveNode(t *testing.T, app *DesktopApp, id string) RemoteNodeSummary {
	t.Helper()
	snapshot, err := app.GetRemoteNodes()
	if err == nil {
		for _, node := range snapshot.Nodes {
			if node.ID == id {
				return node
			}
		}
	}
	t.Fatal("isolated node configuration is unavailable")
	return RemoteNodeSummary{}
}

func assertRemoteUpgradeLiveRunner(t *testing.T, runner *RemoteRunnerSummary, input remoteUpgradeLiveInput, target bool) {
	t.Helper()
	version, build := input.OldVersion, input.OldBuild
	if target {
		version, build = input.TargetVersion, input.TargetBuild
	}
	if runner == nil || runner.NodeID != input.NodeID || runner.InstallationID != input.InstallationID ||
		runner.BuildID != build || runner.Version != version || runner.Version == "upgrade-live-distinct-client" ||
		runner.OS != "linux" || runner.Arch != "amd64" || !runner.Ready || !runner.Healthy || runner.Pending != 0 {
		t.Fatal("physical runner identity, release, readiness or idle state is invalid")
	}
}

func pairRemoteUpgradeLiveNode(t *testing.T, ctx context.Context, app *DesktopApp, input remoteUpgradeLiveInput) {
	t.Helper()
	if app.SaveRemoteNode(RemoteNodeDraft{
		ID: input.ConfigurationID, Name: "product upgrade acceptance", Region: "validation",
		Host: input.Endpoint.Host, Port: input.Endpoint.Port, User: input.Endpoint.User, Enabled: true,
		Authentication: remotenodes.AuthenticationPassword, Password: string(input.Password),
	}) != nil {
		t.Fatal("isolated node save failed")
	}
	confirmed := false
	for {
		node := remoteUpgradeLiveNode(t, app, input.ConfigurationID)
		if node.InstallationStage != "" || node.Upgrade != nil {
			t.Fatal("pairing unexpectedly installed or upgraded runner")
		}
		if !confirmed && node.ProposedHostKey != nil {
			key := *node.ProposedHostKey
			if key.Type != input.Endpoint.HostKeyType || key.Fingerprint != input.Endpoint.Fingerprint {
				t.Fatal("exact SSH host pin differs")
			}
			if app.ConfirmRemoteNodeHostKey(RemoteNodeHostKeyConfirmation{
				ID: node.ID, ExpectedRevision: node.Revision, Key: key,
			}) != nil {
				t.Fatal("exact SSH host pin confirmation failed")
			}
			confirmed = true
		}
		if node.Eligible {
			if node.HostKey == nil || node.HostKey.Type != input.Endpoint.HostKeyType || node.HostKey.Fingerprint != input.Endpoint.Fingerprint {
				t.Fatal("eligible node lost exact SSH pin")
			}
			assertRemoteUpgradeLiveRunner(t, node.Runner, input, false)
			return
		}
		liveK3Pause(t, ctx, time.Second)
	}
}

func remoteUpgradeLiveCheckedAt(node RemoteNodeSummary) string {
	if node.CheckedAt == nil {
		return ""
	}
	return *node.CheckedAt
}

func waitRemoteUpgradeChecked(
	t *testing.T,
	ctx context.Context,
	app *DesktopApp,
	input remoteUpgradeLiveInput,
	target bool,
	checkedBefore string,
) RemoteNodeSummary {
	t.Helper()
	for {
		node := remoteUpgradeLiveNode(t, app, input.ConfigurationID)
		version, build := input.OldVersion, input.OldBuild
		if target {
			version, build = input.TargetVersion, input.TargetBuild
		}
		fresh := remoteUpgradeLiveCheckedAt(node) != checkedBefore
		matches := node.Runner != nil && node.Runner.Version == version && node.Runner.BuildID == build
		if fresh && !node.Checking && matches {
			assertRemoteUpgradeLiveRunner(t, node.Runner, input, target)
			return node
		}
		liveK3Pause(t, ctx, time.Second)
	}
}

// A separate explicitly selected retry process reconstructs only transient
// credentials from supplied password and the existing protected shared pairing.
// It preserves original local configuration, binding, token refs and operation.
func hydrateRemoteUpgradeSSH(
	t *testing.T,
	ctx context.Context,
	input remoteUpgradeLiveInput,
	root string,
	memory credentials.Store,
	includeToken bool,
) {
	t.Helper()
	directory, _, err := productionStoragePaths(root)
	if err != nil {
		t.Fatal("retained workspace path invalid")
	}
	directory = filepath.Join(directory, "remote-nodes")
	files, err := remotenodes.NewFileStore(filepath.Join(directory, "nodes.json"))
	if err != nil {
		t.Fatal("original node configuration unavailable")
	}
	config, err := files.Read(ctx)
	if err != nil || len(config.Nodes) != 1 {
		t.Fatal("retained node configuration invalid")
	}
	node := config.Nodes[0]
	validIdentity := node.ID == input.ConfigurationID && node.Binding != nil && node.HostKey != nil &&
		node.Binding.Identity.NodeID == input.NodeID && node.Binding.Identity.InstallationID == input.InstallationID
	if !validIdentity || node.Endpoint() != input.Endpoint {
		t.Fatal("retained physical node or exact SSH pin changed")
	}
	scope, err := credentialScopeForRoot(directory)
	if err != nil {
		t.Fatal("retained credential scope unavailable")
	}
	scoped, err := credentials.NewScopedStore(memory, scope)
	if err != nil {
		t.Fatal("retained credential scope invalid")
	}
	passwordRef, err := credentials.ParseStoreRef(node.Authentication.PasswordRef)
	if err != nil {
		t.Fatal("original password reference invalid")
	}
	if scoped.Test(ctx, passwordRef) != nil && scoped.Set(ctx, passwordRef, input.Password) != nil {
		t.Fatal("transient password restoration failed")
	}
	if !includeToken {
		return
	}
	lease, err := credentials.NewTemporaryLease(input.Password)
	if err != nil {
		t.Fatal("transient password lease failed")
	}
	defer lease.Close()
	client, err := remotessh.Dial(ctx, input.Endpoint, remotessh.PasswordAuthentication(lease))
	if err != nil {
		t.Fatal("pinned read-only pairing connection failed")
	}
	defer client.Close()
	physical, _, err := client.ConnectNode(ctx)
	if err != nil {
		t.Fatal("original shared pairing unavailable")
	}
	defer physical.Close()
	if physical.Identity() != node.Binding.Identity {
		t.Fatal("original shared pairing identity changed")
	}
	token, err := physical.PairingLease()
	if err != nil {
		t.Fatal("transient shared pairing lease failed")
	}
	defer token.Close()
	value, err := token.Bytes()
	if err != nil {
		t.Fatal("transient pairing bytes unavailable")
	}
	defer clear(value)
	tokenRef, err := credentials.ParseStoreRef(node.Binding.TokenRef)
	if err != nil || scoped.Set(ctx, tokenRef, value) != nil {
		t.Fatal("transient shared pairing restoration failed")
	}
}

func TestRemoteUpgradeLiveCredentialEnvelopeCannotSerialize(t *testing.T) {
	input := remoteUpgradeLiveInput{Password: []byte("synthetic-private")}
	if _, err := json.Marshal(input); err == nil {
		t.Fatal("private input serialization was allowed")
	}
	if strings.Contains(input.String(), "synthetic-private") || strings.Contains(input.GoString(), "synthetic-private") {
		t.Fatal("private input formatting was allowed")
	}
}
