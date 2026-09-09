---
name: api-boundary-test-case-design
description: Audit, design, or implement contract-complete API test matrices, cases, and scenario suites using equivalence partitioning, boundary-value analysis, decision tables, negative testing, and cost-aware execution tiers. Use for REST or OpenAI-compatible API parameter suites and case catalogs; do not use for ordinary unit-test implementation with no external API contract.
---

# API Boundary Test Case Design

Build the smallest suite that demonstrates the documented contract at its valid, invalid, and interacting boundaries. Keep capability smoke coverage distinct from parameter-boundary coverage.

## Workflow

1. Establish whether the request is an audit, a design, an implementation, or an authorized live execution. Do not turn an audit into code changes or a design into paid requests.
2. Inspect the repository's case schema, loader, assertion operators, model routing, and neighboring cases before proposing artifacts. In this repository, follow `AGENTS.md` and use the codebase graph before source fallback for structural discovery.
3. Verify the current provider contract from primary official documentation. Record the retrieval date and direct source URL for every material header, field, nested field, limit, default, enum, dependency, workflow transition, and error expectation. Treat undocumented behavior as observed behavior, not contract.
4. Build a contract inventory and parameter constraint matrix before writing cases. Read [coverage-matrix.md](references/coverage-matrix.md) for the completeness gate, required columns, coverage-state rules, and selection rules.
5. For an LLM or OpenAI-compatible API, also read [llm-api-checklist.md](references/llm-api-checklist.md). For `E:\GITHUB\llm-test`, read [llm-test-studio-cases.md](references/llm-test-studio-cases.md) before editing case files.
6. Select cases by input shape:
   - ordered numeric, length, or cardinality domain: boundary-value analysis;
   - enum, nullable, optional, or polymorphic input: equivalence partitions;
   - cross-parameter rules: decision table, then pairwise reduction for unconstrained combinations;
   - workflow or multi-turn behavior: state transitions;
   - undocumented robustness questions: explicitly labeled exploratory or characterization cases.
7. Give every case one primary contract claim, explicit preconditions, exact request delta, expected transport and business outcome, stable assertions, model applicability, execution tier, and source reference.
8. Audit the proposed suite against the matrix. A parameter merely present in a happy-path request is not covered; an enum sweep is not numeric boundary testing; an HTTP 2xx or accepted asynchronous task is not business success.
9. Implement only the approved scope. Reuse existing assertion operators when they can prove the claim; extend the runner with tests when they cannot. Never weaken an assertion merely to make a provider response pass.
10. When creating or materially extending a model case catalog in LLM Test Studio, define its execution profiles and generate scenario Suite files with [suite-generation.md](references/suite-generation.md). Keep reviewed memberships in the current manifest and generate the Suite files from it.
11. Validate schema/loading, targeted tests, generated catalog consistency, and diff hygiene. Report separately what was designed, statically validated, live-executed, deferred for cost, or blocked by missing authoritative limits.

## Scope and completeness gates

Before generating case artifacts:

1. Write one explicit scope statement naming provider, endpoint, exact model/version, included modes, excluded modes, and documentation retrieval date. If the user names a model or endpoint without narrowing its parameters, the matrix scope is the full documented request and lifecycle contract for that target. Do not silently narrow it to a cheaper capability subset.
2. Inventory the contract in two passes:
   - structural: method/path, headers, top-level fields, nested fields, collection item fields, response fields, and workflow states;
   - behavioral: required/optional rules, defaults, valid and invalid partitions, ranges, lengths, cardinalities, formats, mutual exclusions, combined budgets, state transitions, and expiry rules.
3. Map every inventory item to a matrix row or an explicit out-of-scope row with rationale. Do not start case generation while a documented item has no row.
4. Keep design coverage separate from execution evidence. Missing credentials, deterministic assets, assertion capability, or spend authorization changes a row to `deferred` or `blocked`; it does not remove the row or its required scenarios.
5. Apply the coverage-state rules in [coverage-matrix.md](references/coverage-matrix.md). Do not mark a row `covered` unless implemented cases and stable assertions prove every mandatory scenario selected for that row.

