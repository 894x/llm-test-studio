# LLM Test Studio case integration

Use this reference only inside `E:\GITHUB\llm-test`.

## Discovery

1. Follow the repository `AGENTS.md` graph-first discovery rules.
2. Inspect `internal/casecodec`, `internal/application/casecatalog`, `internal/domain/test_case.go`, `cases/bundle.go`, and neighboring `case.json` definitions relevant to the target suite.
3. Trace how the selected assertion `kind` is executed before claiming it proves a business outcome.
4. Check built-in suite filtering, `model_targets`, enabled/default flags, execution mode, and persisted revision behavior.
5. When creating or materially extending a model catalog, read [suite-generation.md](suite-generation.md) and classify cases into execution profiles before finalizing the catalog.

## Artifact rules

- Preserve `schema_version`, stable case keys, model applicability, execution mode, and repository naming conventions.
- Give each case one primary boundary or dependency claim. Do not duplicate the same request under several report labels when an aggregate can derive them.
- Use an existing assertion kind only when it can distinguish the expected positive or negative contract outcome. Add focused runner tests before introducing a new kind.
- Keep deterministic fixtures repository-owned. Do not fabricate provider-scoped file IDs, asset IDs, or credentials.
- Separate first-party provider baselines from downstream compatibility or characterization behavior.
- Keep T3 cases disabled or outside the default plan unless their spend and environment are explicitly authorized.
- Store semantic Suite membership such as connectivity and basic functionality as explicit keys; derive mechanical Suite membership such as automatic rejection, automatic regression, and complete coverage from metadata selectors.
- Generate Suite files from the checked-in profile manifest. Do not maintain selector-derived lists by hand.

## Validation

Run checks proportional to the change: case JSON parsing and embedded-bundle tests; case import/conversion and model-target tests; assertion-runner tests for changed kinds; `build_scenario_suites.py --check`; built-in suite/plan seed tests when membership changes; `go test -p 1 ./...` for a completed backend/catalog change; frontend checks only when DTOs, bindings, or UI behavior changed; and `git diff --check` with explicit changed-file inspection.

Do not run a provider-backed case merely because its file was added. Report static validation separately from live T1/T2/T3 execution.
