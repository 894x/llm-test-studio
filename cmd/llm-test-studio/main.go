package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/894x/llm-test-studio/engine/apiaudit"
	"github.com/894x/llm-test-studio/internal/application/compatibility"
	appdoctor "github.com/894x/llm-test-studio/internal/application/doctor"
	"github.com/894x/llm-test-studio/internal/diagnostics"
)

const rootUsage = `Usage: llm-test-studio <doctor|audit|load> [options]

Commands:
  doctor      Check whether the Go core and local case definitions are usable
  audit       List or run compatibility audit cases
  load        Run an OpenAI-compatible load test with the Go core
`

const auditUsage = `Usage: llm-test-studio audit <list|run> [options]

Commands:
  list        List available compatibility cases
  run         Run selected compatibility cases
`

type compatibilityApplication interface {
	List(context.Context, compatibility.ListRequest) ([]compatibility.CaseDefinition, error)
	Run(context.Context, compatibility.RunRequest) (compatibility.FinalEvent, error)
}

type dependencies struct {
	getenv                  func(string) string
	httpDoer                compatibility.HTTPDoer
	doctorFileSystem        appdoctor.FileSystem
	newApplication          func(compatibility.Dependencies) compatibilityApplication
	applicationDependencies compatibility.Dependencies
}

func defaultDependencies() dependencies {
	return dependencies{
		getenv:           os.Getenv,
		httpDoer:         http.DefaultClient,
		doctorFileSystem: appdoctor.OSFileSystem{},
		newApplication: func(dependencies compatibility.Dependencies) compatibilityApplication {
			return compatibility.New(dependencies)
		},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr, defaultDependencies()))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, dependencies dependencies) int {
	_ = stdin
	if len(args) == 0 {
		return diagnosticExit(stderr, "json", "usage_error", "a command is required", 2)
	}
	switch args[0] {
	case "help", "-h", "--help":
		return writeUsage(stdout, stderr, rootUsage)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr, dependencies)
	case "audit":
		return runAudit(ctx, args[1:], stdout, stderr, dependencies)
	case "load":
		return runLoad(ctx, args[1:], stdout, stderr, dependencies)
	default:
		return diagnosticExit(stderr, "json", "usage_error", "unknown command", 2)
	}
}

func runAudit(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	if len(args) == 0 {
		return diagnosticExit(stderr, "json", "usage_error", "an audit command is required", 2)
	}
	switch args[0] {
	case "help", "-h", "--help":
		return writeUsage(stdout, stderr, auditUsage)
	case "list":
		return runAuditList(ctx, args[1:], stdout, stderr, dependencies)
	case "run":
		return runAuditRun(ctx, args[1:], stdout, stderr, dependencies)
	default:
		return diagnosticExit(stderr, "json", "usage_error", "unknown audit command", 2)
	}
}

type stringListFlag []string

func (values *stringListFlag) String() string { return strings.Join(*values, ",") }

func (values *stringListFlag) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("case id must not be empty")
	}
	*values = append(*values, value)
	return nil
}

type diagnosticPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type diagnosticResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Type          string            `json:"type"`
	Payload       diagnosticPayload `json:"payload"`
}

type runOutputFormatFlag struct {
	value string
	set   bool
}

func (format *runOutputFormatFlag) String() string { return format.value }

func (format *runOutputFormatFlag) Set(value string) error {
	if value != "jsonl" && value != "json" && value != "human" {
		return fmt.Errorf("format must be jsonl, json, or human")
	}
	format.value = value
	format.set = true
	return nil
}

const runEventsJSONPrefix = `{"schema_version":1,"type":"audit_run_events","payload":{"events":[`

type jsonEventDocumentWriter struct {
	output  io.Writer
	started bool
	count   int
	closed  bool
	failed  error
}

