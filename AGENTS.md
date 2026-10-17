# Project Constraints

## Public-release compatibility policy

- This project is publicly released. Preserve supported user-facing contracts and user data when upgrading. Follow [Compatibility and data upgrades](doc/compatibility-policy.md); the previous development-stage prohibition on compatibility code and runtime migrations no longer applies.
- Protected boundaries include documented CLI commands/options/exit codes/machine output, public APIs and desktop/remote-runner protocols, user configuration, authored Case/Suite/Plan files, persisted snapshots/reports, and database contents. Identify the published versions and consumers affected before changing them.
- Keep one current internal domain model and business implementation. Internal functions, package layout and bindings shipped together may be refactored with their callers; they do not need historical aliases unless they cross a supported external or persisted boundary.
- Compatibility adapters, deprecated aliases, versioned boundary decoders and migration chains are allowed when required by a supported published contract. Keep them at explicit boundaries, document their supported versions and removal conditions, and test them. Do not scatter historical-version branches through business logic or add speculative compatibility for unpublished formats.
- Update current in-repository producers, consumers, bindings, fixtures and documentation together. Retain fixtures and tests for supported published formats; do not replace them merely to make a breaking implementation pass.
- Breaking changes require a documented release/version decision, deprecation or transition path, upgrade instructions and verification. Being open source or using a `0.x` version does not authorize silent breakage or data loss.

### Data migrations shipped with the application

- Implement routine upgrades in version-controlled migration code shipped with the application. On opening a workspace, perform version preflight and any supported migration before repositories, background jobs or other writes start. Desktop, CLI and runner entry points must share the applicable migration implementation.
- Keep ordered, immutable migrations and an explicit schema/format version record. Never rewrite a released baseline or checksum to represent a new schema. Retain all migration steps needed for the documented upgrade paths, including skipped application releases.
- Validate the source version and integrity, prevent concurrent writers, create and verify a recoverable backup before mutation, then migrate and validate the target. Use transactions for database changes and staged/atomic replacement for files; multi-resource upgrades need explicit recovery coordination.
- Failed or interrupted upgrades must stop normal workspace use and leave a recoverable state. Reopening must safely resume or report recovery instructions; already completed steps must not be applied twice. Never reset, delete, recreate or silently discard user data to make an upgrade succeed.
- Preserve record identities, relationships, credential references, authored content and historical report/snapshot meaning. Do not recompute historical test conclusions using current rules. Do not copy secrets into logs or fixtures.
- Unknown, corrupt, unsupported or newer formats must fail clearly without modifying rejected data. Do not guess a missing version or try arbitrary fallback decoders. A published unversioned format needs a documented, unambiguous recognition rule and a tested migration.
- Routine supported upgrades must not depend on a temporary script, a developer's local files, or asking users to delete their database. Exceptional manual repair/conversion tools may supplement the upgrade path but must document scope, backup, recovery and verification. Do not remove shipped migrations while supported installations still need them.
- Downgrades are separate from upgrades: do not assume an older binary can read upgraded data. Document whether rollback requires restoring the pre-upgrade backup and matching binary; disclose any loss of post-upgrade writes.

### Required verification for contract changes

1. Inventory affected published contracts, source versions, callers and stored data; distinguish public/persisted boundaries from private implementation details.
2. Verify supported old inputs/clients still work through a bounded adapter or a tested migration into the current model. Document unsupported versions and any planned removal; preserve historical regression fixtures.
3. For storage changes, test fresh initialization, each supported source version, skipped-release upgrades, repeat open, failure/interruption recovery, backup restoration, preservation of data semantics, and rejection of newer/unknown/corrupt formats without mutation. Also test concurrent-open protection and cross-resource recovery where applicable.
4. Include compatibility impact, deprecation/removal timing, supported upgrade paths, downgrade limitations and actual validation evidence in the change and release notes. Do not claim migration is implemented or a user's data upgraded based only on documentation or a prepared script.

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
