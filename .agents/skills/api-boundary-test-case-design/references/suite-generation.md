# Current scenario Suite generation

Case files use schema 2, explicit stable IDs and protocol-keyed `definitions` with explicit inputs, request bodies and assertions. Suite files use schema 1: ordered Case ID references and explicit mappings from Suite inputs to declared Case inputs. The Run binds one model, channel and credential. A Suite never selects a model or overrides a Case body implicitly.

The manifest has exactly `schema_version: 1`, `protocol`, and `profiles`. Each profile contains `directory` plus the complete current Suite document (`schema_version`, `key`, `name`, `protocol`, `description`, `cases`, `inputs`). The manifest format is a single authoring format, not a historical decoder.

```json
{
  "schema_version": 1,
  "protocol": "openai-chat",
  "profiles": [{
    "directory": "example-connectivity",
    "schema_version": 1,
    "key": "example.connectivity",
    "name": "Connectivity",
    "protocol": "openai-chat",
    "description": "Reviewed smoke membership",
    "cases": [{"case_id": "<current stable Case ID>"}],
    "inputs": []
  }]
}
```

Use the Case's explicit stable ID; never derive it from protocol/key. The selected protocol must exist in every member's `definitions`. Connectivity/basic profiles require semantic review; rejection profiles must be justified by explicit transport or terminal-task assertions. Automatic regression includes enabled automatic Cases; complete catalogs may include manual Cases requiring fixtures. Disabled binding tests cannot become executable merely by including them in a Suite.

Store reviewed memberships in the manifest. The generator validates the full manifest before writing any file, preserves reference order, checks input mappings, and writes atomically. It rejects removed `model_target`, `model_targets`, `case_keys`, `selector`, `kind`, and `quick_test` contracts. It does not convert historical input or infer coverage from Case names.

```powershell
python .agents/skills/api-boundary-test-case-design/scripts/build_scenario_suites.py --cases-root data/cases/minimax-video --suites-root data/suites/minimax-video --manifest data/suites/minimax-video/MiniMax-H3.suite-profiles.json
# After inspecting membership and counts, use the same arguments with --write.
# Verify checked-in output with --check.
python -m unittest discover -s .agents/skills/api-boundary-test-case-design/tests
```

Generation never authorizes provider requests. Keep assertions, provider-contract matrix and cost tiers separately reviewed; generated membership alone is not coverage evidence.
