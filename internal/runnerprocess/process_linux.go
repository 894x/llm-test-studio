// Package runnerprocess assembles the independent Linux execution owner.
package runnerprocess

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/894x/llm-test-studio/internal/adapters/runnerhttp"
	"github.com/894x/llm-test-studio/internal/application/jobhost"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/enginebuild"
	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerstate"
)

const ConfigName = "runner.json"

type Options struct {
	StatePath string
	Version   string
	OnReady   func(Ready) error
}

type Ready struct {
	Contract           int    `json:"contract"`
	URL                string `json:"url"`
	NodeID             string `json:"node_id"`
	NodeInstallationID string `json:"node_installation_id"`
	EngineBuildID      string `json:"engine_build_id"`
	RunnerVersion      string `json:"runner_version"`
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

func Initialize(path string, config runnerconfig.Config) error {
	if err := config.Validate(); err != nil {
		return err
	}
	state, err := runnerstate.Open(path)
	if err != nil {
		return err
	}
	defer state.Close()
	document, err := json.Marshal(config)
	if err != nil {
		return err
	}
	defer clear(document)
	return state.WriteNew(ConfigName, document)
}

func Run(ctx context.Context, options Options) (runErr error) {
	if ctx == nil || options.Version == "" {
		return runnerconfig.ErrInvalid
	}
	state, err := runnerstate.Open(options.StatePath)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, state.Close()) }()
	document, err := state.ReadPrivate(ConfigName, runnerconfig.MaxConfigBytes)
	if err != nil {
		return err
	}
	defer clear(document)
	config, err := runnerconfig.Decode(bytes.NewReader(document))
	if err != nil {
		return err
	}
	defer config.Clear()
	controlStore := maintenanceStore{state: state, installationID: config.InstallationID}
	// Validate the current control format before any database or recovery write.
	if _, err := controlStore.Read(ctx); err != nil {
		return err
	}
	guard := &diskGuard{state: state, config: config}
	if err := guard.Check(ctx); err != nil {
		return err
	}
	startup, cancelStartup := context.WithTimeout(ctx, 30*time.Second)
	defer cancelStartup()
	path := filepath.Join(state.Path(), "runner.db")
	if err := sqlite.Migrate(startup, path, sqlite.MigrateOptions{AppVersion: options.Version}); err != nil {
		return err
	}
	repository, err := sqlite.OpenRepository(startup, path, sqlite.RepositoryOptions{})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, repository.Close()) }()
	readView, err := repository.OpenReadOnlyView(startup)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, readView.Close()) }()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns, transport.MaxIdleConnsPerHost = 4096, 4096
	defer transport.CloseIdleConnections()
	core, err := jobhost.NewCore(jobhost.CoreDependencies{
		Repository: repository, Runs: runs.NewProtocolExecutor(transport), Transport: transport, Clock: wallClock{},
	})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, core.Close()) }()
	buildID, err := enginebuild.Current()
	if err != nil {
		return err
	}
	host, err := jobhost.New(startup, jobhost.Dependencies{
		Repository: repository, Executor: core, Guard: guard, Clock: wallClock{}, Policy: config.Policy(),
		Target: jobcontract.ExecutionTarget{Kind: jobcontract.TargetRemote, NodeID: config.NodeID},
		NodeID: config.NodeID, RunnerVersion: options.Version,
		InstallationID: config.InstallationID, EngineBuildID: buildID, CommitmentKey: config.CommitmentKey,
	})
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, host.Close()) }()
	if err := repository.CheckWritable(startup); err != nil {
		return err
	}
	maintenance, err := jobhost.NewMaintenance(startup, host, controlStore)
	if err != nil {
		return err
	}
	hash, _ := hex.DecodeString(config.TokenSHA256)
	handler, err := runnerhttp.New(runnerhttp.Dependencies{
		Host: host, Maintenance: maintenance, Repository: readView, TokenSHA256: [32]byte(hash),
		Capabilities: capabilities(),
	})
	clear(hash)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(int(config.ListenPort)))
	if err != nil {
		return err
	}
	defer listener.Close()
	requests := &drainingHandler{next: handler}
	server := &http.Server{
		Handler: requests, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 16 << 10,
		ErrorLog: log.New(io.Discard, "", 0),
	}
	serveResult := make(chan error, 1)
	serveDone := make(chan struct{})
	apiCtx, cancelAPI := context.WithCancel(context.Background())
	server.BaseContext = func(net.Listener) context.Context { return apiCtx }
	go func() {
		defer close(serveDone)
		serveResult <- server.Serve(listener)
	}()
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		requests.shutdown(shutdown, server, cancelAPI, serveDone)
	}()
	origin := host.Identity().Origin
	ready := Ready{
		Contract: jobcontract.CurrentContract, URL: "http://" + listener.Addr().String(), NodeID: origin.NodeID,
		NodeInstallationID: origin.InstallationID, EngineBuildID: origin.EngineBuildID, RunnerVersion: origin.RunnerVersion,
	}
	if options.OnReady != nil {
		if err := options.OnReady(ready); err != nil {
			return err
		}
	}
	if err := notify("READY=1"); err != nil {
		return err
	}
	monitorCtx, cancelMonitor := context.WithCancel(context.Background())
	monitorResult := make(chan error, 1)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		monitorResult <- monitor(
			monitorCtx,
			guard,
			host,
			repository,
		)
	}()
	defer func() { cancelMonitor(); <-monitorDone }()
	select {
	case <-ctx.Done():
		return nil
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-monitorResult:
		return err
	}
}

func capabilities() jobcontract.RunnerCapabilities {
	capabilities := jobhost.CoreCapabilities()
	capabilities.Features = append(capabilities.Features, "maintenance")
	return capabilities
}

func monitor(
	ctx context.Context,
	guard *diskGuard,
	host *jobhost.Host,
	repository *sqlite.Repository,
) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		probeCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := guard.Check(probeCtx)
		if err == nil {
			err = repository.CheckWritable(probeCtx)
		}
		cancel()
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			continue
		}
		if err != nil || !host.Status().Healthy {
			host.MarkUnavailable()
			return jobhost.ErrUnavailable
		}
		if err := notify("WATCHDOG=1"); err != nil {
			return err
		}
	}
}
