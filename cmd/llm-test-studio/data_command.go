package main

import (
	"context"
	"flag"
	"io"

	persistence "github.com/894x/llm-test-studio/internal/persistence/sqlite"
)

const dataUsage = `Usage: llm-test-studio data export-upgrade-history --database <file> --output <new.jsonl>

Export read-only historical job records preserved during a database upgrade.
The database must already be upgraded. Existing output files are never replaced.
`

func runData(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && (args[0] == "help" || args[0] == "--help" || args[0] == "-h") {
		return writeUsage(stdout, stderr, dataUsage)
	}
	if len(args) == 0 || args[0] != "export-upgrade-history" {
		return diagnosticExit(stderr, "json", "usage_error", "expected data export-upgrade-history", 2)
	}
	flags := flag.NewFlagSet("data export-upgrade-history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var database, output string
	flags.StringVar(&database, "database", "", "upgraded database path")
	flags.StringVar(&output, "output", "", "new private JSONL archive")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || database == "" || output == "" {
		return diagnosticExit(stderr, "json", "usage_error", "database and output paths are required", 2)
	}
	if err := persistence.ExportUpgradeHistory(ctx, database, output); err != nil {
		return diagnosticExit(stderr, "json", "history_export_failed", "history export failed; source was not modified and existing output was not replaced", 1)
	}
	return 0
}
