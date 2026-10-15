package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/persistence/sqlite"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
	"github.com/894x/llm-test-studio/internal/runnerstate"
	"golang.org/x/sys/unix"
)

var version = "dev"

type streams struct {
	input  io.Reader
	output io.Writer
	errors io.Writer
}

func main() {
	unix.Umask(0077)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], streams{input: os.Stdin, output: os.Stdout, errors: os.Stderr}))
}

func run(ctx context.Context, arguments []string, streams streams) int {
	if len(arguments) == 1 && arguments[0] == "version" {
		buildID, err := runnerprocess.BuildID()
		if err != nil {
			return fail(streams.errors, err)
		}
		err = json.NewEncoder(streams.output).Encode(struct {
			Version string `json:"version"`
			BuildID string `json:"engine_build_id"`
		}{Version: version, BuildID: buildID})
		if err != nil {
			return fail(streams.errors, err)
		}
		return 0
	}
	if len(arguments) == 0 {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	if arguments[0] != "init" && arguments[0] != "serve" {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	flags := flag.NewFlagSet("llm-test-runner", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("state", "", "private state directory")
	var port *uint
	if arguments[0] == "init" {
		port = flags.Uint("port", 44917, "loopback management port")
	}
	if err := flags.Parse(arguments[1:]); err != nil {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	if flags.NArg() != 0 || *path == "" {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	if arguments[0] == "serve" {
		err := runnerprocess.Run(ctx, runnerprocess.Options{
			StatePath: *path, Version: version,
			OnReady: func(ready runnerprocess.Ready) error { return json.NewEncoder(streams.output).Encode(ready) },
		})
		if err != nil {
			return fail(streams.errors, err)
		}
		return 0
	}
	if *port < 1024 || *port > 65535 {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	token, err := io.ReadAll(io.LimitReader(streams.input, 129))
	if err != nil {
		return fail(streams.errors, runnerconfig.ErrInvalid)
	}
	defer clear(token)
	config, err := runnerconfig.New(token)
	if err != nil {
		return fail(streams.errors, err)
	}
	defer config.Clear()
	config.ListenPort = uint16(*port)
	if err := runnerprocess.Initialize(*path, config); err != nil {
		return fail(streams.errors, err)
	}
	err = json.NewEncoder(streams.output).Encode(struct {
		NodeID         string `json:"node_id"`
		InstallationID string `json:"installation_id"`
		ListenPort     uint16 `json:"listen_port"`
	}{NodeID: config.NodeID, InstallationID: config.InstallationID, ListenPort: config.ListenPort})
	if err != nil {
		return fail(streams.errors, err)
	}
	return 0
}

func fail(output io.Writer, err error) int {
	code := "runner_start_failed"
	switch {
	case errors.Is(err, runnerstate.ErrLocked):
		code = "runner_already_running"
	case errors.Is(err, os.ErrExist):
		code = "runner_already_initialized"
	case errors.Is(err, jobcontract.ErrUnsupported), errors.Is(err, sqlite.ErrSchemaResetRequired):
		code = "runner_upgrade_required"
	case errors.Is(err, runnerconfig.ErrInvalid), errors.Is(err, runnerstate.ErrInvalid):
		code = "invalid_runner_configuration"
	}
	_, _ = io.WriteString(output, code+"\n")
	return 1
}
