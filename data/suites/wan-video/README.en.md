# Wan 3.0 scenario Suites

[English](README.en.md) | [简体中文](README.md)

Wan 3.0 standard and Prime share the same Case contract, but each Suite binds to one upstream model through `model_target`. Each model has these five Suite categories:

| Directory suffix | Suite key suffix | Case count | Directly runnable | Scenario |
|---|---|---:|---|---|
| `-connectivity` | `.connectivity` | 1 | Yes | Minimum-cost connectivity and complete asynchronous task workflow |
| `-basic` | `.basic` | 6 | Yes | Basic generation, ratio, audio, prompt expansion, watermark, and required-field validation |
| `-negative` | `.negative` | 39 | Yes | Rejection of parameter types, enums, missing required fields, and numeric boundaries |
| `-automatic` | `.automatic` | 74 | Yes | Complete automatic contract tests: 35 positive + 39 negative |
| No suffix | No suffix | 191 | No | Complete contract matrix, including 117 disabled media/environment/oracle templates |

The hierarchy is connectivity ⊂ basic functionality ⊂ complete automatic tests ⊂ complete matrix. Parameter rejection is the negative subset of complete automatic tests.

Selecting a Suite and starting a run executes directly, without a separate payment checkbox. `-automatic` includes longer-duration successful generations and should not be treated as a low-cost smoke test. The 191-Case matrix can become a runnable Plan only after the disabled templates satisfy their prerequisites and are individually enabled.
