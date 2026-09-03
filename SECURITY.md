# Security policy

## Supported versions

Security fixes are applied to the latest released version and the current `main` branch. Older releases are not guaranteed to receive patches.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability or include credentials, private endpoints, customer data, or exploit details in public discussions.

Use GitHub's **Report a vulnerability** action on the repository's Security tab to send a private report. If private vulnerability reporting is not available, contact **@894x** through a private contact method listed on the maintainer's GitHub profile. Include:

- affected version or commit;
- impact and prerequisites;
- minimal reproduction steps;
- suggested mitigation, if known.

You should receive an acknowledgement within seven days. Disclosure timing will be coordinated after the issue is reproduced and a remediation plan exists.

## Scope notes

LLM Test Studio sends requests to model endpoints configured by the user and stores secrets through the operating-system credential store. Reports and diagnostics should be reviewed before sharing because model inputs or outputs may contain sensitive information.
