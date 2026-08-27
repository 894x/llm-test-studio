package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llm-test/engine/apiaudit"
)

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

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, doer apiaudit.HTTPDoer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: api-audit <list|run> [options]")
		return 2
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		suite := flags.String("suite", "", "case suite: openai-chat, kimi-k3, or seedance")
		casesRoot := flags.String("cases-root", "cases", "case definition root")
		jsonl := flags.Bool("jsonl", false, "emit machine-readable JSON Lines events")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		cases, err := apiaudit.LoadSuite(*casesRoot, *suite)
		if err != nil {
			fmt.Fprintln(stderr, "CONFIG ERROR:", err)
			return 2
		}
		if !*jsonl {
			fmt.Fprintf(stdout, "%-8s %-10s %-12s %s\n", "ID", "DEFAULT", "DIMENSION", "NAME")
		}
		for _, definition := range cases {
			if *jsonl {
				emitJSON(stdout, map[string]any{"type": "case", "case": definition})
			} else {
				fmt.Fprintf(stdout, "%-8s %-10t %-12s %s\n", definition.ID, definition.Default, definition.Dimension, definition.Name)
			}
		}
		if *jsonl {
			emitJSON(stdout, map[string]any{"type": "final", "command": "list", "count": len(cases)})
		}
		return 0

	case "run":
		flags := flag.NewFlagSet("run", flag.ContinueOnError)
		flags.SetOutput(stderr)
		suite := flags.String("suite", "", "case suite: openai-chat, kimi-k3, or seedance")
		casesRoot := flags.String("cases-root", "cases", "case definition root")
		baseURL := flags.String("base-url", "", "HTTPS gateway base URL")
		model := flags.String("model", "", "model to audit")
		allCases := flags.Bool("all-cases", false, "run every case in the suite")
		allModels := flags.Bool("all-models", false, "run all configured Seedance models")
		dryRun := flags.Bool("dry-run", false, "render requests without network calls")
		confirmPaid := flags.Bool("confirm-paid-suite", false, "confirm a multi-task live Seedance run")
		noWait := flags.Bool("no-wait", false, "do not poll Seedance tasks to terminal status")
		keyEnv := flags.String("api-key-env", "API_AUDIT_API_KEY", "environment variable containing the bearer key")
		output := flags.String("output", "", "report output directory")
		pollInterval := flags.Duration("poll-interval", 10*time.Second, "Seedance polling interval")
		timeout := flags.Duration("timeout", 10*time.Minute, "per-case timeout")
		concurrency := flags.Int("concurrency", 1, "maximum concurrent case runs (OpenAI chat and Kimi-K3 only)")
		jsonl := flags.Bool("jsonl", false, "emit machine-readable JSON Lines events")
		var caseIDs stringListFlag
		flags.Var(&caseIDs, "case", "case ID to run; repeat for multiple cases")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		if *suite != "openai-chat" && *suite != "kimi-k3" && *suite != "seedance" {
			fmt.Fprintln(stderr, "CONFIG ERROR: --suite must be openai-chat, kimi-k3, or seedance")
			return 2
		}
		parsedBase, err := url.Parse(*baseURL)
		if err != nil || parsedBase.Scheme != "https" || parsedBase.Host == "" {
			fmt.Fprintln(stderr, "CONFIG ERROR: --base-url must be an absolute HTTPS URL")
			return 2
		}
		if *allModels && *suite != "seedance" {
			fmt.Fprintln(stderr, "CONFIG ERROR: --all-models is only valid for seedance")
			return 2
		}
		if *suite == "openai-chat" && strings.TrimSpace(*model) == "" {
			fmt.Fprintln(stderr, "CONFIG ERROR: --model is required for openai-chat")
			return 2
		}
		if *timeout <= 0 {
			fmt.Fprintln(stderr, "CONFIG ERROR: --timeout must be positive")
			return 2
		}
		if *pollInterval <= 0 {
			fmt.Fprintln(stderr, "CONFIG ERROR: --poll-interval must be positive")
			return 2
		}
		if *concurrency < 1 || *concurrency > 32 {
			fmt.Fprintln(stderr, "CONFIG ERROR: --concurrency must be between 1 and 32")
			return 2
		}
		if *suite == "seedance" && *concurrency != 1 {
			fmt.Fprintln(stderr, "CONFIG ERROR: --concurrency is not supported for seedance")
			return 2
		}
		apiKey := strings.TrimSpace(getenv(*keyEnv))
		if !*dryRun && apiKey == "" {
			fmt.Fprintf(stderr, "CONFIG ERROR: environment variable %s is required for a live run\n", *keyEnv)
			return 2
		}
		cases, err := apiaudit.LoadSuite(*casesRoot, *suite)
		if err != nil {
			fmt.Fprintln(stderr, "CONFIG ERROR:", err)
			return 2
		}
		selected, err := apiaudit.SelectCases(cases, caseIDs, *allCases)
		if err != nil {
			fmt.Fprintln(stderr, "CONFIG ERROR:", err)
			return 2
		}
		config := apiaudit.RunConfig{
			Suite: *suite, BaseURL: *baseURL, APIKey: apiKey, Model: strings.TrimSpace(*model),
			DryRun: *dryRun, NoWait: *noWait, ConfirmPaidSuite: *confirmPaid,
			PollInterval: *pollInterval, Timeout: *timeout,
		}
		if *suite == "kimi-k3" && config.Model == "" {
			config.Model = apiaudit.DefaultKimiK3Model
		}
		if *suite == "seedance" {
			if config.Model == "" {
				config.Model = apiaudit.DefaultSeedanceModel
			}
			if *allModels {
				config.Models = append([]string(nil), apiaudit.DefaultSeedanceModels...)
			}
		}
		runs, err := apiaudit.ExpandRuns(config, selected)
		if err != nil {
			fmt.Fprintln(stderr, "CONFIG ERROR:", err)
			return 2
		}
		if *jsonl {
			plannedEvents := make([]map[string]any, 0, len(runs))
			for _, planned := range runs {
				plannedEvents = append(plannedEvents, map[string]any{
					"id": planned.ResultID, "case_id": planned.Case.ID, "name": planned.Case.Name,
					"dimension": planned.Case.Dimension, "kind": planned.Case.Kind, "model": planned.Model,
				})
			}
			emitJSON(stdout, map[string]any{"type": "plan", "total": len(runs), "runs": plannedEvents})
		} else {
			fmt.Fprintf(stdout, "PLAN %d case run(s)\n", len(runs))
			for _, planned := range runs {
				fmt.Fprintf(stdout, "- %s %s [%s]\n", planned.ResultID, planned.Model, planned.Case.Kind)
			}
		}

		results := make([]apiaudit.CaseResult, 0, len(runs))
		if config.DryRun {
			for index, planned := range runs {
				if config.DryRun && (config.Suite == "openai-chat" || config.Suite == "kimi-k3") {
					result := apiaudit.CaseResult{
						ID: planned.ResultID, Name: planned.Case.Name, Dimension: planned.Case.Dimension,
						Protocol: planned.Case.Protocol, Model: planned.Model, Status: apiaudit.StatusUnknown,
						Severity: planned.Case.Severity, Evidence: "dry-run: request was not submitted",
					}
					if planned.Case.Kind == "manual_unknown" {
						result.Evidence = "dry-run: manual/externally-instrumented case has no HTTP request"
					} else {
						body := make(map[string]any, len(planned.Case.Request.Body)+1)
						for key, value := range planned.Case.Request.Body {
							body[key] = value
						}
						if planned.Case.Kind != "models_contains" {
							body["model"] = planned.Model
						}
						if len(body) == 0 {
							body = nil
						}
						path := planned.Case.Request.Path
						if !strings.HasPrefix(path, "/") {
							path = "/" + path
						}
						result.Exchanges = []apiaudit.HTTPExchange{{Method: planned.Case.Request.Method, URL: strings.TrimRight(config.BaseURL, "/") + path, RequestBody: body}}
					}
					results = append(results, result)
					emitProgress(stdout, *jsonl, index+1, len(runs), result, config.APIKey)
					continue
				}
				result := runPlannedCase(doer, config, planned)
				results = append(results, result)
				emitProgress(stdout, *jsonl, index+1, len(runs), result, config.APIKey)
			}
		} else {
			type indexedResult struct {
				index  int
				result apiaudit.CaseResult
			}
			workerCount := min(*concurrency, len(runs))
			jobs := make(chan int)
			completed := make(chan indexedResult, len(runs))
			var workers sync.WaitGroup
			workers.Add(workerCount)
			for range workerCount {
				go func() {
					defer workers.Done()
					for index := range jobs {
						completed <- indexedResult{index: index, result: runPlannedCase(doer, config, runs[index])}
					}
				}()
			}
			go func() {
				for index := range runs {
					jobs <- index
				}
				close(jobs)
				workers.Wait()
				close(completed)
			}()

			results = make([]apiaudit.CaseResult, len(runs))
			completedCount := 0
			for outcome := range completed {
				results[outcome.index] = outcome.result
				completedCount++
				emitProgress(stdout, *jsonl, completedCount, len(runs), outcome.result, config.APIKey)
			}
		}
		if *output == "" {
			*output = filepath.Join("output", "api-audit", time.Now().Format("20060102-150405"))
		}
		displayConfig := config
		if len(config.Models) > 1 {
			displayConfig.Model = strings.Join(config.Models, ", ")
		}
		report := apiaudit.BuildReport(displayConfig, results)
		if err := apiaudit.WriteReport(*output, report); err != nil {
			fmt.Fprintln(stderr, "REPORT ERROR:", err)
			return 1
		}
		if *jsonl {
			emitJSON(stdout, map[string]any{
				"type": "final", "command": "run", "report_dir": *output,
				"report_json": filepath.Join(*output, "report.json"),
				"report_html": filepath.Join(*output, "report.html"),
				"overall":     report.Overall, "verdict": report.Verdict, "summary": report.Summary,
			})
		} else {
			fmt.Fprintf(stdout, "REPORT %s\n", filepath.Join(*output, "report.html"))
			fmt.Fprintf(stdout, "VERDICT %s\n", report.Verdict)
		}
		if report.Summary.Fail > 0 {
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, "unknown command:", args[0])
		return 2
	}
}

