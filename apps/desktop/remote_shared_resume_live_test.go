package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/894x/llm-test-studio/internal/application/jobs"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/remotesync"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/domain"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	remotessh "github.com/894x/llm-test-studio/internal/remote/ssh"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

const sharedResumeLiveEnable = "LLM_TEST_LIVE_REMOTE_SHARED_RESUME"

// This role reopens one retained workspace. It has no launch or control input.
// Secrets arrive once through bounded anonymous stdin and stay in MemoryStore.
type sharedResumeLiveInput struct {
	Endpoint             remotessh.Endpoint `json:"endpoint"`
	Password             []byte             `json:"password"`
	NodeToken            []byte             `json:"node_token"`
	WorkspaceRoot        string             `json:"workspace_root"`
	ConfigurationID      string             `json:"configuration_id"`
	JobID                string             `json:"job_id"`
	ExpectedNodeID       string             `json:"expected_node_id"`
	ExpectedInstallation string             `json:"expected_installation_id"`
	ExpectedVersion      string             `json:"expected_version"`
	ExpectedBuild        string             `json:"expected_build"`
}

func (sharedResumeLiveInput) String() string   { return "sharedResumeLiveInput{redacted}" }
func (sharedResumeLiveInput) GoString() string { return "sharedResumeLiveInput{redacted}" }
func (sharedResumeLiveInput) MarshalJSON() ([]byte, error) {
	return nil, errors.New("live credentials cannot be serialized")
}

type sharedResumeResult struct {
	Executor      jobs.ExecutorView `json:"executor"`
	ReportID      string            `json:"report_id"`
	Sealed        bool              `json:"sealed"`
	Archived      bool              `json:"archived"`
	Origin        bool              `json:"origin_verified"`
	BlockedWrites uint32            `json:"blocked_writes"`
	ProviderCalls uint32            `json:"provider_calls"`
	LaunchCalls   uint32            `json:"launch_calls"`
}

type sharedResumeEvent struct {
	Stage  string              `json:"stage"`
	JobID  string              `json:"job_id"`
	State  string              `json:"state"`
	Result *sharedResumeResult `json:"result,omitempty"`
}

// A test-only transport guard protects against accidental outbox or control
// delivery during recovery. Both backend write methods are blocked even when a
// caller asks for ObservationAccess; access selection alone is insufficient.
type sharedResumeGuard struct {
	writes   atomic.Uint32
	provider atomic.Uint32
}

func (guard *sharedResumeGuard) connect(
	ctx context.Context,
	configuration *remotenodes.Service,
	node remotesync.Node,
	access jobs.Access,
) (runnerConnection, error) {
	if access != jobs.ObservationAccess {
		guard.writes.Add(1)
		return nil, jobs.ErrUnavailable
	}
	connection, err := openProductionRunner(ctx, configuration, node, jobs.ObservationAccess)
	if err != nil {
		return nil, err
	}
	return &sharedResumeConnection{runnerConnection: connection, guard: guard}, nil
}

func (guard *sharedResumeGuard) RoundTrip(*http.Request) (*http.Response, error) {
	guard.provider.Add(1)
	return nil, errors.New("provider calls are forbidden in the query-only recovery role")
}

type sharedResumeConnection struct {
	runnerConnection
	guard *sharedResumeGuard
}

func (connection *sharedResumeConnection) Submit(
	context.Context,
	*jobcontract.Submission,
) (jobcontract.Receipt, error) {
	connection.guard.writes.Add(1)
	return jobcontract.Receipt{}, jobs.ErrUnavailable
}

func (connection *sharedResumeConnection) Command(
	context.Context,
	jobcontract.Command,
) (jobcontract.CommandConfirmation, error) {
	connection.guard.writes.Add(1)
	return jobcontract.CommandConfirmation{}, jobs.ErrUnavailable
}

