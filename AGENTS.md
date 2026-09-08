# Project Constraints

## Development-stage policy: no backward-compatibility code

- This project is in active development. Maintain only the current contract and one implementation path. Do not introduce backward-compatibility code or retain a superseded implementation in the affected scope. Breaking changes are expected; do not propose compatibility layers as a solution.
- Compatibility code means logic whose sole purpose is to keep superseded project APIs, fields, formats, configuration, storage layouts, or callers working. This includes legacy aliases, deprecated wrappers, old/new dual reads or writes, fallback to removed fields or paths, version-detection branches, and feature flags that retain an old implementation.
- When changing a contract, update all affected in-repository producers, consumers, bindings, fixtures, authored definitions, tests, and documentation in the same change. Replace the old path and remove obsolete code within the affected scope; do not add an adapter merely to avoid updating callers or tests. Do not expand a focused task into unrelated legacy cleanup.
- Update affected callers to the current contract, including known external integration points where they are within scope. Report any callers that cannot be updated in this change as outstanding upgrade requirements; do not accommodate them with a legacy runtime path. Old installations, historical data, and existing legacy code are not reasons to add compatibility.
- Unsupported old input or persisted formats must fail explicitly with an actionable error. Do not silently reinterpret them, default away a version mismatch, or retry through a legacy path. This rule does not authorize deleting, overwriting, or resetting user data.
- When historical data or code needs upgrading, write a temporary, separately invoked script to convert or refactor the previous version directly to the current version. Define the source and target versions, validate inputs before writing, and verify the converted result against the current contract. Preserve recoverability for persisted user data and do not overwrite unrelated work.
- Keep upgrade scripts outside application startup, builds, and normal runtime reads and writes. Do not ship them as permanent migration infrastructure, add automatic migration/import-on-read, or make the current application depend on them. Remove temporary scripts and artifacts after the required upgrade and verification are complete; if execution is still pending, report their location and remaining work explicitly.
- Current, intentionally supported provider protocols, platform integrations, and their explicit boundary adapters are product functionality. Documented optional values and normal resilience behavior are also allowed. They must not be used to disguise support for superseded internal contracts.

### Strict prohibition on version compatibility

- Do not introduce support for multiple historical versions of a project-owned contract. This applies to APIs, DTOs, bindings, case/suite/plan definitions, configuration, serialized snapshots, reports, and database schemas. Replace the affected contract in place and update its consumers together.
- Do not add parallel `V1`/`V2` implementations, version-indexed parser/renderer/handler registries, `if version < ...` behavior branches, legacy DTOs, compatibility modes, or fallback to an older version. The prohibition applies equally when versions are inferred from field presence, missing metadata, file paths, or object shape instead of an explicit version number.
- Do not turn a missing or unknown version into a supported legacy/default version, negotiate down to an older contract, or retry another decoder after current-format validation fails. Reject unsupported formats explicitly without changing their data.
- A version marker may identify and validate the single current format; it must not select historical implementations. Do not add a new version identifier merely to retain the previous implementation. Existing version numbers do not imply an obligation to support older versions.
- Historical conversions belong exclusively in temporary, explicitly invoked upgrade scripts as described above. Do not add runtime migration chains, persistent version adapters, or historical format readers to application code, even if described as temporary, defensive, or required to keep existing tests passing.
- Supporting an external provider's currently required protocol does not authorize version compatibility for project-owned contracts. Keep external protocol details at their boundary and use the single current internal representation.

### Required verification for contract changes

1. Identify the current contract and affected callers/data before editing; resolve references to the replaced symbols, fields, formats, and paths within the affected scope.
2. Verify that callers and tests use the new contract and that no obsolete alias, wrapper, fallback, dual path, or unused compatibility flag remains in that scope. Update obsolete tests instead of retaining old behavior to make them pass.
   Inspect added version markers, version dispatch, shape detection, and migration references for hidden compatibility paths. Any new runtime path that accepts a superseded project format or selects its old behavior is a blocking defect and must be removed before completion.
