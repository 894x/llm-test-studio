package bootstrap

import (
	"io/fs"
	"net/http"
	"os"
	"time"

	casebundle "github.com/894x/llm-test-studio/data/cases"
	suitebundle "github.com/894x/llm-test-studio/data/suites"
	"github.com/894x/llm-test-studio/internal/application/remotenodes"
	"github.com/894x/llm-test-studio/internal/application/runs"
	"github.com/894x/llm-test-studio/internal/credentials"
	"github.com/894x/llm-test-studio/internal/runnerartifacts"
)

type Options struct {
	ProtocolTransport       http.RoundTripper
	UserConfigDir           func() (string, error)
	AppVersion              string
	CaseBundle              fs.FS
	SuiteBundle             fs.FS
	ExecutablePath          func() (string, error)
	ReportRunDiagnostic     func(runs.Diagnostic)
	ReportCredentialCleanup func(error)
	CredentialStore         credentials.Store
	RunnerSource            runnerartifacts.Source
	RunnerRelease           string
	RemoteConnector         remotenodes.Connector
	RunnerConnector         runnerConnector
}

type ProductionClock struct{}

func (ProductionClock) Now() time.Time {
	return time.Now().UTC()
}

func DefaultOptions(appVersion string) Options {
	return Options{
		UserConfigDir:  os.UserConfigDir,
		AppVersion:     appVersion,
		CaseBundle:     casebundle.Bundle,
		SuiteBundle:    suitebundle.Bundle,
		ExecutablePath: os.Executable,
	}
}
