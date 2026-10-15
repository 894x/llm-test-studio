package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/894x/llm-test-studio/internal/jobcontract"
	"github.com/894x/llm-test-studio/internal/runnerconfig"
	"github.com/894x/llm-test-studio/internal/runnerinstall"
	"github.com/894x/llm-test-studio/internal/runnerprocess"
)

func TestProvisionRejectsInvalidAndUnsupportedRequestsBeforeInstallation(t *testing.T) {
	request := runnerinstall.Request{
		Contract:       jobcontract.CurrentContract + 1,
		OperationID:    "d9b48d9a-977c-4dd8-8c6b-3bdeba3b37d7",
		ArtifactSHA256: strings.Repeat("a", 64), ListenPort: 44917,
	}
	unsupported, _ := json.Marshal(request)
	cases := []struct {
		name  string
		input string
		code  string
	}{
		{name: "invalid", input: `{}`, code: "invalid_installation_request"},
		{name: "unsupported", input: string(unsupported), code: "runner_upgrade_required"},
	}
	if os.Geteuid() != 0 {
		request.Contract = jobcontract.CurrentContract
		document, _ := json.Marshal(request)
		cases = append(cases, struct{ name, input, code string }{
			name: "nonroot", input: string(document), code: "installation_root_required",
		})
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			var output, diagnostic bytes.Buffer
			code := run(t.Context(), []string{"provision"}, streams{
				input: strings.NewReader(test.input), output: &output, errors: &diagnostic,
			})
			valid := code == 1 && output.Len() == 0 && diagnostic.String() == test.code+"\n"
			if !valid {
				t.Fatalf(
					"code=%d output=%q diagnostic=%q",
					code,
					output.String(),
					diagnostic.String(),
				)
			}
		})
	}
}

func TestInitUsesStdinAndPreservesExistingIdentityOnRepeatedCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	token := strings.Repeat("t", 64)
	var output, diagnostic bytes.Buffer
	invoke := func() int {
		output.Reset()
		diagnostic.Reset()
		return run(t.Context(), []string{"init", "--state", path}, streams{
			input: strings.NewReader(token), output: &output, errors: &diagnostic,
		})
	}
	code := invoke()
	validOutput := code == 0 && !strings.Contains(output.String(), token) && diagnostic.Len() == 0
	if !validOutput {
		t.Fatalf("init result=%d diagnostic=%q", code, diagnostic.String())
	}
	before, err := os.ReadFile(filepath.Join(path, runnerprocess.ConfigName))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(before)
	if code := invoke(); code == 0 || diagnostic.String() != "runner_already_initialized\n" {
		t.Fatal("repeated init did not explicitly preserve the existing installation")
	}
	after, err := os.ReadFile(filepath.Join(path, runnerprocess.ConfigName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("repeated init changed authorization or identity")
	}
	clear(after)
}

func TestServeRejectsUnsupportedPrivateConfigWithoutOpeningDatabase(t *testing.T) {
	config, err := runnerconfig.New([]byte(strings.Repeat("t", 64)))
	if err != nil {
		t.Fatal(err)
	}
	defer config.Clear()
	path := filepath.Join(t.TempDir(), "state")
	if err := runnerprocess.Initialize(path, config); err != nil {
		t.Fatal(err)
	}
	config.Contract++
	document, _ := json.Marshal(config)
	defer clear(document)
	if err := os.WriteFile(filepath.Join(path, runnerprocess.ConfigName), document, 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostic bytes.Buffer
	code := run(context.Background(), []string{"serve", "--state", path}, streams{
		input: strings.NewReader(""), output: &output, errors: &diagnostic,
	})
	validOutput := code != 0 && diagnostic.String() == "runner_upgrade_required\n" && output.Len() == 0
	if !validOutput {
		t.Fatal("unsupported current-format marker was not rejected explicitly")
	}
	retained, err := os.ReadFile(filepath.Join(path, runnerprocess.ConfigName))
	if err != nil || !bytes.Equal(retained, document) {
		t.Fatal("unsupported private configuration was modified")
	}
	clear(retained)
	if _, err := os.Stat(filepath.Join(path, "runner.db")); !os.IsNotExist(err) {
		t.Fatal("unsupported configuration reached database initialization")
	}
}
