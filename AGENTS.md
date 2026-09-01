# Project Constraints

## Wails version

- The desktop application is pinned to `github.com/wailsapp/wails/v2 v2.15.0`.
- Do not upgrade, downgrade, or otherwise change the Wails module version unless the user explicitly requests that exact version change.
- All Wails development, build, binding-generation, and verification commands must use Wails CLI `v2.15.0`.
- Verify the CLI with `wails version` before running a Wails command. Do not use a mismatched CLI because it may rewrite `go.mod` and `go.sum`.
