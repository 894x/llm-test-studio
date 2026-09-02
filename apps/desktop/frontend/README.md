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

The production bundle expects the Wails shell to provide `GetWorkspace`, `StartRun`, `StopSending`, and `CancelRun`. Missing bindings or invalid responses are reported as errors; the UI does not substitute fixture data or manufacture successful state transitions.
