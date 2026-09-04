---
name: api-boundary-test-case-design
description: Audit, design, or implement contract-based API test cases using equivalence partitioning, boundary-value analysis, decision tables, negative testing, and cost-aware execution tiers. Use for REST or OpenAI-compatible API parameter suites and case catalogs; do not use for ordinary unit-test implementation with no external API contract.
---

# API Boundary Test Case Design

Build the smallest suite that demonstrates the documented contract at its valid, invalid, and interacting boundaries. Keep capability smoke coverage distinct from parameter-boundary coverage.

## Workflow

1. Establish whether the request is an audit, a design, an implementation, or an authorized live execution. Do not turn an audit into code changes or a design into paid requests.
2. Inspect the repository's case schema, loader, assertion kinds, model routing, and neighboring cases before proposing artifacts. In this repository, follow `AGENTS.md` and use the codebase graph before source fallback for structural discovery.
3. Verify the current provider contract from primary official documentation. Record the retrieval date and direct source URL for every material limit, default, enum, dependency, and error expectation. Treat undocumented behavior as observed behavior, not contract.
4. Build a parameter constraint matrix before writing cases. Read [coverage-matrix.md](references/coverage-matrix.md) for the required columns and selection rules.
5. For an LLM or OpenAI-compatible API, also read [llm-api-checklist.md](references/llm-api-checklist.md). For `E:\GITHUB\llm-test`, read [llm-test-studio-cases.md](references/llm-test-studio-cases.md) before editing case files.
6. Select cases by input shape:
   - ordered numeric, length, or cardinality domain: boundary-value analysis;
   - enum, nullable, optional, or polymorphic input: equivalence partitions;
   - cross-parameter rules: decision table, then pairwise reduction for unconstrained combinations;
   - workflow or multi-turn behavior: state transitions;
   - undocumented robustness questions: explicitly labeled exploratory or characterization cases.
7. Give every case one primary contract claim, explicit preconditions, exact request delta, expected transport and business outcome, stable assertions, model applicability, execution tier, and source reference.
8. Audit the proposed suite against the matrix. A happy-path case does not cover rejection behavior; an enum sweep is not numeric boundary testing; an HTTP 2xx or completed stream is not business success.
9. Implement only the approved scope. Reuse existing assertion kinds when they can prove the claim; extend the runner with tests when they cannot. Never weaken an assertion merely to make a provider response pass.
10. Validate schema/loading, targeted tests, generated catalog consistency, and diff hygiene. Report separately what was designed, statically validated, live-executed, deferred for cost, or blocked by missing authoritative limits.

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

## Inventory helper

Use `scripts/audit_case_coverage.py` to inventory repository-owned `case.json` files before making exhaustive claims:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/audit_case_coverage.py cases/kimi-k3 --model kimi-k3
```

The script reports observed dimensions, request parameters, assertion kinds, and heuristic negative-case counts. It does not know the official contract, so compare its output with the constraint matrix and current primary documentation.

## Completion standard

A boundary suite is complete only for the explicitly bounded contract scope. State uncovered parameters and combinations, disabled or deferred cases, provider-documentation conflicts, assertion limitations, and live-execution level. Never call a foundation, compatibility, or smoke suite comprehensive boundary coverage without matrix evidence.
