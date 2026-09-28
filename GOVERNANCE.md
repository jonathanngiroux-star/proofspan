# GOVERNANCE.md — Proofspan license & change rules

## License split
- **Core** (schema, converters, runtime, CLI, self-host SCIM): MIT — see LICENSE.
- **Cloud tier** (any code under `cloud/` when it exists): Business Source License 1.1, Change License MIT, Change Date 2030-09-27 — see LICENSE-CLOUD.

## Hard rules
- DCO-only contributions (`git commit -s`). **No CLA, ever.**
- BSL/ELv2 is permitted **only on the cloud tier**. The core stays MIT permanently.
- MIT-only on the cloud tier is prohibited until 10 invoiced Cloud Pro teams (anti-cloud-stripping window).
- Core code must never import cloud-tier code.
- SSO/SCIM ships in the self-host base; it is never a cloud-only gate.

## License changes
Any change to either license requires all three:
1. 4/4 maintainer consensus (all maintainers; currently 1)
2. 30-day public notice posted on the repo
3. A stated migration path for existing users and contributors
