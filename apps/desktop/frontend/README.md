# LLM Test Studio frontend

[简体中文](README.zh-CN.md)

This package is the React presentation layer for the local LLM Test Studio desktop application. It renders the compact run workspace, collects user intent, and calls Wails-generated bindings. Domain rules, persistence, credentials, execution, and authoritative run state remain in the Go Core.

## Development

Install the pinned dependencies and start Vite:

```bash
pnpm install --frozen-lockfile
pnpm dev
```

The Vite development build may load local fixtures when no Wails runtime is present. Fixtures are development-only and are excluded from production bundles.

## Validation

Run the frontend checks before submitting changes:

```bash
pnpm test
pnpm lint
pnpm build
```

## Production boundary

The production bundle expects the current Wails bindings, including `GetWorkspace`, `GetReports`, `GetReportGeneration`, `StartRunTarget`, `StopSending`, and `CancelRun`. Missing bindings or invalid responses are reported as errors; the UI does not substitute fixture data or manufacture successful state transitions.

Report generation publishes `report-generation-progress` events. The UI displays the actual phase, uses processed/total counts only during aggregation, and refreshes the report list after durable persistence. `GetReportGeneration` supplies the current in-memory phase snapshot when the UI subscribes; generating a report does not trigger repeated full report-list polling.