func TestLiveRemoteSharedResumeRole(t *testing.T) {
	if os.Getenv(sharedResumeLiveEnable) != "1" {
		t.Skip("explicit original-job query-only recovery; bounded stdin only")
	}
	final := sharedResumeEvent{Stage: "failed", State: "query_only_recovery_incomplete"}
	defer func() {
		_ = json.NewEncoder(os.Stdout).Encode(final)
		code := 0
		if t.Failed() || final.Stage != "completed" {
			code = 1
		}
		os.Exit(code)
	}()
	input, err := decodeSharedResumeInput(os.Stdin)
	if err != nil {
		t.Fatal("invalid bounded query-only recovery configuration")
	}
	defer clear(input.Password)
	defer clear(input.NodeToken)
	final.JobID = input.JobID
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	configurationRoot := filepath.Join(input.WorkspaceRoot, "configuration")
	directory, _, err := productionStoragePaths(configurationRoot)
	if err != nil {
		t.Fatal("cannot locate the retained production workspace")
	}
	remoteRoot := filepath.Join(directory, "remote-nodes")
	fileStore, err := remotenodes.NewFileStore(filepath.Join(remoteRoot, "nodes.json"))
	if err != nil {
		t.Fatal("the original current node configuration is unavailable")
	}
	configuration, err := fileStore.Read(ctx)
	if err != nil {
		t.Fatal("the original node configuration does not satisfy the current contract")
	}
	var node *remotenodes.Node
	for index := range configuration.Nodes {
		if configuration.Nodes[index].ID == input.ConfigurationID {
			node = &configuration.Nodes[index]
		}
	}
	selected := configuration.Default.Kind == jobcontract.TargetRemote &&
		configuration.Default.NodeID == input.ConfigurationID
	if len(configuration.Nodes) != 1 || node == nil || !selected {
		t.Fatal("recovery requires the original single paired node and default selection")
	}
	store, err := restoreSharedResumeCredentials(ctx, remoteRoot, *node, input)
	if err != nil {
		t.Fatal("private credentials do not match the retained current node binding")
	}
	artifactRoot := filepath.Join(input.WorkspaceRoot, "no-runner-artifacts")
	entries, err := os.ReadDir(artifactRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("the retained installation source must exist and remain empty")
	}
	source, err := runnerartifacts.NewDirectorySource(artifactRoot)
	if err != nil {
		t.Fatal("cannot disable installation and upgrade in recovery")
	}
	guard := &sharedResumeGuard{}
	catalogRoot := filepath.Join(input.WorkspaceRoot, "catalog")
	options := productionOptions{
		userConfigDir: func() (string, error) { return configurationRoot, nil },
		executablePath: func() (string, error) {
			return filepath.Join(catalogRoot, "shared-live.exe"), nil
		},
		appVersion: "r15-client-resume", credentialStore: store, runnerSource: source,
		runnerConnector: guard.connect, protocolTransport: guard,
	}
	app := startLiveK3App(t, ctx, options)
	defer func() { _ = app.shutdown() }()
	assertLiveK3Outbox(t, app, []liveK3JobSummary{{JobID: input.JobID}})
	view, err := app.GetJob(input.JobID)
	if err != nil || view.Delivery != jobs.SubmissionAccepted || view.Confirmation == nil {
		t.Fatal("the original accepted job is missing; recovery will not submit a replacement")
	}
	expected := liveK3Input{ExpectedNodeID: input.ExpectedNodeID, ExpectedInstallation: input.ExpectedInstallation,
		ExpectedVersion: input.ExpectedVersion, ExpectedBuild: input.ExpectedBuild}
	assertLiveK3Executor(t, view, expected)
	if view.Kind != jobcontract.KindPerformance || view.Location.ConfigurationID != input.ConfigurationID {
		t.Fatal("the retained job is not the original remote performance submission")
	}
	_ = json.NewEncoder(os.Stdout).Encode(sharedResumeEvent{
		Stage: "original_accepted", JobID: input.JobID, State: string(view.Delivery),
	})
	view = waitSharedRunnerMirror(t, ctx, app, input.JobID)
	assertLiveK3Executor(t, view, expected)
	_ = json.NewEncoder(os.Stdout).Encode(sharedResumeEvent{
		Stage: "mirror_completed", JobID: input.JobID, State: string(view.Execution.Status),
	})
	outcome := waitLiveK3Performance(t, ctx, app, input.JobID)
	archive := waitLiveK3Archive(t, ctx, app, outcome.ReportID)
	result := sharedResumeResult{Executor: view.Executor, ReportID: outcome.ReportID,
		Sealed: outcome.SealedAt != "", Archived: archive.Archived,
		Origin: liveK3PerformanceOriginMatches(outcome, view) && liveK3ArchiveOriginMatches(archive, outcome)}
	if !sharedRunnerMinimumPerformance(outcome) || !result.Origin || !result.Sealed || !result.Archived {
		t.Fatal("the original mock performance result and archive are incomplete")
	}
	assertLiveK3Outbox(t, app, []liveK3JobSummary{{JobID: input.JobID}})
	if err := app.shutdown(); err != nil {
		t.Fatal("query-only recovery shutdown failed")
	}
	result.BlockedWrites, result.ProviderCalls = guard.writes.Load(), guard.provider.Load()
	if result.BlockedWrites != 0 || result.ProviderCalls != 0 {
		t.Fatal("query-only recovery attempted a forbidden runner write or provider request")
	}
	final = sharedResumeEvent{Stage: "completed", JobID: input.JobID,
		State: string(view.Execution.Status), Result: &result}
}