func (writer *jsonEventDocumentWriter) WriteEvent(event compatibility.Event) error {
	if writer.failed != nil {
		return writer.failed
	}
	if writer.closed {
		return fmt.Errorf("JSON event document is closed")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		writer.failed = err
		return err
	}
	if !writer.started {
		if err := writeAll(writer.output, []byte(runEventsJSONPrefix)); err != nil {
			writer.failed = err
			return err
		}
		writer.started = true
	}
	if writer.count > 0 {
		if err := writeAll(writer.output, []byte(",")); err != nil {
			writer.failed = err
			return err
		}
	}
	if err := writeAll(writer.output, encoded); err != nil {
		writer.failed = err
		return err
	}
	writer.count++
	return nil
}

func (writer *jsonEventDocumentWriter) Close() error {
	if writer.failed != nil {
		return writer.failed
	}
	if writer.closed {
		return nil
	}
	writer.closed = true
	if !writer.started {
		return nil
	}
	if err := writeAll(writer.output, []byte("]}}\n")); err != nil {
		writer.failed = err
		return err
	}
	return nil
}

func writeAll(output io.Writer, contents []byte) error {
	if output == nil {
		return fmt.Errorf("output writer is unavailable")
	}
	for len(contents) > 0 {
		written, err := output.Write(contents)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(contents) {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

func writeJSONLine(output io.Writer, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return writeAll(output, append(encoded, '\n'))
}

type outputGuardedDoer struct {
	delegate compatibility.HTTPDoer
	blocked  func() bool
}

func (doer outputGuardedDoer) Do(request *http.Request) (*http.Response, error) {
	if doer.blocked != nil && doer.blocked() {
		return nil, context.Canceled
	}
	return doer.delegate.Do(request)
}

func runAuditRun(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	flags := flag.NewFlagSet("audit run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	suite := flags.String("suite", "", "case suite: "+strings.Join(apiaudit.SupportedProtocols(), ", "))
	casesRoot := flags.String("cases-root", "cases", "case definition root")
	baseURL := flags.String("base-url", "", "HTTPS gateway base URL")
	model := flags.String("model", "", "model to audit")
	allCases := flags.Bool("all-cases", false, "run every case in the suite")
	allModels := flags.Bool("all-models", false, "run all configured Seedance models")
	dryRun := flags.Bool("dry-run", false, "render requests without network calls")
	confirmPaid := flags.Bool("confirm-paid-suite", false, "confirm a paid-capable live video run")
	noWait := flags.Bool("no-wait", false, "do not poll video tasks to terminal status")
	keyEnv := flags.String("api-key-env", "API_AUDIT_API_KEY", "environment variable containing the bearer key")
	output := flags.String("output", "", "report output directory")
	pollInterval := flags.Duration("poll-interval", 10*time.Second, "video task polling interval")
	timeout := flags.Duration("timeout", 10*time.Minute, "per-case timeout")
	concurrency := flags.Int("concurrency", 1, "maximum concurrent case runs")
	format := runOutputFormatFlag{value: "jsonl"}
	flags.Var(&format, "format", "output format: jsonl, json, or human")
	jsonl := flags.Bool("jsonl", false, "emit canonical machine-readable JSON Lines")
	diagnosticDetail := flags.Bool("diagnostic-detail", false, "include a redacted internal error detail")
	var caseIDs stringListFlag
	flags.Var(&caseIDs, "case", "case ID to run; repeat for multiple cases")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeUsage(stdout, stderr, "Usage: llm-test-studio audit run [options]\n")
		}
		return diagnosticExit(stderr, normalizeFormat(format.value), "usage_error", "invalid audit run options", 2)
	}
	if flags.NArg() != 0 {
		return diagnosticExit(stderr, normalizeFormat(format.value), "usage_error", "invalid audit run options", 2)
	}
	if *jsonl && format.set && format.value != "jsonl" {
		return diagnosticExit(stderr, normalizeFormat(format.value), "usage_error", "--jsonl conflicts with the selected format", 2)
	}
	if !validEnvironmentName(*keyEnv) {
		return diagnosticExit(stderr, format.value, "config_error", "API key environment variable name is invalid", 2)
	}

	apiKey := ""
	if dependencies.getenv != nil {
		apiKey = strings.TrimSpace(dependencies.getenv(*keyEnv))
	}
	runContext, cancelRun := context.WithCancel(nonNilContext(ctx))
	defer cancelRun()
	var outputMu sync.Mutex
	emitErr := error(nil)
	jsonDocument := &jsonEventDocumentWriter{output: stdout}
	emit := func(event compatibility.Event) {
		outputMu.Lock()
		defer outputMu.Unlock()
		if emitErr != nil {
			return
		}
		switch format.value {
		case "human":
			emitErr = emitHumanRunEvent(stdout, event)
		case "json":
			emitErr = jsonDocument.WriteEvent(event)
		case "jsonl":
			emitErr = writeJSONLine(stdout, event)
		}
		if emitErr != nil {
			cancelRun()
		}
	}
	outputFailed := func() bool {
		outputMu.Lock()
		defer outputMu.Unlock()
		return emitErr != nil
	}
	runDependencies := dependencies
	underlyingDoer := runDependencies.applicationDependencies.HTTPDoer
	if underlyingDoer == nil {
		underlyingDoer = runDependencies.httpDoer
	}
	if underlyingDoer == nil {
		underlyingDoer = http.DefaultClient
	}
	runDependencies.applicationDependencies.HTTPDoer = outputGuardedDoer{
		delegate: underlyingDoer,
		blocked:  outputFailed,
	}
	application := newCompatibilityApplication(runDependencies, emit)
	if application == nil {
		return diagnosticExit(stderr, format.value, "core_unavailable", "Go core is unavailable", 1)
	}
	final, err := application.Run(runContext, compatibility.RunRequest{
		Suite:            *suite,
		CasesRoot:        *casesRoot,
		BaseURL:          *baseURL,
		APIKey:           apiKey,
		Model:            strings.TrimSpace(*model),
		CaseIDs:          append([]string(nil), caseIDs...),
		OutputDir:        *output,
		AllCases:         *allCases,
		AllModels:        *allModels,
		DryRun:           *dryRun,
		NoWait:           *noWait,
		ConfirmPaidSuite: *confirmPaid,
		PollInterval:     *pollInterval,
		Timeout:          *timeout,
		Concurrency:      *concurrency,
	})
	outputMu.Lock()
	if format.value == "json" && emitErr == nil {
		emitErr = jsonDocument.Close()
		if emitErr != nil {
			cancelRun()
		}
	}
	outputError := emitErr
	outputMu.Unlock()
	if outputError != nil {
		return diagnosticExit(stderr, format.value, "output_error", "command output could not be written", 1)
	}
	if err != nil {
		switch {
		case compatibility.IsMissingCredentialError(err):
			return diagnosticExitCause(stderr, format.value, "missing_credential", "configured API key is required for a live run", err, *diagnosticDetail, 2)
		case compatibility.IsConfigError(err):
			return diagnosticExitCause(stderr, format.value, "config_error", "audit configuration is invalid", err, *diagnosticDetail, 2)
		case compatibility.IsReportError(err):
			return diagnosticExitCause(stderr, format.value, "report_error", "report could not be written", err, *diagnosticDetail, 1)
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return diagnosticExitCause(stderr, format.value, "canceled", "audit run was canceled", err, *diagnosticDetail, 1)
		default:
			return diagnosticExitCause(stderr, format.value, "run_error", "audit run failed", err, *diagnosticDetail, 1)
		}
	}
	if final.Payload.Summary.Fail > 0 {
		return 1
	}
	return 0
}

func writeUsage(stdout, stderr io.Writer, usage string) int {
	if err := writeAll(stdout, []byte(usage)); err != nil {
		return diagnosticExit(stderr, "json", "output_error", "command output could not be written", 1)
	}
	return 0
}

func normalizeFormat(format string) string {
	if format == "human" {
		return "human"
	}
	return "json"
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func validEnvironmentName(value string) bool {
	if value == "" {
		return false
	}
	for index, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') || character == '_' {
			continue
		}
		if index > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func writeDiagnostic(output io.Writer, format, code, message string) error {
	return writeDiagnosticDetail(output, format, code, message, "")
}

func writeDiagnosticDetail(output io.Writer, format, code, message, detail string) error {
	detail = diagnostics.RedactText(strings.TrimSpace(detail))
	if format == "human" {
		contents := fmt.Sprintf("%s: %s\n", strings.ToUpper(strings.ReplaceAll(code, "_", " ")), message)
		if detail != "" {
			contents += "DETAIL: " + detail + "\n"
		}
		return writeAll(output, []byte(contents))
	}
	encoded, err := json.Marshal(diagnosticResponse{
		SchemaVersion: 1,
		Type:          "error",
		Payload:       diagnosticPayload{Code: code, Message: message, Detail: detail},
	})
	if err != nil {
		return err
	}
	return writeAll(output, append(encoded, '\n'))
}

func diagnosticExit(output io.Writer, format, code, message string, successCode int) int {
	if err := writeDiagnostic(output, format, code, message); err != nil {
		return 1
	}
	return successCode
}

func diagnosticExitCause(output io.Writer, format, code, message string, cause error, includeDetail bool, successCode int) int {
	detail := ""
	if includeDetail && cause != nil {
		detail = cause.Error()
	}
	if err := writeDiagnosticDetail(output, format, code, message, detail); err != nil {
		return 1
	}
	return successCode
}

func emitHumanRunEvent(output io.Writer, event compatibility.Event) error {
	var contents strings.Builder
	switch event := event.(type) {
	case compatibility.PlanEvent:
		fmt.Fprintf(&contents, "PLAN %d case run(s)\n", event.Payload.Total)
		for _, planned := range event.Payload.Runs {
			fmt.Fprintf(&contents, "- %s %s [%s]\n", planned.ID, planned.Model, planned.Kind)
		}
	case compatibility.ProgressEvent:
		fmt.Fprintf(&contents, "DONE %d/%d %s %s (%d ms)\n", event.Payload.Completed, event.Payload.Total, event.Payload.Result.ID, event.Payload.Result.Status, event.Payload.Result.ElapsedMS)
	case compatibility.FinalEvent:
		fmt.Fprintf(&contents, "REPORT %s\n", event.Payload.ReportHTML)
		fmt.Fprintf(&contents, "VERDICT %s\n", event.Payload.Verdict)
	default:
		return nil
	}
	return writeAll(output, []byte(contents.String()))
}

type listPayload struct {
	Command string        `json:"command"`
	Count   int           `json:"count"`
	Cases   []caseSummary `json:"cases"`
}

type caseSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Dimension string `json:"dimension"`
	Protocol  string `json:"protocol"`
	Kind      string `json:"kind"`
	Default   bool   `json:"default"`
	Severity  string `json:"severity"`
}

type listResponse struct {
	SchemaVersion int         `json:"schema_version"`
	Type          string      `json:"type"`
	Payload       listPayload `json:"payload"`
}

func runAuditList(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	ctx = nonNilContext(ctx)
	flags := flag.NewFlagSet("audit list", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	suite := flags.String("suite", "", "case suite: "+strings.Join(apiaudit.SupportedProtocols(), ", "))
	casesRoot := flags.String("cases-root", "cases", "case definition root")
	format := flags.String("format", "json", "output format: json or human")
	diagnosticDetail := flags.Bool("diagnostic-detail", false, "include a redacted internal error detail")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeUsage(stdout, stderr, "Usage: llm-test-studio audit list [options]\n")
		}
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid audit list options", 2)
	}
	if flags.NArg() != 0 {
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid audit list options", 2)
	}
	if *format != "json" && *format != "human" {
		return diagnosticExit(stderr, "json", "usage_error", "invalid audit list format", 2)
	}
	application := newCompatibilityApplication(dependencies, nil)
	if application == nil {
		return diagnosticExit(stderr, *format, "core_unavailable", "Go core is unavailable", 1)
	}
	cases, err := application.List(ctx, compatibility.ListRequest{Suite: *suite, CasesRoot: *casesRoot})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return diagnosticExitCause(stderr, *format, "canceled", "audit list was canceled", err, *diagnosticDetail, 1)
		}
		return diagnosticExitCause(stderr, *format, "config_error", "audit configuration is invalid", err, *diagnosticDetail, 2)
	}
	if *format == "human" {
		var contents strings.Builder
		fmt.Fprintf(&contents, "%-8s %-10s %-12s %s\n", "ID", "DEFAULT", "DIMENSION", "NAME")
		for _, definition := range cases {
			fmt.Fprintf(&contents, "%-8s %-10t %-12s %s\n", definition.ID, definition.Default, definition.Dimension, definition.Name)
		}
		if err := writeAll(stdout, []byte(contents.String())); err != nil {
			return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
		}
		return 0
	}
	response := listResponse{
		SchemaVersion: 1,
		Type:          "audit_case_list",
		Payload:       listPayload{Command: "list", Count: len(cases), Cases: summarizeCases(cases)},
	}
	if err := writeJSONLine(stdout, response); err != nil {
		return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
	}
	return 0
}

