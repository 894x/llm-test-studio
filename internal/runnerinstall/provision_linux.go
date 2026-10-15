package runnerinstall

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

type layout struct {
	program string
	state   string
	install string
	unit    string
}

func productionLayout() layout {
	return layout{
		program: ProgramPath, state: StatePath,
		install: InstallPath, unit: "/etc/systemd/system/llm-test-runner.service",
	}
}

type account struct{ uid, gid int }

type system interface {
	Preflight(context.Context) error
	Account(context.Context) (account, error)
	Initialize(context.Context, initializeInput) error
	Start(context.Context) error
	Verify(context.Context, verificationInput) error
}

type initializeInput struct {
	program string
	port    uint16
	token   []byte
}

type verificationInput struct {
	account account
	buildID string
}

type provisioner struct {
	layout   layout
	system   system
	source   io.ReadSeeker
	progress func(Progress)
}

// Provision installs once or reuses the existing current-contract runner. A
// different artifact never upgrades an existing installation through this path.
func Provision(ctx context.Context, request Request, progress func(Progress)) (Result, error) {
	if ctx == nil {
		return Result{}, ErrInvalid
	}
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	if os.Geteuid() != 0 {
		return Result{}, ErrRootRequired
	}
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		return Result{}, err
	}
	defer source.Close()
	paths := productionLayout()
	installer := provisioner{
		layout: paths, system: localSystem{layout: paths}, source: source, progress: progress,
	}
	return installer.provision(ctx, request)
}

