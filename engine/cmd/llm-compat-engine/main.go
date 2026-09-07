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
	"time"

	"github.com/894x/llm-test-studio/internal/application/compatibility"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr, http.DefaultClient))
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer, doer compatibility.HTTPDoer) int {
	return runContext(context.Background(), args, getenv, stdout, stderr, doer)
}

func runContext(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer, doer compatibility.HTTPDoer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: api-audit <list|run> [options]")
		return 2
	}
	switch args[0] {
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		flags.SetOutput(stderr)
		suite := flags.String("suite", "", "case suite: openai-chat, kimi-k3, seedance, wan-video, or minimax-video")
		casesRoot := flags.String("cases-root", "data/cases", "case definition root")
		jsonl := flags.Bool("jsonl", false, "emit machine-readable JSON Lines events")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		application := compatibility.New(compatibility.Dependencies{HTTPDoer: doer})
		cases, err := application.List(ctx, compatibility.ListRequest{Suite: *suite, CasesRoot: *casesRoot})
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
		suite := flags.String("suite", "", "case suite: openai-chat, kimi-k3, seedance, wan-video, or minimax-video")
		casesRoot := flags.String("cases-root", "data/cases", "case definition root")
		baseURL := flags.String("base-url", "", "HTTPS gateway base URL")
		model := flags.String("model", "", "model to audit")
		allCases := flags.Bool("all-cases", false, "run every case in the suite")
		allModels := flags.Bool("all-models", false, "run all configured Seedance models")
		dryRun := flags.Bool("dry-run", false, "render requests without network calls")
		noWait := flags.Bool("no-wait", false, "do not poll video tasks to terminal status")
		keyEnv := flags.String("api-key-env", "API_AUDIT_API_KEY", "environment variable containing the bearer key")
		output := flags.String("output", "", "report output directory")
		pollInterval := flags.Duration("poll-interval", 10*time.Second, "video task polling interval")
		timeout := flags.Duration("timeout", 10*time.Minute, "per-case timeout")
		concurrency := flags.Int("concurrency", 1, "maximum concurrent case runs (OpenAI chat and Kimi-K3 only)")
		jsonl := flags.Bool("jsonl", false, "emit machine-readable JSON Lines events")
		var caseIDs stringListFlag
		flags.Var(&caseIDs, "case", "case ID to run; repeat for multiple cases")
		if err := flags.Parse(args[1:]); err != nil {
			return 2
		}
		apiKey := strings.TrimSpace(getenv(*keyEnv))
		application := compatibility.New(compatibility.Dependencies{
			HTTPDoer: doer,
			Emit: func(event compatibility.Event) {
				emitRunEvent(stdout, *jsonl, event)
			},
		})
		final, err := application.Run(ctx, compatibility.RunRequest{
			Suite: *suite, CasesRoot: *casesRoot, BaseURL: *baseURL, APIKey: apiKey,
			Model: strings.TrimSpace(*model), CaseIDs: append([]string(nil), caseIDs...), OutputDir: *output,
			AllCases: *allCases, AllModels: *allModels, DryRun: *dryRun, NoWait: *noWait,
			PollInterval: *pollInterval, Timeout: *timeout, Concurrency: *concurrency,
		})
		if err != nil {
			switch {
			case compatibility.IsMissingCredentialError(err):
				fmt.Fprintf(stderr, "CONFIG ERROR: environment variable %s is required for a live run\n", *keyEnv)
				return 2
			case compatibility.IsConfigError(err):
				fmt.Fprintln(stderr, "CONFIG ERROR:", err)
				return 2
			case compatibility.IsReportError(err):
				fmt.Fprintln(stderr, "REPORT ERROR:", err)
				return 1
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				fmt.Fprintln(stderr, "RUN ERROR:", err)
				return 1
			default:
				fmt.Fprintln(stderr, "RUN ERROR:", err)
				return 1
			}
		}
		if final.Payload.Summary.Fail > 0 {
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

func emitRunEvent(output io.Writer, jsonl bool, event compatibility.Event) {
	if jsonl {
		emitJSON(output, legacyJSONEvent(event))
		return
	}
	switch event := event.(type) {
	case compatibility.PlanEvent:
		fmt.Fprintf(output, "PLAN %d case run(s)\n", event.Payload.Total)
		for _, planned := range event.Payload.Runs {
			fmt.Fprintf(output, "- %s %s [%s]\n", planned.ID, planned.Model, planned.Kind)
		}
	case compatibility.ProgressEvent:
		fmt.Fprintf(output, "DONE %d/%d %s %s (%d ms)\n", event.Payload.Completed, event.Payload.Total, event.Payload.Result.ID, event.Payload.Result.Status, event.Payload.Result.ElapsedMS)
	case compatibility.FinalEvent:
		fmt.Fprintf(output, "REPORT %s\n", event.Payload.ReportHTML)
		fmt.Fprintf(output, "VERDICT %s\n", event.Payload.Verdict)
	}
}

func legacyJSONEvent(event compatibility.Event) any {
	switch event := event.(type) {
	case compatibility.PlanEvent:
		runs := make([]map[string]any, 0, len(event.Payload.Runs))
		for _, planned := range event.Payload.Runs {
			runs = append(runs, map[string]any{
				"id": planned.ID, "case_id": planned.CaseID, "name": planned.Name,
				"dimension": planned.Dimension, "kind": planned.Kind, "model": planned.Model,
			})
		}
		return map[string]any{"type": "plan", "total": event.Payload.Total, "runs": runs}
	case compatibility.ProgressEvent:
		return map[string]any{
			"type": "progress", "completed": event.Payload.Completed, "total": event.Payload.Total,
			"result": event.Payload.Result,
		}
	case compatibility.FinalEvent:
		return map[string]any{
			"type": "final", "command": event.Payload.Command, "report_dir": event.Payload.ReportDir,
			"report_json": event.Payload.ReportJSON, "report_html": event.Payload.ReportHTML,
			"overall": event.Payload.Overall, "verdict": event.Payload.Verdict, "summary": event.Payload.Summary,
		}
	default:
		return event
	}
}
