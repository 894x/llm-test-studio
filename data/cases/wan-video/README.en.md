# Wan video API boundary cases

[English](README.en.md) | [简体中文](README.md)

This directory stores executable text-to-video contract cases by Wan model version. Wan uses the DashScope asynchronous task protocol: after creation, poll `/api/v1/tasks/{task_id}` using `output.task_id`; success requires `output.video_url`. These are not aliases for Seedance cases.

The current Wan 3.0 catalog retains 191 definitions: 70 automatic Cases, 117 fixture-dependent disabled templates, and 4 disabled model mutation Cases. Those 4 binding claims are excluded from every Suite, leaving 187 references in each complete matrix Suite. The Run chooses the upstream model and credential; no Case or Suite model routing remains. Cases use schema 3 and explicit assertions, Suites use schema 2 and explicit input bindings.

## Version scope

- Wan 3.0: `wan3.0-video`, `wan3.0-video-prime`
- Wan 2.7: `wan2.7-t2v`, `wan2.7-t2v-2026-06-12`
- Wan 2.6: `wan2.6-t2v`
- Wan 2.5: `wan2.5-t2v-preview`
- Wan 2.2: `wan2.2-t2v-plus`
- Wan 2.1: `wanx2.1-t2v-turbo`, `wanx2.1-t2v-plus`

All bundled cases are non-default. Wan 3.0 has 191 concrete Cases; see the [Wan 3.0 Case matrix](wan3-case-matrix.md): 70 automatic Cases cover fields, types, enums, defaults, and numeric boundaries that need no external media, while 117 disabled templates describe media, environment, and output-oracle prerequisites.

Automatic negative cases may pass on HTTP 400 at creation or on a task entering `FAILED` after creation. Both phases require stable provider `code` and `message` values, with an error code in the documented `InvalidParameter` family. Billing, authentication, throttling, gateway failures, and a 2xx task acceptance alone cannot count as correct boundary rejection.

## Execution safety

Video generation incurs charges. Use `--dry-run` to inspect selected versions, request bodies, and model injection before execution if needed. Live runs retain `--concurrency 1`; clicking start or invoking the run command executes directly. This implementation was verified using static checks, mock servers, and dry-run only, without live Bailian calls.

Cases requiring external assets or greater spending, including media inputs, audio inputs, and maximum-duration success, are stored as concrete `enabled=false` templates. They use `fixture://wan3/...` to describe deterministic media and record activation requirements in the Case description and matrix. They do not invent provider file IDs or enter execution plans.

## Wan 3.0 Suite layers

`wan3.0-video` and `wan3.0-video-prime` each provide the same five scenario Suites, separating low-cost smoke tests, automatic contract validation, and the complete matrix awaiting fixtures:

| Scenario | Case count | Composition | Purpose |
|---|---:|---|---|
| Connectivity | 1 | One positive 2-second/480P Case | Minimal-cost authentication, routing, polling, and result URL verification |
| Basic functionality | 6 | Five short positive Cases + one required-field negative | Basic generation, ratio, audio, prompt expansion, watermark, and input validation |
| Parameter rejection | 35 | 35 automatic negative Cases | Stable rejection of types, enums, required fields, and numeric boundaries at creation or terminal state |
| Complete automatic tests | 70 | 35 automatic positive + 35 automatic negative Cases | Full automatic contract set without external fixtures; includes longer-duration Cases and costs more than basic tests |
| Complete matrix with disabled templates | 187 | 70 automatic + 117 disabled templates | Full contract review and later activation; not directly runnable as a Plan |

Creating a Suite makes no provider calls; execution begins only when the user starts a run. Select the first four automatic Suite categories for execution. The 187-Case Suite includes disabled templates that must satisfy their fixture/oracle prerequisites and be enabled before becoming a runnable Plan.

## References

- [Wan 3.0 video generation API](https://help.aliyun.com/zh/model-studio/wan3-video-generation-api-reference)
- [Wan 2.7 text-to-video API](https://help.aliyun.com/zh/model-studio/text-to-video-api-reference)
- [Wan 2.1–2.6 text-to-video API](https://help.aliyun.com/zh/model-studio/legacy-wan-text-to-video-api-reference)
- [Bailian API error codes](https://help.aliyun.com/zh/model-studio/error-code/)
- [User-provided Bailian console model page](https://bailian.console.aliyun.com/cn-beijing?tab=api#/api/?type=model&url=3049634)

Constraints were extracted on 2026-09-05. The console page requires authentication and frontend rendering; this matrix uses public official Alibaba Cloud API documentation as its verifiable basis.
