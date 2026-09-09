# MiniMax H3 video generation API boundary cases

[English](README.en.md) | [简体中文](README.md)

The catalog targets MiniMax-H3 native video creation and polling. Provider constraints in the accompanying matrix were retrieved on 2026-09-05; the runtime integration does not revalidate those external constraints.

## Current catalog

The 149 Cases use envelope schema 3 and explicit protocol assertions: 43 enabled automatic Cases, 101 manual Cases requiring deterministic media or paid generation, and 5 disabled model/authentication mutation Cases. Model and credentials belong to the Run binding. The disabled definitions preserve the uncovered claims but cannot execute in current Suites.

Manual `{{H3_*}}` markers describe fixture prerequisites; replace them with controlled media through authored inputs before enabling execution. Do not invent provider assets.

## Scenario Suites

| Profile | Cases | Requirements |
|---|---:|---|
| Connectivity | 1 | Creation, polling and terminal success; one paid generation |
| Basic functionality | 21 | Five automatic and 16 manual Cases; fixtures and budget |
| Parameter rejection | 42 | Explicit rejection assertions; no model/auth mutation |
| Automatic core regression | 43 | Minimal success and parameter rejection |
| Complete video boundaries | 144 | 43 automatic and 101 manual Cases |

Suite schema 2 stores ordered Case IDs and explicit input mappings. The checked-in [profile manifest](../../suites/minimax-video/MiniMax-H3.suite-profiles.json) is the source for generated membership:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases/minimax-video --suites-root data/suites/minimax-video --manifest data/suites/minimax-video/MiniMax-H3.suite-profiles.json --check
```

Use `--write` after reviewing intentional manifest changes. The generator rejects old kind selectors and model routing fields.

## Assertions and execution

Assertions explicitly check HTTP rejection details or a terminal task, its identity and output URL. HTTP 200 or task admission alone is insufficient; empty assertions mean observation only. Waiting belongs to the Case workflow. Review individual current definitions for exact field assertions.

CLI `--dry-run` prepares enabled automatic requests without network calls; it cannot prove provider behavior. Live execution incurs generation charges. This integration used local HTTPS fixtures, current schema checks and unit/E2E tests; it made no live MiniMax calls.

## Official references

- [Create a video generation task](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-create)
- [Query a task](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-query)
- [Video V2 OpenAPI](https://platform.minimaxi.com/docs/api-reference/video/generation/api/v2-video-generation.json)