func decodeSharedResumeInput(reader io.Reader) (sharedResumeLiveInput, error) {
	const maximum = 64 << 10
	document, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil || len(document) > maximum {
		clear(document)
		return sharedResumeLiveInput{}, jobs.ErrInvalid
	}
	defer clear(document)
	var input sharedResumeLiveInput
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	decoded := decoder.Decode(&input) == nil && decoder.Decode(&struct{}{}) == io.EOF
	validPin := input.Endpoint.Validate() == nil && input.Endpoint.Fingerprint != "" && input.Endpoint.HostKeyType != ""
	validIdentity := domain.IsUUID(input.ConfigurationID) && domain.IsUUID(input.JobID) &&
		domain.IsUUID(input.ExpectedNodeID) && domain.IsUUID(input.ExpectedInstallation)
	validRelease := input.ExpectedVersion != "" && len(input.ExpectedVersion) <= 128 &&
		strings.TrimSpace(input.ExpectedVersion) == input.ExpectedVersion && jobcontract.IsSHA256(input.ExpectedBuild)
	validSecret := len(input.Password) > 0 && len(input.Password) <= 8192 &&
		len(input.NodeToken) >= 32 && len(input.NodeToken) <= 128
	filesystemRoot := filepath.VolumeName(input.WorkspaceRoot) + string(os.PathSeparator)
	validRoot := filepath.IsAbs(input.WorkspaceRoot) && input.WorkspaceRoot == filepath.Clean(input.WorkspaceRoot) &&
		input.WorkspaceRoot != filesystemRoot
	if !decoded || !validPin || !validIdentity || !validRelease || !validSecret || !validRoot {
		clear(input.Password)
		clear(input.NodeToken)
		return sharedResumeLiveInput{}, jobs.ErrInvalid
	}
	return input, nil
}

func restoreSharedResumeCredentials(
	ctx context.Context,
	root string,
	node remotenodes.Node,
	input sharedResumeLiveInput,
) (credentials.Store, error) {
	validBinding := node.Binding != nil && node.Binding.Identity.Validate() == nil && node.Endpoint() == input.Endpoint
	validAuth := node.Authentication.Kind == remotenodes.AuthenticationPassword &&
		node.Authentication.PrivateKeyRef == "" && node.Authentication.PassphraseRef == ""
	if !validBinding || !validAuth || node.ID != input.ConfigurationID {
		return nil, jobs.ErrTargetChanged
	}
	identity := node.Binding.Identity
	if identity.NodeID != input.ExpectedNodeID || identity.InstallationID != input.ExpectedInstallation {
		return nil, jobs.ErrTargetChanged
	}
	digest := sha256.Sum256(input.NodeToken)
	expectedDigest, err := hex.DecodeString(identity.TokenSHA256)
	if err != nil || subtle.ConstantTimeCompare(digest[:], expectedDigest) != 1 {
		return nil, jobs.ErrTargetChanged
	}
	passwordRef, passwordErr := credentials.ParseStoreRef(node.Authentication.PasswordRef)
	tokenRef, tokenErr := credentials.ParseStoreRef(node.Binding.TokenRef)
	validPassword := passwordErr == nil && passwordRef.Scope() == "" &&
		passwordRef.Purpose() == domain.CredentialSSHPassword
	validToken := tokenErr == nil && tokenRef.Scope() == "" && tokenRef.Purpose() == domain.CredentialRunnerToken
	if !validPassword || !validToken {
		return nil, jobs.ErrInvalid
	}
	scope, err := credentialScopeForRoot(root)
	if err != nil {
		return nil, jobs.ErrInvalid
	}
	store := credentials.NewMemoryStore()
	scoped, err := credentials.NewScopedStore(store, scope)
	if err != nil {
		return nil, jobs.ErrInvalid
	}
	if scoped.Set(ctx, passwordRef, input.Password) != nil || scoped.Set(ctx, tokenRef, input.NodeToken) != nil {
		return nil, jobs.ErrUnavailable
	}
	return store, nil
}

func TestSharedResumeGuardRejectsEveryWriteWithoutConnecting(t *testing.T) {
	guard := &sharedResumeGuard{}
	if _, err := guard.connect(t.Context(), nil, remotesync.Node{}, jobs.SubmissionAccess); err == nil {
		t.Fatal("submission access was allowed")
	}
	connection := &sharedResumeConnection{guard: guard}
	if _, err := connection.Submit(t.Context(), nil); err == nil {
		t.Fatal("submit was allowed through the observation connection")
	}
	if _, err := connection.Command(t.Context(), jobcontract.Command{}); err == nil {
		t.Fatal("control was allowed through the observation connection")
	}
	if _, err := guard.RoundTrip(nil); err == nil || guard.provider.Load() != 1 || guard.writes.Load() != 3 {
		t.Fatal("query-only guards did not reject every forbidden call")
	}
}