func emitJSON(output io.Writer, event any) {
	if envelope, ok := event.(map[string]any); ok {
		if _, exists := envelope["schema_version"]; !exists {
			envelope["schema_version"] = 1
		}
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return
	}
	fmt.Fprintln(output, string(encoded))
}

func emitProgress(output io.Writer, jsonl bool, completed, total int, result apiaudit.CaseResult, apiKey string) {
	if jsonl {
		emitJSON(output, map[string]any{
			"type": "progress", "completed": completed, "total": total,
			"result": apiaudit.RedactCaseResult(result, apiKey),
		})
		return
	}
	fmt.Fprintf(output, "DONE %d/%d %s %s (%d ms)\n", completed, total, result.ID, result.Status, result.ElapsedMS)
}

func runPlannedCase(doer apiaudit.HTTPDoer, config apiaudit.RunConfig, planned apiaudit.PlannedRun) apiaudit.CaseResult {
	caseContext, cancelCase := context.WithTimeout(context.Background(), config.Timeout)
	defer cancelCase()
	if config.Suite == "seedance" {
		return apiaudit.RunSeedanceCase(caseContext, doer, config, planned)
	}
	caseConfig := config
	caseConfig.Model = planned.Model
	if config.Suite == "kimi-k3" {
		result := apiaudit.RunKimiK3Case(caseContext, doer, caseConfig, planned.Case)
		result.ID = planned.ResultID
		return result
	}
	result := apiaudit.RunOpenAIChatCase(caseContext, doer, caseConfig, planned.Case)
	result.ID = planned.ResultID
	return result
}
