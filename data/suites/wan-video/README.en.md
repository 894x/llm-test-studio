# Wan 3.0 scenario Suites

[English](README.en.md) | [简体中文](README.md)

Wan 3.0 standard and Prime share current protocol Cases. Suite schema 2 contains references and input mappings; the Run binds the upstream model and credential. Four model mutation Cases are disabled and excluded from Suite membership. Each named model profile retains these five scenarios:

| Directory suffix | Suite key suffix | Case count | Directly runnable | Scenario |
|---|---|---:|---|---|
| `-connectivity` | `.connectivity` | 1 | Yes | Minimum-cost connectivity and complete asynchronous task workflow |
| `-basic` | `.basic` | 6 | Yes | Basic generation, ratio, audio, prompt expansion, watermark, and required-field validation |
| `-negative` | `.negative` | 35 | Yes | Rejection of parameter types, enums, missing required fields, and numeric boundaries |
| `-automatic` | `.automatic` | 70 | Yes | Complete automatic contract tests: 35 positive + 35 negative |
| No suffix | No suffix | 187 | No | Complete contract matrix, including 117 disabled media/environment/oracle templates |

The hierarchy is connectivity ⊂ basic functionality ⊂ complete automatic tests ⊂ complete matrix. Parameter rejection is the negative subset of complete automatic tests.

Selecting a Suite and starting a run executes directly, without a separate payment checkbox. `-automatic` includes longer-duration successful generations and should not be treated as a low-cost smoke test. The 187-Case matrix can become a runnable Plan only after the disabled templates satisfy their prerequisites and are individually enabled.