For asynchronous APIs, state whether invalid input is rejected at admission, after task creation, or at either documented phase. A negative assertion must follow the task to a terminal failure when admission success is not business success.

## Boundary selection

- Inclusive ordered range `[min,max]`: prefer `min-1`, `min`, a representative interior value, `max`, `max+1`. Add `min+1` or `max-1` only for asymmetric or historically fragile behavior.
- Fixed value `v`: test omitted/default behavior, explicit `v`, and one nearby or type-valid non-`v` rejection. Do not describe a fixed-value contract as a range.
- Maximum collection or string size `n`: test empty only when meaningful, then `1`, `n`, and `n+1`; test byte and character limits separately when encoding matters.
- Enum: test omitted/default, every supported value whose behavior differs, one unknown value, wrong type, and case/whitespace variants only when normalization is part of the contract.
- Conditional parameter: cover every valid decision-table row and each distinct invalid row. For example, test `top_logprobs` both with and without its required `logprobs=true` prerequisite.
- Combined budgets: calculate the actual invariant, such as `input_tokens + max_completion_tokens <= context_window`; do not test either field in isolation and claim combined-limit coverage.

## Cost and execution boundary

Classify cases before execution:

- `T0 static`: schema, loader, local validation, and request-shape checks; no provider call.
- `T1 smoke`: short, low-cost live calls proving ordinary success and error contracts.
- `T2 admission-boundary`: provider calls crafted to trigger admission validation while minimizing generated output.
- `T3 expensive`: long-context, large tool arrays, load, cache, concurrency, or repeated statistical cases. Keep these out of default suites unless the user explicitly authorizes their cost and environment.

Never infer authorization to spend provider quota from a request to design or add cases. Redact credentials and sensitive payloads from artifacts and reports.

Cost controls execution, not design completeness. Define expensive or asset-dependent scenarios in the matrix and keep their executable cases disabled or deferred until the user authorizes the required assets and spend.

## Inventory helper

Use `scripts/audit_case_coverage.py` to inventory repository-owned `case.json` files before making exhaustive claims:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/audit_case_coverage.py data/cases/openai-chat --key-prefix must.
```

The script reports observed dimensions, request parameters, assertion operators, and explicit 4xx HTTP rejection assertion counts. It does not know the official contract and cannot detect a documented parameter that is absent from both the matrix and case files. Compare its output with the independently built contract inventory and current primary documentation; never use the inventory output alone to claim completeness.

## Scenario Suite generator

For LLM Test Studio model catalogs, use `scripts/build_scenario_suites.py` after the case files and their execution modes are stable. Read [suite-generation.md](references/suite-generation.md) before creating or changing the profile manifest.

The manifest stores current Suite documents with reviewed ordered Case ID references and explicit input bindings. The generator validates all references before writing and rejects removed kind selectors and model routing fields. Use the contract matrix to justify membership; names and HTTP acceptance do not establish boundary coverage.

Preview first, write only after inspecting the counts, then use `--check` in validation. The generator never authorizes live execution and never deletes unrelated Suite files.

## Completion standard

A boundary suite is complete only for the explicitly bounded contract scope and only when the contract inventory has no unmapped item. State uncovered parameters and combinations, disabled or deferred cases, provider-documentation conflicts, assertion limitations, and live-execution level. Never call a foundation, compatibility, or smoke suite comprehensive boundary coverage without matrix evidence. If any row is `partial`, `missing`, `deferred`, or `blocked`, describe the suite as incomplete and list those rows. For a newly created LLM Test Studio model catalog, completion also requires a checked-in Suite profile manifest, generated Suite files, and a successful generator `--check`, unless the user explicitly limits the work to case design only.
