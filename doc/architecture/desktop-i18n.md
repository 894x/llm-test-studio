# Desktop internationalization

The desktop supports `zh-CN`, `en-US`, and a persisted system-language preference.
`LanguageProvider` owns the active locale; changing it updates the document
language and React translations without reloading Core data or repeating a save.

## Presentation boundaries

- Translate interface labels, accessibility names, known error codes, registered
  case types, and system-generated report conclusions.
- Preserve authored model/channel/case/plan names, upstream model IDs, custom
  report conclusions, request evidence, and machine-readable snapshots.
- React components use `useTranslation`. Formatting and validation helpers use
  `desktopLocale` and `translateDesktop` at call time, not module initialization.
- Protocol parsers throw `DesktopDataError`. Error classification must never
  depend on the wording or language of the error message.
- Existing UI errors retained in state can use `localizeStoredMessage` at render
  time. It recognizes known bilingual resource templates, with specific
  templates ordered before generic ones. Never apply it to user content or
  response evidence. New complex error flows should retain keys and parameters.

## Coverage added when integrating the desktop i18n branch

The integration preserves main's catalog validation and recovery behavior and
adds translations for the model/channel matrix, case type and model-target
controls, input latency ladders, paid-video confirmation, workload and arrival
settings, warmup/ramp phases, SLO/capacity ladders, request evidence analysis,
response probe distributions, and v3 streaming timing tables.

Supplemental keys are in `src/i18n/resources/{zh-CN,en-US}/desktop.json` under the
frontend. Both locales must contain the same keys and interpolation parameters.
The other resources retain the original feature namespaces.

Report exports and native save dialogs receive the active locale. Browser
HTML/PNG/PDF exports share the localized report DOM. Go HTML exports localize
headings, table labels and statuses while preserving optional v3 measurements
and older report JSON compatibility. Machine-readable exports preserve data.

## Verification

Run from `apps/desktop/frontend`:

```powershell
pnpm install --frozen-lockfile
pnpm i18n:check
pnpm test
pnpm lint
pnpm build
```

Run `go test ./... -count=1` and `go vet ./...` at the repository root. Before
packaging, confirm `wails version` reports **v2.15.0**. A build using the already
verified frontend is `wails build -s -m -nosyncgomod` from `apps/desktop`.

Regression tests cover English invalid-payload diagnostics, bilingual validation
errors and accessibility associations, preserving an open request selection on
language changes, model/channel names in the translated matrix, export locale
forwarding, HTML localization, and schema-v3 missing measurement semantics.
