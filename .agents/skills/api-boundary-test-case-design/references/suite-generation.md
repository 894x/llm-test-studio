# Scenario Suite generation for LLM Test Studio

Use this reference when creating or materially extending a model case catalog under `cases/<protocol>`. It turns one model-scoped case corpus into deterministic scenario Suite files without duplicating case definitions.

## Profile design

Prefer these profiles when they match the model and runner. Omit or rename a profile only with an explicit rationale.

| Profile | Membership rule | Purpose |
|---|---|---|
| Connectivity | Explicit keys | Smallest valid business success plus stable authentication or routing failures needed to prove the endpoint is reachable |
| Basic functionality | Explicit keys | One representative for each common business mode and critical optional feature; include only deliberate acceptance and essential rejection cases |
| Parameter rejection | Selector | Enabled automatic cases using the provider's parameter-rejection assertion kind; exclude authentication, safety, and terminal task failures unless they are intentionally part of the profile |
| Automatic regression | Selector | Every enabled case with `execution_mode=automatic` |
| Complete | Selector | Every case applicable to the model, including manual, expensive, and disabled templates; name the Suite accordingly when disabled templates are present |

Connectivity and basic membership require product judgment, so list their case keys explicitly. Selector-derived profiles must stay mechanical. Do not use name substrings such as `invalid` or `error` to classify rejection cases; select the exact assertion kind or kinds traced through the runner.

Suite membership does not change execution authorization. A Suite containing a paid success case still requires the repository's paid-run confirmation, and manual cases still require their fixtures.

## Manifest

Check in one manifest per model near its Suite files, for example `suites/minimax-video/MiniMax-H3.suite-profiles.json`:

```json
{
  "schema_version": 1,
  "protocol": "example-api",
  "model_target": "example-v1",
  "profiles": [
    {
      "directory": "example-v1-connectivity",
      "key": "example-api.example-v1.connectivity",
      "name": "Example V1 connectivity",
      "case_keys": ["example.smoke", "example.authorization.invalid"]
    },
    {
      "directory": "example-v1-parameter-rejection",
      "key": "example-api.example-v1.parameter-rejection",
      "name": "Example V1 parameter rejection",
      "selector": {
        "enabled": true,
        "execution_modes": ["automatic"],
        "kinds": ["example_task_rejected"]
      }
    },
    {
      "directory": "example-v1-automatic",
      "key": "example-api.example-v1.automatic",
      "name": "Example V1 automatic regression",
      "selector": {
        "enabled": true,
        "execution_modes": ["automatic"]
      }
    },
    {
      "directory": "example-v1-complete",
      "key": "example-api.example-v1.complete",
      "name": "Example V1 complete",
      "selector": {}
    }
  ]
}
```

Each profile must define exactly one of `case_keys` or `selector`. Selectors support `enabled`, `execution_modes`, `kinds`, `dimensions`, and `severities`. All selector fields are conjunctive; values within an array are alternatives.

The generator always filters by `protocol` and model applicability. Empty `model_targets` apply globally; otherwise the manifest's `model_target` must be present. Explicit unknown or inapplicable keys are errors. Source order is the sorted case-file path, while explicit profile order is preserved.

## Commands

Preview without writing:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py `
  --cases-root cases/<protocol> `
  --suites-root suites/<protocol> `
  --manifest suites/<protocol>/<model>.suite-profiles.json
```

After reviewing profile counts, write the generated files:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py `
  --cases-root cases/<protocol> `
  --suites-root suites/<protocol> `
  --manifest suites/<protocol>/<model>.suite-profiles.json `
  --write
```

Check for missing or stale generated files in validation and CI:

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py `
  --cases-root cases/<protocol> `
  --suites-root suites/<protocol> `
  --manifest suites/<protocol>/<model>.suite-profiles.json `
  --check
```

`--write` atomically creates or replaces only the manifest's target `suite.json` files. It does not delete other Suite directories. `--check` compares parsed JSON documents, so harmless whitespace does not cause drift.

## Repository integration

After generation:

1. Inspect every profile count and its automatic/manual, success/rejection, fixture, and paid-execution composition.
2. Add or update embedded-Suite tests that verify expected profiles, unique keys, semantic subset relationships, and exact selector-derived membership.
3. Update catalog cardinality fixtures intentionally; do not make the generator rewrite Go tests.
4. Run the generator with `--check`, targeted Suite/catalog tests, and the repository validation required by [llm-test-studio-cases.md](llm-test-studio-cases.md).
5. Report generated design separately from any live provider execution.
