# MiniMax H3 video generation API boundary cases

[English](README.en.md) | [简体中文](README.md)

This directory covers `MiniMax-H3` request parameters on MiniMax's native `POST /v2/video_generation` endpoint and the workflow of polling a newly created task to a terminal state with `GET /v2/query/video_generation/{task_id}`. It is not an OpenAI-compatible API or an alias for Seedance, Wan Video, or `MiniMax-H3-Max`.

## Scope

- Provider/version: MiniMax Video V2, with model `MiniMax-H3` only.
- Modes: text-to-video, first/last-frame image-to-video, and multimodal reference-to-video.
- Contracts: Bearer authentication, all top-level request fields, the `content` union and role decision table, media formats and all documented boundaries, creation responses, single-task polling, terminal results, and usage.
- Excluded: `MiniMax-H3-Max`, task listing, cancellation/deletion, H3-Context-IR, video regeneration, and undocumented behavior.
- Official constraints retrieved on 2026-09-05.

## Case composition

There are 149 cases, all `enabled=true`, `default=false`, and bound only to `model_targets=["MiniMax-H3"]`:

- 48 automatic: one minimal successful generation, 45 HTTP 400 parameter rejections, and two HTTP 401 authentication rejections.
- 101 manual: 65 positive boundaries requiring paid generation or media, and 36 negative boundaries requiring deterministic media to isolate the primary assertion.
- Manual cases use `{{H3_*}}` fixture markers. `options.fixture_requirements` records format, dimensions, duration, cardinality, or byte boundaries. Do not change them to automatic before replacing the markers with real repository-controlled media.

## Scenario Suites

The same cases form five Suites by execution purpose, without copying or changing their definitions:

| Suite | Count | Scenario | Execution requirements |
|---|---:|---|---|
| MiniMax H3 connectivity | 3 | Check routing, valid/invalid authentication, creation, polling, and successful terminal state after deployment | Includes one paid 4-second generation; no external media fixtures |
| MiniMax H3 basic functionality | 24 | Release acceptance for text-to-video, image-to-video, references, callbacks, watermark, and usage | Eight automatic and 16 manual cases; media fixtures, callback endpoint, and budget required |
| MiniMax H3 parameter rejection | 45 | Gateway validation and upstream parameter-contract regression; HTTP 400 rejection contracts only | All automatic; no valid generation tasks or external media fixtures |
| MiniMax H3 automatic core regression | 48 | CI or routine regression with minimal media dependencies; minimal success, 45 parameter rejections, and two authentication rejections | No external media fixtures; includes one paid 4-second generation |
| MiniMax H3 complete video boundaries | 149 | Version upgrades, contract-drift audits, and full release certification | 48 automatic and 101 manual cases; complete fixtures and an explicit budget required |

Connectivity is a subset of basic functionality, which is a subset of the complete boundary Suite. Parameter rejection contains exactly 45 automatic `minimax_video_task_rejected` cases. Automatic core regression is an independent `execution_mode=automatic` slice containing all 48 automatic cases.

All five Suites are generated from [`MiniMax-H3.suite-profiles.json`](../../suites/minimax-video/MiniMax-H3.suite-profiles.json). After adding or changing cases, run:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py `
  --cases-root cases/minimax-video `
  --suites-root suites/minimax-video `
  --manifest suites/minimax-video/MiniMax-H3.suite-profiles.json `
  --write
```

Before committing, replace `--write` with `--check` to verify that generated files match case metadata.

## Runner assertions

- Parameter rejection passes only with HTTP 400, `error.type=bad_request_error`, `error.http_code="400"`, and a nonempty message.
- Authentication rejection passes only with HTTP 401, `error.type=authorized_error`, `error.http_code="401"`, and a nonempty message.
- Business success requires creation of a `task_id` and polling to terminal state; HTTP 200 or successful creation alone is insufficient.
- Terminal state must preserve task ID, model, `task_type=generation`, and `modality=video`, and provide an HTTP(S) video URL without userinfo.
- Cases may require exact resolution, duration, ratio, usage fields, and `total_seconds=input_seconds+output_seconds`.

## Execution safety

Video generation incurs charges. Use `--dry-run` to inspect the 48 automatic requests before execution if needed. Live runs retain `--concurrency 1`; clicking start or invoking the run command executes directly. API Audit does not automatically load manual cases. Prepare the matrix's deterministic fixtures, callback endpoint, and budget before converting them individually to automatic.

This case expansion was verified through T0 schema checks, mock servers, unit/integration tests, and dry-run only, without live MiniMax calls.

## Official references

- [Create a video generation task](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-create)
- [Query a task](https://platform.minimaxi.com/docs/api-reference/video-generation-v2-query)
- [Video V2 OpenAPI](https://platform.minimaxi.com/docs/api-reference/video/generation/api/v2-video-generation.json)
