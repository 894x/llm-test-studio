package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/runnerinstall"
)

func TestUninstallCLIRejectsUnknownInputAndArgvWithoutEcho(t *testing.T) {
	for _, name := range []string{"uninstall", "recover-uninstall"} {
		for _, input := range []string{`{}`, `{"password":"never-echo-this"}`, `{"contract":2}`} {
			var output, diagnostic bytes.Buffer
			code := run(t.Context(), []string{name}, streams{
				input: strings.NewReader(input), output: &output, errors: &diagnostic,
			})
			if code != 1 || output.Len() != 0 || diagnostic.String() != "invalid_installation_request\n" {
				t.Fatalf("uninstall stdin failed unsafely: %d %q", code, diagnostic.String())
			}
		}
		var output, diagnostic bytes.Buffer
		if run(t.Context(), []string{name, "--purge", "never-echo-this"}, streams{
			input: strings.NewReader(`{}`), output: &output, errors: &diagnostic,
		}) != 1 || strings.Contains(diagnostic.String(), "never-echo-this") {
			t.Fatal("uninstall accepted destructive argv or leaked input")
		}
	}
}

func TestUninstallCLIDecodesCurrentRequestAndUsesFixedDiagnostics(t *testing.T) {
	request := runnerinstall.UninstallRequest{Contract: 1, OperationID: "db2810f8-dc34-47ac-a2c0-f7823986dacf",
		NodeID: "eab57e15-3e5b-4714-8619-333db7042426", InstallationID: "0498de0e-ebbc-4419-b22b-e5001b6193c4",
		ExpectedBuildID: strings.Repeat("a", 64)}
	raw, _ := json.Marshal(request)
	failures := []error{nil, runnerinstall.ErrBusy, runnerinstall.ErrUninstalled, errors.New("private-path-do-not-print")}
	for _, failure := range failures {
		var output, diagnostic bytes.Buffer
		calls := 0
		invoke := func(
			_ context.Context,
			got runnerinstall.UninstallRequest,
			progress func(runnerinstall.UninstallProgress),
		) (runnerinstall.UninstallOperation, error) {
			calls++
			if got != request {
				t.Fatal("CLI changed the durable uninstall identity")
			}
			progress(runnerinstall.UninstallProgress{OperationID: got.OperationID, Stage: "checking"})
			return runnerinstall.UninstallOperation{Request: got, Stage: "checking"}, failure
		}
		code := runUninstall(t.Context(), streams{input: bytes.NewReader(raw), output: &output, errors: &diagnostic}, invoke)
		if calls != 1 || strings.Contains(diagnostic.String(), "private-path") {
			t.Fatal("CLI repeated the operation or leaked an internal error")
		}
		if failure == nil && code != 0 || failure != nil && code != 1 {
			t.Fatal("incorrect uninstall exit code")
		}
		if failure != nil && diagnostic.String() != runnerinstall.ErrorCode(failure)+"\n" {
			t.Fatal("uninstall emitted an unrecognized diagnostic")
		}
	}
}