func TestSharedResumeInputRequiresOriginalIdentityAndSecrets(t *testing.T) {
	valid := map[string]any{
		"endpoint": map[string]any{"Host": "example.test", "Port": 22, "User": "runner",
			"Fingerprint": "SHA256:" + base64.RawStdEncoding.EncodeToString(make([]byte, 32)),
			"HostKeyType": "ssh-ed25519"},
		"password":       base64.StdEncoding.EncodeToString([]byte("synthetic-unit-password")),
		"node_token":     base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 32)),
		"workspace_root": t.TempDir(), "configuration_id": "84000000-0000-4000-8000-000000000001",
		"job_id":                   "84000000-0000-4000-8000-000000000002",
		"expected_node_id":         "84000000-0000-4000-8000-000000000003",
		"expected_installation_id": "84000000-0000-4000-8000-000000000004",
		"expected_version":         "actual-runner-unit", "expected_build": strings.Repeat("a", 64),
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{name: "current original job", valid: true},
		{name: "missing token", change: func(value map[string]any) { delete(value, "node_token") }},
		{name: "raw password", change: func(value map[string]any) { value["password"] = "raw-not-base64" }},
		{name: "short token", change: func(value map[string]any) { value["node_token"] = "eA==" }},
		{name: "missing job", change: func(value map[string]any) { delete(value, "job_id") }},
		{name: "provider URL", change: func(value map[string]any) { value["base_url"] = "https://provider.example" }},
		{name: "launch command", change: func(value map[string]any) { value["kind"] = "performance" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := make(map[string]any, len(valid))
			for key, item := range valid {
				value[key] = item
			}
			if test.change != nil {
				test.change(value)
			}
			document, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			input, err := decodeSharedResumeInput(bytes.NewReader(document))
			defer clear(input.Password)
			defer clear(input.NodeToken)
			if (err == nil) != test.valid {
				t.Fatal("query-only input acceptance differs from its current contract")
			}
		})
	}
	if _, err := decodeSharedResumeInput(bytes.NewReader(bytes.Repeat([]byte(" "), (64<<10)+1))); err == nil {
		t.Fatal("oversized private input was accepted")
	}
}

func TestSharedResumeRestoresOnlyMatchingCurrentScopedReferences(t *testing.T) {
	passwordRef, _ := credentials.NewStoreRef(domain.CredentialSSHPassword, "84000000-0000-4000-8000-000000000001")
	tokenRef, _ := credentials.NewStoreRef(domain.CredentialRunnerToken, "84000000-0000-4000-8000-000000000002")
	input := sharedResumeLiveInput{Password: []byte("synthetic-unit-password"), NodeToken: bytes.Repeat([]byte("x"), 32),
		ConfigurationID:      "84000000-0000-4000-8000-000000000003",
		ExpectedNodeID:       "84000000-0000-4000-8000-000000000004",
		ExpectedInstallation: "84000000-0000-4000-8000-000000000005"}
	defer clear(input.Password)
	defer clear(input.NodeToken)
	node := remotenodes.Node{Authentication: remotenodes.Authentication{
		Kind: remotenodes.AuthenticationPassword, PasswordRef: passwordRef.Value()}, Binding: &remotenodes.Binding{
		TokenRef: tokenRef.Value()}}
	node.ID = input.ConfigurationID
	node.Binding.Identity.Contract = jobcontract.CurrentContract
	node.Binding.Identity.NodeID = input.ExpectedNodeID
	node.Binding.Identity.InstallationID = input.ExpectedInstallation
	node.Binding.Identity.ListenPort = 38423
	digest := sha256.Sum256(input.NodeToken)
	node.Binding.Identity.TokenSHA256 = hex.EncodeToString(digest[:])
	root := t.TempDir()
	store, err := restoreSharedResumeCredentials(t.Context(), root, node, input)
	if err != nil {
		t.Fatal("matching current references were rejected")
	}
	scope, _ := credentialScopeForRoot(root)
	scoped, _ := credentials.NewScopedStore(store, scope)
	for _, ref := range []credentials.StoreRef{passwordRef, tokenRef} {
		lease, err := scoped.Get(t.Context(), ref)
		if err != nil {
			t.Fatal("the original logical reference was not restored into its production scope")
		}
		_ = lease.Close()
	}
	input.NodeToken[0] = 'y'
	if _, err := restoreSharedResumeCredentials(t.Context(), root, node, input); err == nil {
		t.Fatal("a different node token was accepted")
	}
}