3. Run checks appropriate to the affected behavior. When an input or storage format changes, verify that unsupported old formats are rejected clearly without mutating rejected data.
4. In the completion report, state breaking contract changes, historical-data impact, upgrade-script execution and verification results when applicable, and any outstanding upgrades. Do not claim an upgrade has been completed if only the script has been written.

## Wails version

- The desktop application is pinned to `github.com/wailsapp/wails/v2 v2.15.0`.
- Do not upgrade, downgrade, or otherwise change the Wails module version unless the user explicitly requests that exact version change.
- All Wails development, build, binding-generation, and verification commands must use Wails CLI `v2.15.0`.
- Verify the CLI with `wails version` before running a Wails command. Do not use a mismatched CLI because it may rewrite `go.mod` and `go.sum`.

## Repository skills

Project-local skills live in `.agents/skills/<skill-name>/SKILL.md`. Use this directory as the source of truth for available repository skills.

### Discovery and use

1. Before task-specific work, inspect the local skill names and descriptions and select the skills whose scope matches the task. Use the table below as a starting point; check the directory for newly added skills as well.
2. If the user explicitly names a skill, read its `SKILL.md`. Otherwise, proactively read applicable skills before designing, editing, reviewing, or running the relevant workflow. Select the smallest set that covers the task; do not load every skill for every request.
3. Briefly state which skills you are using and why when first applying them. Read the full selected `SKILL.md`, then follow its workflow and validation requirements within the authorized task scope.
4. Resolve relative references from the skill's own directory. Read referenced documents when the skill requires them for the current task, and reuse relevant scripts or templates instead of recreating them. Do not load unrelated reference files in bulk.
5. Combine skills when their responsibilities overlap: for example, a desktop UI change involving Go bindings may need the UI design, Wails, and Go code style skills. README synchronization and commit preparation apply when those activities are part of the task.
6. Skills do not override higher-priority instructions, the user's explicit scope, or applicable `AGENTS.md` constraints. Project-specific guidance takes precedence over generic skill defaults; in particular, the Wails version constraint above applies to all Wails skill examples and commands.
7. If a skill or required reference is missing, report the specific path and use the available project guidance where possible. Ask for clarification only when the missing information prevents correct completion. Do not claim to have followed a skill you could not read.

### Skill selection

| Task | Skill entry point |
| --- | --- |
| Design or audit external API contracts, parameter boundaries, test case catalogs, or scenario suites; not ordinary unit tests without an external API contract | [api-boundary-test-case-design](.agents/skills/api-boundary-test-case-design/SKILL.md) |
| Create, change, review, or visually verify desktop pages, forms, tables, navigation, themes, or shared UI components under `apps/desktop/frontend` | [llm-test-studio-ui-design](.agents/skills/llm-test-studio-ui-design/SKILL.md) |
| Develop or debug Wails lifecycle, Go/frontend bindings, runtime integration, or desktop builds; use the v2 guidance with the pinned version above | [wails](.agents/skills/wails/SKILL.md) |
| Write or review Go code for style and clarity | [golang-code-style](.agents/skills/golang-code-style/SKILL.md) |
| Write, review, debug, or test Go database access, queries, transactions, or connection handling | [golang-database](.agents/skills/golang-database/SKILL.md) |
| Choose Go architecture or design constructors, resource lifecycles, shutdown, timeouts, or retry patterns | [golang-design-patterns](.agents/skills/golang-design-patterns/SKILL.md) |
| Add, edit, rename, or delete root or nested README content and synchronize its English/Chinese counterpart; not application UI localization | [i18n-readme-sync](.agents/skills/i18n-readme-sync/SKILL.md) |
| Stage or commit changes, split commits, or prepare commit messages when requested | [git-commit-message-template](.agents/skills/git-commit-message-template/SKILL.md) |

When adding, renaming, or removing a repository skill, update this table in the same change. Keep detailed procedures in the skill's `SKILL.md` and references rather than duplicating them here.