func (installer provisioner) provision(ctx context.Context, request Request) (result Result, runErr error) {
	if err := request.Validate(); err != nil {
		return Result{}, err
	}
	buildID, err := sourceDigest(installer.source)
	if err != nil || buildID != request.ArtifactSHA256 {
		return Result{}, ErrIntegrity
	}
	if err := installer.system.Preflight(ctx); err != nil {
		return Result{}, err
	}
	if err := installer.layout.verifyBases(); err != nil {
		return Result{}, err
	}
	if err := installer.layout.checkInstallationRoot(); err != nil {
		return Result{}, err
	}
	lock, err := runnerstate.Open(installer.layout.install)
	if errors.Is(err, runnerstate.ErrLocked) {
		return Result{}, ErrBusy
	}
	if err != nil {
		return Result{}, err
	}
	defer lock.Close()
	if err := syncDirectory(installer.layout.install); err != nil {
		return Result{}, err
	}
	if err := syncDirectory(filepath.Dir(installer.layout.install)); err != nil {
		return Result{}, err
	}
	operation, err := loadOperation(lock, request)
	if err != nil {
		return Result{}, err
	}
	if operation.Result != nil {
		return *operation.Result, nil
	}
	checkpoint := func(stage string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		operation.Stage, operation.ChangedAt, operation.ErrorCode = stage, time.Now().UTC(), ""
		if err := writeOperation(lock, operation); err != nil {
			return err
		}
		if installer.progress != nil {
			installer.progress(Progress{OperationID: request.OperationID, Stage: stage})
		}
		return nil
	}
	defer func() {
		if runErr != nil && operation.Result == nil {
			operation.ErrorCode, operation.ChangedAt = ErrorCode(runErr), time.Now().UTC()
			_ = writeOperation(lock, operation)
		}
	}()
	if err := checkpoint("checking"); err != nil {
		return Result{}, err
	}
	unitExists, err := installer.layout.checkUnit()
	if err != nil {
		return Result{}, err
	}
	if err := installer.layout.checkProgramRoot(lock); err != nil {
		return Result{}, err
	}
	if err := checkpoint("account"); err != nil {
		return Result{}, err
	}
	owner, err := installer.system.Account(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := installer.layout.prepareState(owner); err != nil {
		return Result{}, err
	}
	config, exists, err := installer.layout.readConfig(owner)
	if err != nil {
		return Result{}, err
	}
	defer func() { config.Clear() }()
	token, err := pairingToken(lock, exists)
	if err != nil {
		return Result{}, err
	}
	defer clear(token)
	if exists && !tokenMatches(config, token) {
		return Result{}, ErrPairingRequired
	}
	if err := checkpoint("program"); err != nil {
		return Result{}, err
	}
	selected, err := installer.layout.currentBuild()
	if err != nil {
		return Result{}, err
	}
	reused := unitExists && exists && selected != ""
	if unitExists && (selected == "" || !exists) {
		return Result{}, ErrUnownedPath
	}
	if selected == "" {
		if err := installer.publishProgram(buildID, request.OperationID); err != nil {
			return Result{}, err
		}
		if err := installer.layout.selectCurrent(buildID); err != nil {
			return Result{}, err
		}
		selected = buildID
	}
	if err := checkpoint("configuration"); err != nil {
		return Result{}, err
	}
	if !exists {
		input := initializeInput{
			program: installer.layout.programPath(selected), port: request.ListenPort, token: token,
		}
		initializeErr := installer.system.Initialize(ctx, input)
		config, exists, err = installer.layout.readConfig(owner)
		if err != nil {
			return Result{}, err
		}
		if !exists || !tokenMatches(config, token) {
			if initializeErr != nil {
				return Result{}, initializeErr
			}
			return Result{}, ErrIntegrity
		}
		// A lost child acknowledgment is resolved only by the immutable config.
		if config.ListenPort != request.ListenPort {
			return Result{}, ErrIntegrity
		}
	}
	if err := writeIdentity(lock, config); err != nil {
		return Result{}, err
	}
	if err := checkpoint("service"); err != nil {
		return Result{}, err
	}
	if !unitExists {
		if err := installer.layout.writeUnit(); err != nil {
			return Result{}, err
		}
	}
	if err := installer.system.Start(ctx); err != nil {
		return Result{}, err
	}
	if err := checkpoint("verifying"); err != nil {
		return Result{}, err
	}
	info, err := probeRunner(ctx, config, token)
	if err != nil {
		return Result{}, err
	}
	if info.EngineBuildID != selected {
		return Result{}, ErrIntegrity
	}
	if err := installer.system.Verify(ctx, verificationInput{account: owner, buildID: selected}); err != nil {
		return Result{}, err
	}
	result = Result{
		Contract: jobcontract.CurrentContract, OperationID: request.OperationID, Runner: info,
		ListenPort: config.ListenPort, Reused: reused, ObservedAt: time.Now().UTC(),
	}
	operation.Stage, operation.Result, operation.ChangedAt = "completed", &result, time.Now().UTC()
	if err := writeOperation(lock, operation); err != nil {
		return Result{}, err
	}
	return result, nil
}

func writeIdentity(state *runnerstate.State, config runnerconfig.Config) error {
	identity := Identity{
		Contract: config.Contract, NodeID: config.NodeID, InstallationID: config.InstallationID,
		ListenPort: config.ListenPort, TokenSHA256: config.TokenSHA256,
	}
	document, err := state.ReadPrivate("identity.json", MaxRequestBytes)
	if err == nil {
		existing, err := DecodeIdentity(document)
		if err != nil {
			return err
		}
		if existing != identity {
			return ErrPairingRequired
		}
		return state.SyncPrivate("identity.json")
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	document, err = json.Marshal(identity)
	if err != nil {
		return err
	}
	return state.WriteNew("identity.json", document)
}

func operationName(id string) string { return "operation-" + id + ".json" }

func loadOperation(state *runnerstate.State, request Request) (Operation, error) {
	document, err := state.ReadPrivate(operationName(request.OperationID), MaxRecordBytes)
	if errors.Is(err, os.ErrNotExist) {
		directory, err := os.Open(state.Path())
		if err != nil {
			return Operation{}, err
		}
		defer directory.Close()
		entries, err := directory.ReadDir(10002)
		if err != nil && !errors.Is(err, io.EOF) {
			return Operation{}, err
		}
		if len(entries) >= 10002 {
			return Operation{}, ErrBusy
		}
		return Operation{Request: request, Stage: "checking", ChangedAt: time.Now().UTC()}, nil
	}
	if err != nil {
		return Operation{}, err
	}
	operation, err := DecodeOperation(document)
	if err != nil {
		return Operation{}, err
	}
	if operation.Request != request {
		return Operation{}, ErrConflict
	}
	if err := state.SyncPrivate(operationName(request.OperationID)); err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func writeOperation(state *runnerstate.State, operation Operation) error {
	document, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	if _, err := DecodeOperation(document); err != nil {
		return err
	}
	name := operationName(operation.Request.OperationID)
	if err := state.ReplacePrivate(name, document); err != nil {
		confirmed, readErr := state.ReadPrivate(name, MaxRecordBytes)
		if readErr != nil || !bytes.Equal(confirmed, document) {
			return err
		}
		return state.SyncPrivate(name)
	}
	return nil
}

func pairingToken(state *runnerstate.State, configured bool) ([]byte, error) {
	token, err := state.ReadPrivate(PairingName, 128)
	if err == nil {
		config, err := runnerconfig.New(token)
		config.Clear()
		if err != nil {
			clear(token)
			return nil, ErrPairingRequired
		}
		if err := state.SyncPrivate(PairingName); err != nil {
			clear(token)
			return nil, err
		}
		return token, nil
	}
	if !errors.Is(err, os.ErrNotExist) || configured {
		return nil, ErrPairingRequired
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	token = base64.RawURLEncoding.AppendEncode([]byte{}, seed)
	clear(seed)
	if err := state.WriteNew(PairingName, token); err != nil {
		clear(token)
		return nil, err
	}
	return token, nil
}

func tokenMatches(config runnerconfig.Config, token []byte) bool {
	digest := sha256.Sum256(token)
	return config.TokenSHA256 == hex.EncodeToString(digest[:])
}

func probeRunner(ctx context.Context, config runnerconfig.Config, token []byte) (jobcontract.RunnerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	url := "http://127.0.0.1:" + strconv.Itoa(int(config.ListenPort)) + "/runner"
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		url,
		nil,
	)
	if err != nil {
		return jobcontract.RunnerInfo{}, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+string(token))
	request.Header.Set(jobcontract.ContractHeader, strconv.Itoa(jobcontract.CurrentContract))
	response, err := client.Do(request)
	if err != nil {
		return jobcontract.RunnerInfo{}, ErrService
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusUnauthorized:
		return jobcontract.RunnerInfo{}, ErrPairingRequired
	case http.StatusUpgradeRequired:
		return jobcontract.RunnerInfo{}, ErrUpgradeRequired
	case http.StatusOK:
	default:
		return jobcontract.RunnerInfo{}, ErrService
	}
	document, err := io.ReadAll(io.LimitReader(response.Body, MaxRecordBytes+1))
	if err != nil || len(document) > MaxRecordBytes {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	var info jobcontract.RunnerInfo
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		return jobcontract.RunnerInfo{}, ErrUpgradeRequired
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return jobcontract.RunnerInfo{}, ErrUpgradeRequired
	}
	if err := info.Validate(); err != nil {
		return jobcontract.RunnerInfo{}, ErrUpgradeRequired
	}
	if info.NodeID != config.NodeID || info.NodeInstallationID != config.InstallationID {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	if info.OS != "linux" || info.Arch != runtime.GOARCH {
		return jobcontract.RunnerInfo{}, ErrIntegrity
	}
	if !info.Healthy {
		return jobcontract.RunnerInfo{}, ErrService
	}
	return info, nil
}

func (paths layout) programPath(buildID string) string {
	return filepath.Join(
		paths.program,
		"releases",
		buildID,
		"llm-test-runner",
	)
}
