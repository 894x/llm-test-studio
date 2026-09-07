# Project Constraints

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
