---
name: git-commit-message-template
description: Standardize git commit preparation and commit messages using the user's established categorized template. Use when Codex is asked to stage, commit, organize commits, write commit messages, split changes into commits, or follow the user's historical git message format.
---

# Git Commit Message Template

## Workflow

Before committing, inspect the staged and unstaged changes. Stage only the files that belong to the requested change. Do not include unrelated untracked files or unrelated user edits.

Prefer one focused commit when the changes are one coherent unit. Split into multiple commits only when the staged changes represent clearly independent concerns.

Run relevant tests before committing when practical. Mention any skipped or failing validation in the final response.

## Message Shape

Use this structure:

```text
type(scope): concise imperative summary

Category
- detail
- detail

Category
- detail

Tests
- validation command or coverage note

Docs
- changelog or documentation updates
```

Use `type(scope): summary` for the subject. Keep the subject concise and specific. Use lowercase conventional types such as `feat`, `fix`, `perf`, `refactor`, `docs`, `test`, or `chore`. For this repository, prefer `town-editor` as scope when committing `town-editor/` changes.

Use categorized body sections matching the actual work. Common categories:

- `Feat`: new behavior or user-facing capability
- `Fix`: bug fixes or correctness changes
- `Perf`: performance and invalidation improvements
- `Refactor`: internal restructuring without intended behavior change
- `Tests`: tests added or commands run
- `Docs`: changelog or documentation changes
- `UI`: user interface behavior or presentation changes
- `Style`: visual styling-only changes

Do not add empty categories. Do not invent categories that are not supported by the changes.

## Detail Rules

Write body bullets in plain English, following the existing repository style. Keep bullets concrete:

- say what changed and why it matters
- name important subsystems, not every touched file
- mention test coverage in `Tests`
- mention changelog updates in `Docs`

Do not include implementation noise, temporary experiments, or reverted work unless that history is intentionally relevant to the final commit.

## Example

```text
perf(town-editor): narrow editor scene dirty invalidation

Perf
- split terrain content invalidation into a dedicated terrain dirty kind so terrain, infinite, and tile map updates avoid grid, placement, and ghost redraws
- mark high-frequency interaction paths with terrain, furniture, and hover dirty kinds
- extend scene keys with map size, tile size, render style, and transform dependencies while keeping structural changes on full redraw

Fix
- remove the extra viewport controller full-dirty mark after applying viewport transforms
- narrow Pixi projection backend managed child lookup to Sprite | Graphics

Tests
- cover terrain, hover, and furniture dirty isolation in SceneLayerRenderers tests

Docs
- append 2026-05-20 town-editor changelog entries
```

## Final Response

After a successful commit, report the commit hash and subject. If files were staged or committed, emit the relevant Codex git directives in the final answer.
