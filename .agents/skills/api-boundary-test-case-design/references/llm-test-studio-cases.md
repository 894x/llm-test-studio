# LLM Test Studio case integration

Use this reference inside `E:\GITHUB\llm-test` and its worktrees.

Follow AGENTS.md graph discovery rules. Inspect `internal/casecodec`, `internal/testspec`, `internal/protocols`, `internal/application/casecatalog`, `data/cases` and neighboring definitions. Trace the selected assertion operator and observation source before claiming a business outcome.

## Current artifact contract

- Case file schema 2 has an explicit stable ID and `definitions` keyed by protocol. Each value is a complete native Spec with explicit inputs, request body and assertions, plus optional operation/workflow. Keep one logical Case and ID across protocols. Empty assertions mean observation only.
- Inputs use explicit template references. Cases do not own the runtime model, credential, URL or load policy. Do not add model targets, aliases or old kind-based dispatch.
- Suite schema 1 holds ordered Case references and explicit input bindings. Plan entries target a Case or Suite with per-entry load, warmup and protocol settings. The Run binds one model/channel/credential.
- Give each Case a primary contract claim. HTTP 2xx and task admission alone do not prove business success; waiting belongs in the workflow and terminal success belongs in assertions.
- Keep fixtures repository-owned and never fabricate provider asset IDs or credentials. Keep expensive Cases disabled or outside automatic profiles unless execution is authorized.
- Generate Suites from reviewed current manifests using [suite-generation.md](suite-generation.md). Reject unsupported old files without mutating them; historical upgrades belong in separately invoked temporary scripts.

## Validation

Validate current Case parsing, protocol request preparation and assertion evaluation; all bundled Suite references and input bindings; the generator's `--check`; Plan seeds; and old-format rejection without file mutation. Run relevant Go tests, frontend checks for changed DTO/rendering contracts, and `git diff --check`.

Keep static/fixture evidence distinct from live provider execution. Adding an authored Case does not authorize spending provider quota.