func summarizeCases(cases []compatibility.CaseDefinition) []caseSummary {
	summaries := make([]caseSummary, 0, len(cases))
	for _, definition := range cases {
		summaries = append(summaries, caseSummary{
			ID:        definition.ID,
			Name:      definition.Name,
			Dimension: definition.Dimension,
			Protocol:  definition.Protocol,
			Kind:      definition.Kind,
			Default:   definition.Default,
			Severity:  definition.Severity,
		})
	}
	return summaries
}

func newCompatibilityApplication(dependencies dependencies, emit func(compatibility.Event)) compatibilityApplication {
	if dependencies.newApplication == nil {
		return nil
	}
	applicationDependencies := dependencies.applicationDependencies
	if applicationDependencies.HTTPDoer == nil {
		applicationDependencies.HTTPDoer = dependencies.httpDoer
	}
	if emit != nil {
		applicationDependencies.Emit = emit
	}
	return dependencies.newApplication(applicationDependencies)
}

type doctorResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Type          string `json:"type"`
	Payload       struct {
		Status appdoctor.OverallStatus `json:"status"`
		Checks []appdoctor.Check       `json:"checks"`
	} `json:"payload"`
}

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies dependencies) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	casesRoot := flags.String("cases-root", "cases", "case definition root")
	format := flags.String("format", "json", "output format: json or human")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return writeUsage(stdout, stderr, "Usage: llm-test-studio doctor [options]\n")
		}
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid doctor options", 2)
	}
	if flags.NArg() != 0 {
		return diagnosticExit(stderr, normalizeFormat(*format), "usage_error", "invalid doctor options", 2)
	}
	if *format != "json" && *format != "human" {
		return diagnosticExit(stderr, "json", "usage_error", "invalid doctor format", 2)
	}

	application := newCompatibilityApplication(dependencies, nil)
	var catalog appdoctor.Catalog
	if application != nil {
		catalog = appdoctor.CompatibilityCatalog{Lister: application}
	}
	inspection := appdoctor.New(appdoctor.Dependencies{
		FileSystem: dependencies.doctorFileSystem,
		Catalog:    catalog,
	}).Inspect(ctx, appdoctor.InspectRequest{CasesRoot: *casesRoot})

	response := doctorResponse{SchemaVersion: inspection.SchemaVersion, Type: "doctor"}
	response.Payload.Status = inspection.Status
	response.Payload.Checks = inspection.Checks
	if *format == "human" {
		var contents strings.Builder
		fmt.Fprintf(&contents, "Doctor: %s\n", strings.ToUpper(string(inspection.Status)))
		for _, check := range inspection.Checks {
			fmt.Fprintf(&contents, "%-18s %s\n", check.Label, strings.ToUpper(strings.ReplaceAll(string(check.Status), "_", " ")))
		}
		if err := writeAll(stdout, []byte(contents.String())); err != nil {
			return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
		}
	} else if err := writeJSONLine(stdout, response); err != nil {
		return diagnosticExit(stderr, *format, "output_error", "command output could not be written", 1)
	}
	if inspection.Status == appdoctor.OverallFailed {
		return 1
	}
	return 0
}
