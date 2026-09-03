# Contributing to LLM Test Studio

Thank you for helping improve reusable, multi-model LLM testing.

## Before you start

- Search existing issues before opening a new one.
- Keep changes focused and avoid committing credentials, production endpoints, personal data, generated reports, or local databases.
- For new built-in cases or media, follow [`cases/PROVENANCE.md`](cases/PROVENANCE.md) and state the source, license, and whether AI-assisted generation was used.
- Discuss large behavior, storage-schema, or public API changes in an issue first.

## Development environment

- Go 1.25+ using the Go 1.26.6 toolchain declared in `go.mod`
- Node.js 24
- pnpm 10.30.3
- Wails CLI 2.15.0

Install and verify the exact Wails CLI version:

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
wails version
```

Install frontend dependencies:

```powershell
cd apps/desktop/frontend
pnpm install --frozen-lockfile
```

## Validate your change

From the repository root:

```powershell
go mod verify
go test ./... -count=1
go vet ./...
```

From `apps/desktop/frontend`:

```powershell
pnpm test
pnpm lint
pnpm build
```

Use `wails build -nosyncgomod` for a desktop package. The `-nosyncgomod` flag prevents the CLI from rewriting pinned module versions.

## Pull requests

- Explain the user-visible problem and the chosen solution.
- Include test evidence and screenshots for visible UI changes.
- Update `README.md` and `README.zh-CN.md` together when changing documented behavior.
- By submitting a contribution, you agree that it is licensed under Apache-2.0 according to section 5 of the license.
